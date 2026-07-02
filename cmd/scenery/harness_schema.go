package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const harnessSchemaValidationSchema = "scenery.harness.schema_validation.v1"

type harnessSchemaValidationReport struct {
	SchemaVersion string                        `json:"schema_version"`
	Validated     []harnessSchemaValidationItem `json:"validated"`
	Diagnostics   []checkDiagnostic             `json:"diagnostics,omitempty"`
}

type harnessSchemaValidationItem struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func runHarnessSchemaValidationStep(repoRoot string, resp harnessSelfResponse) (harnessStep, *harnessSchemaValidationReport) {
	started := time.Now()
	report := buildHarnessSchemaValidationReport(repoRoot, resp)
	step := harnessStep{
		Name:       "schema validation",
		Command:    []string{"scenery", "harness", "self", "internal:schema-validation", repoRoot},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"validated": len(report.Validated),
			"errors":    countSchemaValidationErrors(report.Validated),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "schema validation failed"
	}
	return step, report
}

func buildHarnessSchemaValidationReport(repoRoot string, resp harnessSelfResponse) *harnessSchemaValidationReport {
	report := &harnessSchemaValidationReport{SchemaVersion: harnessSchemaValidationSchema}
	versionPayload := buildVersionResponse()
	inspectDocsPayload, inspectDocsErr := harnessInspectDocsPayload(repoRoot)
	if inspectDocsErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect docs JSON for schema validation: " + inspectDocsErr.Error(),
			SuggestedAction: "Run `scenery inspect docs --json` and fix the command before relying on schema validation.",
		})
	}
	environmentRegistryPayload, environmentRegistryErr := harnessJSONFilePayload(filepath.Join(repoRoot, "docs", "environment.registry.json"))
	if environmentRegistryErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "environment.registry.json")),
			Message:         "failed to load environment registry JSON for schema validation: " + environmentRegistryErr.Error(),
			SuggestedAction: "Fix docs/environment.registry.json so it can be validated.",
		})
	}
	var inspectHarnessPayload any
	if payload, inspectHarnessErr := buildInspectHarnessResponse(inspectOptions{Subject: "harness", RepoRoot: repoRoot}); inspectHarnessErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect harness JSON for schema validation: " + inspectHarnessErr.Error(),
			SuggestedAction: "Run `scenery inspect harness --json --repo-root <repo>` and fix the command before relying on schema validation.",
		})
	} else {
		inspectHarnessPayload = payload
	}
	artifactEvidencePayload := harnessEvidence{
		SchemaVersion: harnessArtifactEvidenceSchema,
		Command:       []string{"go", "test", "-json", "./..."},
		CWD:           repoRoot,
		StartedAt:     "2026-06-07T00:00:00Z",
		DurationMS:    1234,
		ExitCode:      intPtr(1),
		StdoutTail:    "{}",
		Artifacts: []harnessEvidenceArtifact{{
			Name:          "go-test-json",
			Path:          ".scenery/harness/artifacts/20260607T000000Z/go-test.jsonl",
			SchemaVersion: "go.test.jsonl",
		}},
		ReproCommand: "cd " + repoRoot + " && go test -json ./...",
	}
	var helpJSON bytes.Buffer
	helpPayload := map[string]any{}
	if err := writeHelpJSON(&helpJSON); err == nil {
		_ = json.Unmarshal(helpJSON.Bytes(), &helpPayload)
	}
	items := []struct {
		name      string
		schemaRel string
		payload   any
	}{
		{name: "environment.registry", schemaRel: "docs/schemas/scenery.environment.registry.v1.schema.json", payload: environmentRegistryPayload},
		{name: "help", schemaRel: "docs/schemas/scenery.help.v1.schema.json", payload: helpPayload},
		{name: "version", schemaRel: "docs/schemas/scenery.version.v1.schema.json", payload: versionPayload},
		{name: "doctor", schemaRel: "docs/schemas/scenery.doctor.result.v1.schema.json", payload: buildHarnessDoctorSchemaPayload(versionPayload)},
		{name: "inspect.docs", schemaRel: "docs/schemas/scenery.inspect.docs.v1.schema.json", payload: inspectDocsPayload},
		{name: "inspect.harness", schemaRel: "docs/schemas/scenery.inspect.harness.v1.schema.json", payload: inspectHarnessPayload},
		{name: "harness.artifact", schemaRel: "docs/schemas/scenery.harness.artifact.v1.schema.json", payload: artifactEvidencePayload},
		{name: "harness.self", schemaRel: "docs/schemas/scenery.harness.self.v1.schema.json", payload: resp},
		{name: "harness.self.summary", schemaRel: "docs/schemas/scenery.harness.self.summary.v1.schema.json", payload: buildHarnessSelfSummary(resp)},
		{name: "harness.toolchain", schemaRel: "docs/schemas/scenery.harness.toolchain.v1.schema.json", payload: resp.Toolchain},
		{name: "harness.changed_area", schemaRel: "docs/schemas/scenery.harness.changed_area.v1.schema.json", payload: resp.ChangedArea},
		{name: "harness.drift", schemaRel: "docs/schemas/scenery.harness.drift.v1.schema.json", payload: resp.Drift},
		{name: "harness.test_timing", schemaRel: "docs/schemas/scenery.harness.test_timing.v1.schema.json", payload: resp.TestTiming},
		{name: "harness.fixture_matrix", schemaRel: "docs/schemas/scenery.harness.fixture_matrix.v1.schema.json", payload: resp.FixtureMatrix},
		{name: "harness.schema_validation", schemaRel: "docs/schemas/scenery.harness.schema_validation.v1.schema.json", payload: report},
		{name: "agent_context", schemaRel: "docs/schemas/scenery.agent_context.v1.schema.json", payload: buildHarnessAgentContext(repoRoot, resp)},
	}
	for _, item := range items {
		if harnessNilPayload(item.payload) {
			continue
		}
		schemaPath := filepath.Join(repoRoot, filepath.FromSlash(item.schemaRel))
		errs := validateHarnessJSONSchemaFile(schemaPath, item.payload)
		validation := harnessSchemaValidationItem{
			Name:   item.name,
			Schema: item.schemaRel,
			OK:     len(errs) == 0,
		}
		if len(errs) > 0 {
			validation.Error = strings.Join(errs, "; ")
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "schema validation",
				Severity:        "error",
				File:            filepath.ToSlash(schemaPath),
				Message:         item.name + " does not conform to " + item.schemaRel + ": " + validation.Error,
				SuggestedAction: "Update the JSON producer or schema so the contract matches.",
			})
		}
		report.Validated = append(report.Validated, validation)
	}
	return report
}

