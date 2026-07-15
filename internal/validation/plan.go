// Package validation resolves app-configured validation profiles into
// executable plans and runs those plans. It owns profile inheritance and
// cycle detection, changed-file profile matching, plan ordering, and step
// execution with captured output. CLI argument parsing, response envelopes,
// and text rendering stay in cmd/scenery.
package validation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
)

// Diagnostic is one validation configuration or execution diagnostic. Its
// JSON shape matches the scenery CLI check diagnostic contract.
type Diagnostic struct {
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	File            string `json:"file,omitempty"`
	Line            int    `json:"line,omitempty"`
	Column          int    `json:"column,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// Selection records how validation profiles were selected for a plan.
type Selection struct {
	Mode             string         `json:"mode"`
	Requested        []string       `json:"requested,omitempty"`
	Base             string         `json:"base,omitempty"`
	ChangedFiles     []string       `json:"changed_files,omitempty"`
	MatchedProfiles  []ProfileMatch `json:"matched_profiles,omitempty"`
	ResolvedProfiles []string       `json:"resolved_profiles"`
}

// ProfileMatch records one profile selected by changed-file path globs.
type ProfileMatch struct {
	Profile      string   `json:"profile"`
	MatchedPaths []string `json:"matched_paths"`
	MatchedFiles []string `json:"matched_files"`
}

// PlanStep is one resolved, executable validation step.
type PlanStep struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Profile  string            `json:"profile,omitempty"`
	Command  []string          `json:"command,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
	Artifact []string          `json:"artifacts,omitempty"`
	Env      map[string]string `json:"-"`
}

// ResolvedPlan is the ordered result of resolving one or more validation
// profiles, including every diagnostic found while resolving.
type ResolvedPlan struct {
	App         inspectdata.AppRef
	Profile     string
	Selection   Selection
	Profiles    []string
	Steps       []PlanStep
	Diagnostics []Diagnostic
}

// StepRef is one parsed validation step reference.
type StepRef struct {
	Raw  string
	Kind string
	Name string
}

// PlanRequest selects which profiles a Planner resolves into a plan.
type PlanRequest struct {
	// Profile is the requested profile name; empty selects the default.
	Profile string
	// Changed selects profiles whose path globs match files changed
	// relative to Base instead of one explicit profile.
	Changed bool
	// Base is the git base ref used when Changed is set.
	Base string
}

// Planner resolves and validates the validation profiles of one configured
// app.
type Planner struct {
	// AppRoot is the app root directory.
	AppRoot string
	// Config is the app's parsed configuration.
	Config appcfg.Config
	// App identifies the app in resolved plans and results.
	App inspectdata.AppRef
	// ValidateTaskTarget reports whether a task step target is a valid
	// code task reference. A nil hook skips task target validation.
	ValidateTaskTarget func(target string) error
}

// ParseStepRef parses one validation step string into its kind and name.
func ParseStepRef(step string) StepRef {
	raw := strings.TrimSpace(step)
	switch {
	case strings.HasPrefix(raw, "profile:"):
		return StepRef{Raw: raw, Kind: "profile", Name: strings.TrimSpace(strings.TrimPrefix(raw, "profile:"))}
	case strings.HasPrefix(raw, "task:"):
		return StepRef{Raw: raw, Kind: "task", Name: strings.TrimSpace(strings.TrimPrefix(raw, "task:"))}
	default:
		return StepRef{Raw: raw, Kind: "builtin", Name: raw}
	}
}

// ResolveProfileName resolves a requested profile name, falling back to the
// configured default and then to the "quick" profile when present.
func (p Planner) ResolveProfileName(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	if strings.TrimSpace(p.Config.Validation.Default) != "" {
		return strings.TrimSpace(p.Config.Validation.Default)
	}
	if _, ok := p.Config.Validation.Profiles["quick"]; ok {
		return "quick"
	}
	return ""
}

