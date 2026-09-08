package harnessevidence

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/machine"
	"strconv"
	"strings"
	"time"
)

const Kind = "scenery.harness.artifact"

type Context struct {
	Root    string
	Enabled bool
	RunID   string
}

func NewContext(root string, enabled bool) Context {
	return Context{
		Root:    root,
		Enabled: enabled,
		RunID:   time.Now().UTC().Format("20060102T150405.000000000Z"),
	}
}

func (ctx Context) Write(name, filename, schemaVersion string, data []byte) (harnessreport.EvidenceArtifact, error) {
	if !ctx.Enabled || strings.TrimSpace(ctx.Root) == "" || len(data) == 0 {
		return harnessreport.EvidenceArtifact{}, nil
	}
	cleanName := harnessreport.ArtifactName(name)
	cleanFile := ArtifactFilename(filename)
	rel := filepath.ToSlash(filepath.Join(".scenery", "harness", "artifacts", ctx.RunID, cleanFile))
	abs := filepath.Join(ctx.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return harnessreport.EvidenceArtifact{}, err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return harnessreport.EvidenceArtifact{}, err
	}
	identity := harnessArtifactPayloadIdentity(schemaVersion)
	return harnessreport.EvidenceArtifact{Name: cleanName, Path: rel, Kind: identity.Kind, SchemaRevision: identity.SchemaRevision}, nil
}

func harnessArtifactPayloadIdentity(kind string) machine.PayloadIdentity {
	if _, ok := machine.PayloadSchemaRevision(kind); !ok {
		return machine.PayloadIdentity{}
	}
	return machine.NewPayloadIdentity(kind)
}

func NewArtifact(name, path, kind string, exists bool) harnessreport.Artifact {
	identity := harnessArtifactPayloadIdentity(kind)
	return harnessreport.Artifact{Name: name, Path: path, Kind: identity.Kind, SchemaRevision: identity.SchemaRevision, Exists: exists}
}

func NewArtifactReference(name, path, kind string) harnessreport.EvidenceArtifact {
	identity := harnessArtifactPayloadIdentity(kind)
	return harnessreport.EvidenceArtifact{Name: name, Path: path, Kind: identity.Kind, SchemaRevision: identity.SchemaRevision}
}

func OptionalContext(items []Context) Context {
	if len(items) == 0 {
		return Context{}
	}
	return items[0]
}

func WriteOutputArtifacts(ctx Context, stepName, stdoutFilename, schemaVersion string, stdout, stderr []byte) ([]harnessreport.EvidenceArtifact, []harnessreport.Diagnostic) {
	if !ctx.Enabled {
		return nil, nil
	}
	var artifacts []harnessreport.EvidenceArtifact
	var diagnostics []harnessreport.Diagnostic
	if len(stdout) > 0 {
		artifact, err := ctx.Write(stepName+" stdout", stdoutFilename, schemaVersion, stdout)
		if err != nil {
			diagnostics = append(diagnostics, WriteDiagnostic(stepName, err))
		} else if artifact.Path != "" {
			artifacts = append(artifacts, artifact)
		}
	}
	if len(stderr) > 0 {
		name := strings.TrimSuffix(stdoutFilename, filepath.Ext(stdoutFilename)) + ".stderr.log"
		artifact, err := ctx.Write(stepName+" stderr", name, "", stderr)
		if err != nil {
			diagnostics = append(diagnostics, WriteDiagnostic(stepName, err))
		} else if artifact.Path != "" {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, diagnostics
}

func ArtifactFilename(value string) string {
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

func ensureHarnessStepEvidence(step *harnessreport.Step, defaultCWD string) {
	if step == nil {
		return
	}
	cwd := StepCWD(*step)
	if strings.TrimSpace(cwd) == "" {
		cwd = defaultCWD
	}
	if step.Evidence == nil {
		started := time.Now().UTC().Add(-time.Duration(step.DurationMS) * time.Millisecond)
		step.Evidence = &harnessreport.Evidence{
			PayloadIdentity: machine.NewPayloadIdentity(Kind),
			Command:         append([]string{}, step.Command...),
			CWD:             cwd,
			StartedAt:       started.Format(time.RFC3339Nano),
		}
	}
	if step.Evidence.Kind == "" {
		step.Evidence.PayloadIdentity = machine.NewPayloadIdentity(Kind)
	}
	if len(step.Evidence.Command) == 0 {
		step.Evidence.Command = append([]string{}, step.Command...)
	}
	if step.Evidence.CWD == "" {
		step.Evidence.CWD = cwd
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
		step.Evidence.ReproCommand = ReproCommand(step.Evidence.Command, step.Evidence.CWD)
	}
}

func StepCWD(step harnessreport.Step) string {
	if step.Summary == nil {
		return ""
	}
	if cwd, ok := step.Summary["cwd"].(string); ok {
		return cwd
	}
	return ""
}

func Annotate(steps []harnessreport.Step, defaultCWD string) {
	for i := range steps {
		ensureHarnessStepEvidence(&steps[i], defaultCWD)
	}
}

func New(command []string, cwd string, started time.Time) harnessreport.Evidence {
	return harnessreport.Evidence{
		PayloadIdentity: machine.NewPayloadIdentity(Kind),
		Command:         append([]string{}, command...),
		CWD:             cwd,
		StartedAt:       started.UTC().Format(time.RFC3339Nano),
		ReproCommand:    ReproCommand(command, cwd),
	}
}

func Finalize(evidence *harnessreport.Evidence, duration time.Duration, ok bool, stdoutTail, stderrTail string, exitCode *int, artifacts []harnessreport.EvidenceArtifact) {
	if evidence == nil {
		return
	}
	if evidence.Kind == "" {
		evidence.PayloadIdentity = machine.NewPayloadIdentity(Kind)
	}
	evidence.DurationMS = duration.Milliseconds()
	if exitCode == nil {
		code := 0
		if !ok {
			code = 1
		}
		exitCode = &code
	}
	evidence.ExitCode = exitCode
	evidence.StdoutTail = harnessreport.TailString(strings.TrimSpace(stdoutTail), 8192)
	evidence.StderrTail = harnessreport.TailString(strings.TrimSpace(stderrTail), 8192)
	evidence.Artifacts = append(evidence.Artifacts, artifacts...)
	if evidence.ReproCommand == "" && len(evidence.Command) > 0 {
		evidence.ReproCommand = ReproCommand(evidence.Command, evidence.CWD)
	}
}

func ReproCommand(command []string, cwd string) string {
	if len(command) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if strings.TrimSpace(cwd) != "" {
		buf.WriteString("cd ")
		buf.WriteString(ShellQuote(cwd))
		buf.WriteString(" && ")
	}
	for i, arg := range command {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(ShellQuote(arg))
	}
	return buf.String()
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_-./:=,+@", r)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func ExitCode(err error) *int {
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

func ArtifactReferences(items []harnessreport.Artifact) []harnessreport.EvidenceArtifact {
	out := make([]harnessreport.EvidenceArtifact, 0, len(items))
	for _, item := range items {
		out = append(out, harnessreport.EvidenceArtifact{
			Name:           item.Name,
			Path:           item.Path,
			Kind:           item.Kind,
			SchemaRevision: item.SchemaRevision,
		})
	}
	return out
}

func WriteDiagnostic(name string, err error) harnessreport.Diagnostic {
	return harnessreport.Diagnostic{
		Stage:           name,
		Severity:        "warning",
		Message:         fmt.Sprintf("failed to write harness evidence artifact: %v", err),
		SuggestedAction: "Check `.scenery/harness/artifacts` permissions and available disk space.",
	}
}
