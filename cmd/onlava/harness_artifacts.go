package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const harnessArtifactEvidenceSchema = "onlava.harness.artifact.v1"

type harnessArtifact struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version,omitempty"`
	Exists        bool   `json:"exists"`
}

type harnessEvidence struct {
	SchemaVersion string                    `json:"schema_version"`
	Command       []string                  `json:"command,omitempty"`
	CWD           string                    `json:"cwd,omitempty"`
	StartedAt     string                    `json:"started_at,omitempty"`
	DurationMS    int64                     `json:"duration_ms"`
	ExitCode      *int                      `json:"exit_code,omitempty"`
	StdoutTail    string                    `json:"stdout_tail,omitempty"`
	StderrTail    string                    `json:"stderr_tail,omitempty"`
	Artifacts     []harnessEvidenceArtifact `json:"artifacts,omitempty"`
	ReproCommand  string                    `json:"repro_command,omitempty"`
}

type harnessEvidenceArtifact struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version,omitempty"`
}

type harnessArtifactContext struct {
	Root    string
	Enabled bool
	RunID   string
}

func newHarnessArtifactContext(root string, enabled bool) harnessArtifactContext {
	return harnessArtifactContext{
		Root:    root,
		Enabled: enabled,
		RunID:   time.Now().UTC().Format("20060102T150405.000000000Z"),
	}
}

func (ctx harnessArtifactContext) Write(name, filename, schemaVersion string, data []byte) (harnessEvidenceArtifact, error) {
	if !ctx.Enabled || strings.TrimSpace(ctx.Root) == "" || len(data) == 0 {
		return harnessEvidenceArtifact{}, nil
	}
	cleanName := sanitizeHarnessArtifactName(name)
	cleanFile := sanitizeHarnessArtifactFilename(filename)
	rel := filepath.ToSlash(filepath.Join(".onlava", "harness", "artifacts", ctx.RunID, cleanFile))
	abs := filepath.Join(ctx.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return harnessEvidenceArtifact{}, err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return harnessEvidenceArtifact{}, err
	}
	return harnessEvidenceArtifact{Name: cleanName, Path: rel, SchemaVersion: schemaVersion}, nil
}

func optionalHarnessArtifactContext(items []harnessArtifactContext) harnessArtifactContext {
	if len(items) == 0 {
		return harnessArtifactContext{}
	}
	return items[0]
}

func writeHarnessOutputEvidenceArtifacts(ctx harnessArtifactContext, stepName, stdoutFilename, schemaVersion string, stdout, stderr []byte) ([]harnessEvidenceArtifact, []checkDiagnostic) {
	if !ctx.Enabled {
		return nil, nil
	}
	var artifacts []harnessEvidenceArtifact
	var diagnostics []checkDiagnostic
	if len(stdout) > 0 {
		artifact, err := ctx.Write(stepName+" stdout", stdoutFilename, schemaVersion, stdout)
		if err != nil {
			diagnostics = append(diagnostics, formatArtifactWriteError(stepName, err))
		} else if artifact.Path != "" {
			artifacts = append(artifacts, artifact)
		}
	}
	if len(stderr) > 0 {
		name := strings.TrimSuffix(stdoutFilename, filepath.Ext(stdoutFilename)) + ".stderr.log"
		artifact, err := ctx.Write(stepName+" stderr", name, "", stderr)
		if err != nil {
			diagnostics = append(diagnostics, formatArtifactWriteError(stepName, err))
		} else if artifact.Path != "" {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, diagnostics
}

func sanitizeHarnessArtifactName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "artifact"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ':' || r == '/' || r == ' ':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "artifact"
	}
	return out
}

func sanitizeHarnessArtifactFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	if value == "." || value == string(filepath.Separator) || value == "" {
		return "artifact.log"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "artifact.log"
	}
	return out
}