// Plan resolves the profiles selected by req into one executable plan.
func (p Planner) Plan(ctx context.Context, req PlanRequest) (ResolvedPlan, error) {
	selection := Selection{Mode: "explicit"}
	profile := p.ResolveProfileName(req.Profile)
	if req.Changed {
		files, err := CollectChangedFiles(ctx, p.AppRoot, req.Base)
		if err != nil {
			return ResolvedPlan{}, err
		}
		selection.Mode = "changed"
		selection.Base = req.Base
		selection.ChangedFiles = files
		profiles := p.selectChangedProfiles(files)
		selection.Requested = profiles
		selection.MatchedProfiles = p.matchChangedProfiles(files)
		if len(profiles) == 0 {
			profile = p.ResolveProfileName("")
			selection.Requested = []string{profile}
		} else if len(profiles) == 1 {
			profile = profiles[0]
		} else {
			profile = strings.Join(profiles, "+")
		}
		return p.multiPlan(profile, profiles, selection)
	}
	selection.Requested = []string{profile}
	return p.NamedPlan(profile, selection)
}

// NamedPlan resolves one explicitly named profile into an executable plan.
func (p Planner) NamedPlan(profile string, selection Selection) (ResolvedPlan, error) {
	return p.multiPlan(profile, []string{profile}, selection)
}

func (p Planner) multiPlan(profileLabel string, profiles []string, selection Selection) (ResolvedPlan, error) {
	plan := ResolvedPlan{App: p.App, Profile: profileLabel, Selection: selection}
	plan.Diagnostics = append(plan.Diagnostics, p.ValidateConfig()...)
	seenProfiles := map[string]bool{}
	for _, profile := range profiles {
		if strings.TrimSpace(profile) == "" {
			continue
		}
		p.addProfile(&plan, profile, nil, seenProfiles, nil)
	}
	plan.Selection.ResolvedProfiles = append([]string(nil), plan.Profiles...)
	return plan, nil
}

func (p Planner) addProfile(plan *ResolvedPlan, name string, stack []string, seenProfiles map[string]bool, inheritedEnv map[string]string) {
	if seenProfiles[name] {
		return
	}
	prof, ok := p.Config.Validation.Profiles[name]
	if !ok {
		plan.Diagnostics = append(plan.Diagnostics, errorDiagnostic("validation profile "+name+" is not configured"))
		return
	}
	for _, active := range stack {
		if active == name {
			plan.Diagnostics = append(plan.Diagnostics, errorDiagnostic("profile cycle detected: "+strings.Join(append(stack, name), " -> ")))
			return
		}
	}
	seenProfiles[name] = true
	plan.Profiles = append(plan.Profiles, name)
	profileEnv := overlayStringMap(inheritedEnv, prof.Env)
	stack = append(stack, name)
	for idx, step := range prof.Steps {
		ref := ParseStepRef(step)
		if ref.Kind == "profile" {
			p.addProfile(plan, ref.Name, stack, seenProfiles, profileEnv)
			continue
		}
		stepID := strings.Join(append(stack, ref.Raw), "/")
		plan.Steps = append(plan.Steps, PlanStep{
			ID:       firstNonEmpty(stepID, fmt.Sprintf("%s/step-%d", name, idx+1)),
			Name:     ref.Raw,
			Kind:     ref.Kind,
			Profile:  name,
			Command:  stepCommand(p.AppRoot, ref),
			CWD:      p.AppRoot,
			Artifact: append([]string(nil), prof.Artifacts...),
			Env:      profileEnv,
		})
	}
}