func harnessJSONFilePayload(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func buildHarnessDoctorSchemaPayload(versionPayload versionResponse) doctorResponse {
	resp := doctorResponse{
		SchemaVersion: doctorSchemaVersion,
		OK:            true,
		Scenery:       versionPayload,
		App: &doctorAppInfo{
			Root:       "/tmp/scenery-doctor-fixture",
			ConfigPath: "/tmp/scenery-doctor-fixture/.scenery.json",
			Name:       "doctorfixture",
			ID:         "doctorfixture",
		},
		Environment: doctorEnvironment{
			GOOS:             "linux",
			GOARCH:           "amd64",
			NumCPU:           8,
			TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
			Paths: []doctorPathReport{{
				Kind:       "app_root",
				Path:       "/tmp/scenery-doctor-fixture",
				FreeBytes:  20 * 1024 * 1024 * 1024,
				TotalBytes: 40 * 1024 * 1024 * 1024,
			}},
		},
		Checks: []doctorCheck{
			{
				ID:       "os.runtime",
				Category: "host",
				Name:     "Operating system",
				Status:   doctorStatusOK,
				Severity: doctorSeverityInformational,
				Message:  "linux/amd64",
				Observed: map[string]any{"goos": "linux", "goarch": "amd64"},
			},
			{
				ID:       "tool.go",
				Category: "dependency",
				Name:     "Go toolchain",
				Status:   doctorStatusOK,
				Severity: doctorSeverityRequired,
				Message:  "go version go1.26.3 linux/amd64 at /usr/local/go/bin/go",
				Observed: map[string]any{"path": "/usr/local/go/bin/go", "version": "go version go1.26.3 linux/amd64"},
			},
		},
	}
	resp.Summary = summarizeDoctorChecks(resp.Checks)
	return resp
}

func harnessInspectDocsPayload(repoRoot string) (map[string]any, error) {
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", repoRoot, "--json"}, &out); err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func harnessNilPayload(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func countSchemaValidationErrors(items []harnessSchemaValidationItem) int {
	count := 0
	for _, item := range items {
		if !item.OK {
			count++
		}
	}
	return count
}

func validateHarnessJSONSchemaFile(schemaPath string, payload any) []string {
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return []string{err.Error()}
	}
	var schema any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return []string{"invalid schema JSON: " + err.Error()}
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return []string{"failed to marshal payload: " + err.Error()}
	}
	var value any
	if err := json.Unmarshal(payloadData, &value); err != nil {
		return []string{"failed to normalize payload JSON: " + err.Error()}
	}
	validator := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	return validator.validate(schema, value, "$")
}

type harnessSchemaValidator struct {
	root      any
	schemaDir string
}