func ensureHarnessStepEvidence(step *harnessStep, defaultCWD string) {
	if step == nil {
		return
	}
	if step.Evidence == nil {
		started := time.Now().UTC().Add(-time.Duration(step.DurationMS) * time.Millisecond)
		step.Evidence = &harnessEvidence{
			SchemaVersion: harnessArtifactEvidenceSchema,
			Command:       append([]string{}, step.Command...),
			CWD:           firstNonEmpty(harnessStepEvidenceCWD(*step), defaultCWD),
			StartedAt:     started.Format(time.RFC3339Nano),
		}
	}
	step.Evidence.SchemaVersion = firstNonEmpty(step.Evidence.SchemaVersion, harnessArtifactEvidenceSchema)
	if len(step.Evidence.Command) == 0 {
		step.Evidence.Command = append([]string{}, step.Command...)
	}
	if step.Evidence.CWD == "" {
		step.Evidence.CWD = firstNonEmpty(harnessStepEvidenceCWD(*step), defaultCWD)
	}
	if step.Evidence.StartedAt == "" {
		started := time.Now().UTC().Add(-time.Duration(step.DurationMS) * time.Millisecond)
		step.Evidence.StartedAt = started.Format(time.RFC3339Nano)
	}
	step.Evidence.DurationMS = step.DurationMS
	if step.Evidence.ExitCode == nil {
		code := 0
		if !step.OK {
			code = 1
		}
		step.Evidence.ExitCode = &code
	}
	if step.Evidence.StdoutTail == "" && step.OutputTail != "" {
		step.Evidence.StdoutTail = step.OutputTail
	}
	if step.Evidence.ReproCommand == "" && len(step.Evidence.Command) > 0 {
		step.Evidence.ReproCommand = reproCommand(step.Evidence.Command, step.Evidence.CWD)
	}
}

func harnessStepEvidenceCWD(step harnessStep) string {
	if step.Summary == nil {
		return ""
	}
	if cwd, ok := step.Summary["cwd"].(string); ok {
		return cwd
	}
	return ""
}

func annotateHarnessEvidence(steps []harnessStep, defaultCWD string) {
	for i := range steps {
		ensureHarnessStepEvidence(&steps[i], defaultCWD)
	}
}

func newHarnessEvidence(command []string, cwd string, started time.Time) harnessEvidence {
	return harnessEvidence{
		SchemaVersion: harnessArtifactEvidenceSchema,
		Command:       append([]string{}, command...),
		CWD:           cwd,
		StartedAt:     started.UTC().Format(time.RFC3339Nano),
		ReproCommand:  reproCommand(command, cwd),
	}
}

func finalizeHarnessEvidence(evidence *harnessEvidence, duration time.Duration, ok bool, stdoutTail, stderrTail string, exitCode *int, artifacts []harnessEvidenceArtifact) {
	if evidence == nil {
		return
	}
	evidence.SchemaVersion = firstNonEmpty(evidence.SchemaVersion, harnessArtifactEvidenceSchema)
	evidence.DurationMS = duration.Milliseconds()
	if exitCode == nil {
		code := 0
		if !ok {
			code = 1
		}
		exitCode = &code
	}
	evidence.ExitCode = exitCode
	evidence.StdoutTail = tailString(strings.TrimSpace(stdoutTail), 8192)
	evidence.StderrTail = tailString(strings.TrimSpace(stderrTail), 8192)
	evidence.Artifacts = append(evidence.Artifacts, artifacts...)
	if evidence.ReproCommand == "" && len(evidence.Command) > 0 {
		evidence.ReproCommand = reproCommand(evidence.Command, evidence.CWD)
	}
}

func reproCommand(command []string, cwd string) string {
	if len(command) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if strings.TrimSpace(cwd) != "" {
		buf.WriteString("cd ")
		buf.WriteString(shellQuote(cwd))
		buf.WriteString(" && ")
	}
	for i, arg := range command {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(shellQuote(arg))
	}
	return buf.String()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./:=,+@", r))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func exitCodeFromError(err error) *int {
	if err == nil {
		code := 0
		return &code
	}
	type exitCoder interface {
		ExitCode() int
	}
	if coder, ok := err.(exitCoder); ok {
		code := coder.ExitCode()
		return &code
	}
	text := err.Error()
	if idx := strings.LastIndex(text, "exit status "); idx >= 0 {
		raw := strings.TrimSpace(text[idx+len("exit status "):])
		fields := strings.Fields(raw)
		if len(fields) > 0 {
			if parsed, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
				return &parsed
			}
		}
	}
	code := 1
	return &code
}

func intPtr(value int) *int {
	return &value
}

func evidenceArtifactsFromHarnessArtifacts(items []harnessArtifact) []harnessEvidenceArtifact {
	out := make([]harnessEvidenceArtifact, 0, len(items))
	for _, item := range items {
		out = append(out, harnessEvidenceArtifact{
			Name:          item.Name,
			Path:          item.Path,
			SchemaVersion: item.SchemaVersion,
		})
	}
	return out
}

func formatArtifactWriteError(name string, err error) checkDiagnostic {
	return checkDiagnostic{
		Stage:           name,
		Severity:        "warning",
		Message:         fmt.Sprintf("failed to write harness evidence artifact: %v", err),
		SuggestedAction: "Check `.onlava/harness/artifacts` permissions and available disk space.",
	}
}