// ValidateConfig reports every configuration diagnostic for the app's
// validation profiles, including profile cycles.
func (p Planner) ValidateConfig() []Diagnostic {
	cfg := p.Config
	var diags []Diagnostic
	for name, prof := range cfg.Validation.Profiles {
		if !validProfileNameSegment(name) || strings.Contains(name, ":") {
			diags = append(diags, errorDiagnostic("invalid validation profile name "+name))
		}
		if reservedProfileName(name) {
			diags = append(diags, errorDiagnostic("validation profile name "+name+" is reserved by scenery validate"))
		}
		if prof.Cost != "" && prof.Cost != "low" && prof.Cost != "medium" && prof.Cost != "high" {
			diags = append(diags, errorDiagnostic("validation profile "+name+" has invalid cost "+prof.Cost))
		}
		if len(prof.Steps) == 0 {
			diags = append(diags, errorDiagnostic("validation profile "+name+" has no steps"))
		}
		for _, glob := range prof.Paths {
			if strings.TrimSpace(glob) == "" {
				diags = append(diags, errorDiagnostic("validation profile "+name+" has an empty path glob"))
			}
		}
		for _, step := range prof.Steps {
			ref := ParseStepRef(step)
			switch ref.Kind {
			case "profile":
				if ref.Name == "" {
					diags = append(diags, errorDiagnostic("validation profile "+name+" has an empty profile step"))
				} else if _, ok := cfg.Validation.Profiles[ref.Name]; !ok {
					diags = append(diags, errorDiagnostic("validation profile "+name+" references unknown profile "+ref.Name))
				}
			case "task":
				if ref.Name == "" {
					diags = append(diags, errorDiagnostic("validation profile "+name+" has an empty task step"))
					continue
				}
				if p.ValidateTaskTarget != nil {
					if err := p.ValidateTaskTarget(ref.Name); err != nil {
						diags = append(diags, errorDiagnostic(err.Error()))
					}
				}
			case "builtin":
				if !builtinSupported(ref.Name) {
					diags = append(diags, errorDiagnostic("validation profile "+name+" has unsupported step "+ref.Raw))
				}
			}
		}
	}
	defaultProfile := p.ResolveProfileName("")
	if len(cfg.Validation.Profiles) > 0 && defaultProfile == "" {
		diags = append(diags, errorDiagnostic("validation.default is not configured and profile quick does not exist"))
	} else if defaultProfile != "" {
		if _, ok := cfg.Validation.Profiles[defaultProfile]; !ok {
			diags = append(diags, errorDiagnostic("validation default profile "+defaultProfile+" is not configured"))
		}
	}
	for _, cycle := range p.profileCycles() {
		diags = append(diags, errorDiagnostic("profile cycle detected: "+strings.Join(cycle, " -> ")))
	}
	for i := range diags {
		diags[i].File = cfg.SourcePath(p.AppRoot)
	}
	return diags
}

func (p Planner) profileCycles() [][]string {
	cfg := p.Config
	var cycles [][]string
	var walk func(name string, stack []string)
	walk = func(name string, stack []string) {
		for i, active := range stack {
			if active == name {
				cycle := append([]string(nil), stack[i:]...)
				cycle = append(cycle, name)
				cycles = append(cycles, cycle)
				return
			}
		}
		prof, ok := cfg.Validation.Profiles[name]
		if !ok {
			return
		}
		stack = append(stack, name)
		for _, step := range prof.Steps {
			ref := ParseStepRef(step)
			if ref.Kind == "profile" {
				walk(ref.Name, stack)
			}
		}
	}
	names := make([]string, 0, len(cfg.Validation.Profiles))
	for name := range cfg.Validation.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		walk(name, nil)
	}
	return cycles
}

func stepCommand(appRoot string, ref StepRef) []string {
	switch ref.Kind {
	case "task":
		return []string{"scenery", "task", "run", ref.Name, "--app-root", appRoot}
	case "builtin":
		switch ref.Name {
		case "harness", "harness:core":
			return []string{"scenery", "harness", "--app-root", appRoot, "-o", "json"}
		case "harness:ui":
			return []string{"scenery", "harness", "ui", "--app-root", appRoot, "-o", "json"}
		case "check":
			return []string{"scenery", "check", "--app-root", appRoot, "-o", "json"}
		case "test", "test:go":
			return []string{"scenery", "test", "--app-root", appRoot}
		case "generate":
			return []string{"scenery", "generate", "--app-root", appRoot}
		case "generate:sqlc":
			return []string{"scenery", "generate", "sqlc", "--app-root", appRoot}
		case "db:apply":
			return []string{"scenery", "db", "apply", "--app-root", appRoot}
		case "db:seed":
			return []string{"scenery", "db", "seed", "--app-root", appRoot}
		case "db:setup":
			return []string{"scenery", "db", "setup", "--app-root", appRoot}
		}
	}
	return nil
}

func builtinSupported(name string) bool {
	switch name {
	case "harness", "harness:core", "harness:ui", "check", "test", "test:go", "generate", "generate:sqlc", "db:apply", "db:seed", "db:setup":
		return true
	default:
		return false
	}
}

func reservedProfileName(name string) bool {
	switch name {
	case "list", "inspect", "graph", "changed":
		return true
	default:
		return false
	}
}

// validProfileNameSegment mirrors the [A-Za-z0-9_][A-Za-z0-9_-]* segment rule
// used by scenery task targets.
func validProfileNameSegment(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func errorDiagnostic(message string) Diagnostic {
	return Diagnostic{Stage: "validation", Severity: "error", Message: message}
}

func overlayStringMap(base, values map[string]string) map[string]string {
	if len(base) == 0 && len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(values))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
