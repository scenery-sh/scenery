package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/repoinfo"
	"scenery.sh/internal/validation"
)

type validationRunIdentity struct {
	ID                 string  `json:"id"`
	SourceCommit       *string `json:"source_commit"`
	InputRevision      string  `json:"input_revision"`
	FinalInputRevision string  `json:"final_input_revision"`
	InputScope         string  `json:"input_scope"`
	InputsStable       bool    `json:"inputs_stable"`
	ArchivePath        string  `json:"archive_path,omitempty"`
	IdentityError      string  `json:"identity_error,omitempty"`
}

type validationOmission struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// Application commands can consume untracked configuration and client files.
// Bind their workspace bytes, including the configuration, without executing a
// hidden Git process during in-process validation. Managed caches/dependencies
// have independent identities and are outside this explicitly recorded scope.
func validationInputRevision(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".scenery", "node_modules", ".next", "target":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	return repoinfo.HashInputs(root, paths)
}

// A commit is optional for an application outside Git. This read handles both
// ordinary checkouts and worktree .git files without spawning a process.
func validationSourceCommit(root string) *string {
	for current := root; ; current = filepath.Dir(current) {
		git := filepath.Join(current, ".git")
		if data, err := os.ReadFile(git); err == nil && strings.HasPrefix(string(data), "gitdir: ") {
			git = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
			if !filepath.IsAbs(git) {
				git = filepath.Join(current, git)
			}
		}
		if head, err := os.ReadFile(filepath.Join(git, "HEAD")); err == nil {
			value := strings.TrimSpace(string(head))
			if strings.HasPrefix(value, "ref: ") {
				ref := strings.TrimPrefix(value, "ref: ")
				common := git
				if data, err := os.ReadFile(filepath.Join(git, "commondir")); err == nil {
					common = filepath.Join(git, strings.TrimSpace(string(data)))
				}
				if data, err := os.ReadFile(filepath.Join(common, filepath.FromSlash(ref))); err == nil {
					value = strings.TrimSpace(string(data))
				} else {
					data, _ := os.ReadFile(filepath.Join(common, "packed-refs"))
					value = ""
					for _, line := range strings.Split(string(data), "\n") {
						fields := strings.Fields(line)
						if len(fields) == 2 && fields[1] == ref {
							value = fields[0]
							break
						}
					}
				}
			}
			if len(value) == 40 || len(value) == 64 {
				return &value
			}
			return nil
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func newValidationObservation(root string, cfg appcfg.Config, plan validation.ResolvedPlan, opts validateOptions, artifacts validationArtifactContext) validationResultResponse {
	input, err := validationInputRevision(root)
	provenance := harnessreport.CurrentProvenance()
	provenance.Producer = cliProducer()
	result := validationResultResponse{cliPayloadIdentity: newCLIPayloadIdentity(validationResultKind), App: plan.App, Profile: plan.Profile, Selection: plan.Selection,
		Provenance: provenance,
		Run:        validationRunIdentity{ID: artifacts.RunID, SourceCommit: validationSourceCommit(root), InputRevision: input, InputScope: "application workspace files excluding .git, .scenery, node_modules, .next and target"},
		DryRun:     opts.DryRun, SelectedSteps: len(plan.Steps), OmittedSteps: []validationOmission{},
		UnknownStages: []string{"per-case execution, setup, cleanup and result replay for arbitrary application commands"},
		Plan:          validationPlanResponse{cliPayloadIdentity: newCLIPayloadIdentity(validationPlanKind), Provenance: provenance, RunID: artifacts.RunID, DryRun: opts.DryRun, Source: filepath.Join(root, ".scenery.json"), OK: len(plan.Diagnostics) == 0, App: plan.App, Profile: plan.Profile, Selection: plan.Selection, Steps: plan.Steps, Diagnostics: plan.Diagnostics, Profiles: validationProfileRecords(cfg)},
	}
	if err != nil {
		result.Run.IdentityError = err.Error()
	}
	return result
}

func completeValidationObservation(root string, result *validationResultResponse) {
	result.Run.FinalInputRevision, _ = validationInputRevision(root)
	result.Run.InputsStable = result.Run.InputRevision != "" && result.Run.InputRevision == result.Run.FinalInputRevision
	result.ExecutedSteps = len(result.Steps)
	result.Outcome = "passed"
	if !result.OK {
		result.Outcome = "failed"
	}
	if !result.Run.InputsStable {
		result.OK = false
		result.Outcome = "incomplete"
		result.Diagnostics = append(result.Diagnostics, validationDiagnostic("validation input identity", "error", "Application inputs changed or could not be read; the result does not establish proof for the final inputs."))
	}
	executed := map[string]bool{}
	for _, step := range result.Steps {
		executed[step.ID] = true
	}
	for _, step := range result.Plan.Steps {
		if !executed[step.ID] {
			result.OmittedSteps = append(result.OmittedSteps, validationOmission{ID: step.ID, Outcome: "blocked", Reason: "earlier failure or invalid configuration"})
		}
	}
}

func validationStepOutcome(step validation.StepResult) string {
	if step.OK {
		return "passed"
	}
	if errors.Is(step.Err, context.DeadlineExceeded) {
		return "timed_out"
	}
	if errors.Is(step.Err, context.Canceled) {
		return "canceled"
	}
	return "failed"
}

// Publish the exact plan/result transaction before replacing navigation copies.
// Existing run directories are immutable, even on a repeated write request.
func writeValidationResult(root string, result *validationResultResponse) error {
	if result.Run.ID == "" || !filepath.IsLocal(result.Run.ID) || filepath.Base(result.Run.ID) != result.Run.ID || result.Run.ID == "." {
		return fmt.Errorf("invalid application validation run ID")
	}
	base := filepath.Join(root, ".scenery", "harness", "validation")
	runs := filepath.Join(base, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return err
	}
	destination := filepath.Join(runs, result.Run.ID)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("validation run already exists or cannot be inspected: %s", destination)
	}
	staging, err := os.MkdirTemp(runs, ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	result.Run.ArchivePath = filepath.ToSlash(filepath.Join(".scenery", "harness", "validation", "runs", result.Run.ID))
	result.Wrote = filepath.Join(destination, "result.json")
	write := func(path string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		return atomicfile.Write(path, append(data, '\n'), 0o644, atomicfile.Options{})
	}
	if err := write(filepath.Join(staging, "plan.json"), result.Plan); err != nil {
		return err
	}
	if err := write(filepath.Join(staging, "result.json"), result); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return err
	}
	if err := write(filepath.Join(base, "latest.json"), result); err != nil {
		return err
	}
	if result.Profile != "" {
		return write(filepath.Join(base, sanitizeHarnessArtifactFilename(result.Profile+"-latest.json")), result)
	}
	return nil
}
