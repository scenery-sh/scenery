package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type inspectHarnessOptions struct {
	Topic    string
	Name     string
	Severity string
	Top      int
}

type inspectHarnessArtifactResponse struct {
	SchemaVersion string          `json:"schema_version"`
	GeneratedAt   string          `json:"generated_at"`
	Scope         string          `json:"scope"`
	Root          string          `json:"root"`
	Artifact      harnessArtifact `json:"artifact"`
	Payload       any             `json:"payload,omitempty"`
}

type inspectHarnessDiagnosticsResponse struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Scope         string            `json:"scope"`
	Root          string            `json:"root"`
	Severity      string            `json:"severity,omitempty"`
	Diagnostics   []checkDiagnostic `json:"diagnostics"`
	OmittedCount  int               `json:"omitted_count,omitempty"`
	Artifacts     []harnessArtifact `json:"artifacts,omitempty"`
}

type inspectHarnessTimingResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	GeneratedAt   string                   `json:"generated_at"`
	Scope         string                   `json:"scope"`
	Root          string                   `json:"root"`
	Top           int                      `json:"top"`
	TotalSeconds  float64                  `json:"total_seconds"`
	Budgets       harnessTestTimingBudgets `json:"budgets"`
	SlowTests     []harnessTestTiming      `json:"slow_tests,omitempty"`
	SlowPackages  []harnessPackageTiming   `json:"slow_packages,omitempty"`
	Diagnostics   []checkDiagnostic        `json:"diagnostics,omitempty"`
	Artifact      harnessArtifact          `json:"artifact"`
}

func buildInspectHarnessFocusedResponse(opts inspectOptions) (any, error) {
	root, scope, _, _, err := resolveInspectHarnessRoot(opts)
	if err != nil {
		return nil, err
	}
	switch opts.Harness.Topic {
	case "artifact":
		return buildInspectHarnessArtifactResponse(root, scope, opts.Harness.Name)
	case "diagnostics":
		return buildInspectHarnessDiagnosticsResponse(root, scope, opts.Harness.Severity)
	case "timing":
		top := opts.Harness.Top
		if top <= 0 {
			top = 10
		}
		return buildInspectHarnessTimingResponse(root, scope, top)
	default:
		return nil, fmt.Errorf("unknown inspect harness topic %q", opts.Harness.Topic)
	}
}

func buildInspectHarnessArtifactResponse(root, scope, name string) (inspectHarnessArtifactResponse, error) {
	artifact, path, err := resolveHarnessArtifactByName(root, name)
	if err != nil {
		return inspectHarnessArtifactResponse{}, err
	}
	payload, err := readBoundedHarnessArtifactPayload(name, path)
	if err != nil {
		return inspectHarnessArtifactResponse{}, err
	}
	return inspectHarnessArtifactResponse{SchemaVersion: inspectHarnessSchema, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope, Root: root, Artifact: artifact, Payload: payload}, nil
}

func readBoundedHarnessArtifactPayload(name, path string) (any, error) {
	switch name {
	case "test-timing":
		report, err := readHarnessJSON[harnessTestTimingReport](path)
		if err != nil {
			return nil, err
		}
		packages := append([]harnessPackageTiming{}, report.Packages...)
		sort.Slice(packages, func(i, j int) bool {
			if packages[i].Seconds == packages[j].Seconds {
				return packages[i].Package < packages[j].Package
			}
			return packages[i].Seconds > packages[j].Seconds
		})
		return map[string]any{
			"schema_version":           report.SchemaVersion,
			"total_seconds":            report.TotalSeconds,
			"confirmation_seconds":     report.ConfirmationSeconds,
			"budgets":                  report.Budgets,
			"package_count":            len(report.Packages),
			"observed_slow_test_count": len(report.ObservedSlowTests),
			"slow_test_count":          len(report.SlowTests),
			"top_slow_tests":           capTests(report.SlowTests, 10),
			"top_slow_packages":        capPackages(packages, 10),
			"diagnostics":              capDiagnostics(report.Diagnostics, 50, ""),
		}, nil
	case "drift":
		report, err := readHarnessJSON[harnessDriftReport](path)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"schema_version":    report.SchemaVersion,
			"cli_command_count": len(report.CLI.Commands),
			"env_var_count":     len(report.Env.Variables),
			"embed_count":       len(report.Embeds.Embeds),
			"diagnostic_count":  len(report.Diagnostics),
			"forbidden_tracked": len(report.Artifacts.ForbiddenTracked),
			"diagnostics":       capDiagnostics(report.Diagnostics, 50, ""),
		}, nil
	case "self-harness":
		resp, err := readHarnessJSON[harnessSelfResponse](path)
		if err != nil {
			return nil, err
		}
		return buildHarnessSelfSummary(resp), nil
	default:
		return readHarnessJSONAny(path)
	}
}