func (v harnessSchemaValidator) validate(schema any, value any, path string) []string {
	node, ok := schema.(map[string]any)
	if !ok {
		return []string{path + ": schema node is not an object"}
	}
	if ref, _ := node["$ref"].(string); ref != "" {
		resolved, validator, err := v.resolveRef(ref)
		if err != nil {
			return []string{path + ": " + err.Error()}
		}
		return validator.validate(resolved, value, path)
	}
	if constValue, ok := node["const"]; ok && !reflect.DeepEqual(constValue, value) {
		return []string{fmt.Sprintf("%s: value %s does not equal const %s", path, compactJSON(value), compactJSON(constValue))}
	}
	if enumValues, ok := node["enum"].([]any); ok {
		matched := false
		for _, enumValue := range enumValues {
			if reflect.DeepEqual(enumValue, value) {
				matched = true
				break
			}
		}
		if !matched {
			return []string{fmt.Sprintf("%s: value %s is not in enum", path, compactJSON(value))}
		}
	}
	types := schemaTypes(node["type"])
	if len(types) > 0 && !jsonValueMatchesAnyType(value, types) {
		return []string{fmt.Sprintf("%s: value has type %s, want %s", path, jsonValueType(value), strings.Join(types, "|"))}
	}
	var diagnostics []string
	if jsonValueMatchesAnyType(value, []string{"object"}) {
		diagnostics = append(diagnostics, v.validateObject(node, value.(map[string]any), path)...)
	}
	if jsonValueMatchesAnyType(value, []string{"array"}) {
		diagnostics = append(diagnostics, v.validateArray(node, value.([]any), path)...)
	}
	diagnostics = append(diagnostics, validateNumericBounds(node, value, path)...)
	return diagnostics
}

func (v harnessSchemaValidator) validateObject(schema map[string]any, value map[string]any, path string) []string {
	var diagnostics []string
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			key, _ := raw.(string)
			if key == "" {
				continue
			}
			if _, ok := value[key]; !ok {
				diagnostics = append(diagnostics, path+"."+key+": required property missing")
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, propertySchema := range properties {
		propertyValue, ok := value[key]
		if !ok {
			continue
		}
		diagnostics = append(diagnostics, v.validate(propertySchema, propertyValue, path+"."+key)...)
	}
	if additional, ok := schema["additionalProperties"]; ok {
		switch additional := additional.(type) {
		case bool:
			if !additional {
				for key := range value {
					if _, ok := properties[key]; !ok {
						diagnostics = append(diagnostics, path+"."+key+": additional property is not allowed")
					}
				}
			}
		case map[string]any:
			for key, propertyValue := range value {
				if _, ok := properties[key]; ok {
					continue
				}
				diagnostics = append(diagnostics, v.validate(additional, propertyValue, path+"."+key)...)
			}
		}
	}
	sort.Strings(diagnostics)
	return diagnostics
}

func (v harnessSchemaValidator) validateArray(schema map[string]any, value []any, path string) []string {
	items, ok := schema["items"]
	if !ok {
		return nil
	}
	var diagnostics []string
	for i, item := range value {
		diagnostics = append(diagnostics, v.validate(items, item, fmt.Sprintf("%s[%d]", path, i))...)
	}
	return diagnostics
}

func (v harnessSchemaValidator) resolveRef(ref string) (any, harnessSchemaValidator, error) {
	if strings.HasPrefix(ref, "#") {
		return v.resolveLocalRef(ref)
	}
	schemaFile, fragment, _ := strings.Cut(ref, "#")
	if schemaFile == "" {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	if filepath.IsAbs(schemaFile) || strings.Contains(schemaFile, "://") {
		return nil, v, fmt.Errorf("unsupported external $ref %q", ref)
	}
	cleanFile := filepath.Clean(filepath.FromSlash(schemaFile))
	if cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) {
		return nil, v, fmt.Errorf("external $ref %q escapes schema directory", ref)
	}
	schemaPath := filepath.Join(v.schemaDir, cleanFile)
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, v, fmt.Errorf("failed to read external $ref %q: %w", ref, err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, v, fmt.Errorf("invalid external schema %q: %w", ref, err)
	}
	external := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	if fragment == "" {
		return schema, external, nil
	}
	return external.resolveRef("#" + fragment)
}

func (v harnessSchemaValidator) resolveLocalRef(ref string) (any, harnessSchemaValidator, error) {
	const prefix = "#/$defs/"
	if ref == "#" {
		return v.root, v, nil
	}
	if !strings.HasPrefix(ref, prefix) {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	root, ok := v.root.(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema root is not an object")
	}
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema has no $defs for %q", ref)
	}
	key := strings.TrimPrefix(ref, prefix)
	value, ok := defs[key]
	if !ok {
		return nil, v, fmt.Errorf("schema $defs missing %q", key)
	}
	return value, v, nil
}

func schemaTypes(raw any) []string {
	switch raw := raw.(type) {
	case string:
		return []string{raw}
	case []any:
		var out []string
		for _, value := range raw {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonValueMatchesAnyType(value any, types []string) bool {
	for _, typ := range types {
		switch typ {
		case "null":
			if value == nil {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "number":
			if _, ok := value.(float64); ok {
				return true
			}
		case "integer":
			if number, ok := value.(float64); ok && math.Trunc(number) == number {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		}
	}
	return false
}

func jsonValueType(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case float64:
		if math.Trunc(value) == value {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func validateNumericBounds(schema map[string]any, value any, path string) []string {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	var diagnostics []string
	if min, ok := schema["minimum"].(float64); ok && number < min {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is less than minimum %.3f", path, number, min))
	}
	if max, ok := schema["maximum"].(float64); ok && number > max {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is greater than maximum %.3f", path, number, max))
	}
	return diagnostics
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