func buildInspectHarnessDiagnosticsResponse(root, scope, severity string) (inspectHarnessDiagnosticsResponse, error) {
	resp := inspectHarnessDiagnosticsResponse{SchemaVersion: inspectHarnessSchema, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope, Root: root, Severity: severity}
	self, ok, err := readLatestSelfHarness(root)
	if err != nil {
		return resp, err
	}
	if !ok {
		return resp, nil
	}
	resp.Artifacts = self.Artifacts
	for _, step := range self.Steps {
		for _, diag := range step.Diagnostics {
			if severity != "" && diag.Severity != severity {
				continue
			}
			diag.File = normalizeLikelyPath(diag.File)
			resp.Diagnostics = append(resp.Diagnostics, diag)
		}
	}
	if len(resp.Diagnostics) > 50 {
		resp.OmittedCount = len(resp.Diagnostics) - 50
		resp.Diagnostics = resp.Diagnostics[:50]
	}
	return resp, nil
}

func buildInspectHarnessTimingResponse(root, scope string, top int) (inspectHarnessTimingResponse, error) {
	artifact, path, err := resolveHarnessArtifactByName(root, "test-timing")
	if err != nil {
		return inspectHarnessTimingResponse{}, err
	}
	report, err := readHarnessJSON[harnessTestTimingReport](path)
	if err != nil {
		return inspectHarnessTimingResponse{}, err
	}
	packages := append([]harnessPackageTiming{}, report.Packages...)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Seconds == packages[j].Seconds {
			return packages[i].Package < packages[j].Package
		}
		return packages[i].Seconds > packages[j].Seconds
	})
	return inspectHarnessTimingResponse{SchemaVersion: inspectHarnessSchema, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope, Root: root, Top: top, TotalSeconds: report.TotalSeconds, Budgets: report.Budgets, SlowTests: capTests(report.SlowTests, top), SlowPackages: capPackages(packages, top), Diagnostics: capDiagnostics(report.Diagnostics, 50, ""), Artifact: artifact}, nil
}

func resolveHarnessArtifactByName(root, name string) (harnessArtifact, string, error) {
	for _, artifact := range knownHarnessArtifacts() {
		if artifact.Name != name {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(artifact.Path))
		if _, err := os.Stat(path); err != nil {
			return artifact, "", err
		}
		artifact.Exists = true
		return artifact, path, nil
	}
	return harnessArtifact{}, "", fmt.Errorf("unknown harness artifact %q", name)
}

func knownHarnessArtifacts() []harnessArtifact {
	return []harnessArtifact{
		{Name: "self-harness", Path: ".scenery/harness/self-latest.json", SchemaVersion: "scenery.harness.self.v1"},
		{Name: "self-summary", Path: ".scenery/harness/self-summary-latest.json", SchemaVersion: harnessSelfSummarySchema},
		{Name: "toolchain", Path: ".scenery/harness/toolchain-latest.json", SchemaVersion: harnessToolchainSchema},
		{Name: "changed-area", Path: ".scenery/harness/changed-area-latest.json", SchemaVersion: harnessChangedAreaSchema},
		{Name: "drift", Path: ".scenery/harness/drift-latest.json", SchemaVersion: harnessDriftSchema},
		{Name: "test-timing", Path: ".scenery/harness/test-timing-latest.json", SchemaVersion: harnessTestTimingSchema},
		{Name: "fixture-matrix", Path: ".scenery/harness/fixture-matrix-latest.json", SchemaVersion: harnessFixtureMatrixSchema},
		{Name: "schema-validation", Path: ".scenery/harness/schema-validation-latest.json", SchemaVersion: harnessSchemaValidationSchema},
		{Name: "agent-context", Path: ".scenery/harness/agent-context.json", SchemaVersion: harnessAgentContextSchema},
	}
}

func readLatestSelfHarness(root string) (harnessSelfResponse, bool, error) {
	path := filepath.Join(root, ".scenery", "harness", "self-latest.json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return harnessSelfResponse{}, false, nil
		}
		return harnessSelfResponse{}, false, err
	}
	resp, err := readHarnessJSON[harnessSelfResponse](path)
	return resp, err == nil, err
}

func readHarnessJSONAny(path string) (any, error) {
	var payload any
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
