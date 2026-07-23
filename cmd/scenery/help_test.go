package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHelpCommandJSONScopesBuildDescriptor(t *testing.T) {
	output := captureStdout(t, func() error {
		return helpCommand([]string{"build", "-o", "json"})
	})
	if len(output) >= 8*1024 {
		t.Fatalf("scoped help is %d bytes, want less than 8 KiB", len(output))
	}

	var manifest helpManifest
	if err := decodeCLIJSON([]byte(output), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.cliPayloadIdentity != newCLIPayloadIdentity(helpManifestKind) {
		t.Fatalf("identity = %#v", manifest.cliPayloadIdentity)
	}
	if len(manifest.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(manifest.Commands))
	}
	build := manifest.Commands[0]
	if build.Command != "build" || build.Group != "Build and checks" || build.Stability != "stable" || !build.JSON {
		t.Fatalf("build descriptor identity = %#v", build)
	}
	wantUsage := []string{
		"scenery build [--target <go-target>] [--app-root <path>] [--output <binary>] [-o human|json]",
		"scenery build --lib <name|address|artifact> [--version <vN.N.N>] [--platform all|host|darwin/arm64|linux/amd64|<csv>] [--app-root <path>] [--output <directory>] [-o human|json]",
		"scenery build --desktop [--env <name>] [--app-root <path>] [-o human|json]",
	}
	if !reflect.DeepEqual(build.Usage, wantUsage) {
		t.Fatalf("usage = %#v", build.Usage)
	}
	for _, flag := range []string{"--target <go-target>", "--lib <name|address|artifact>", "--desktop", "--env <name>", "-o human|json"} {
		if !containsHelpString(build.Flags, flag) {
			t.Errorf("flags missing %q: %#v", flag, build.Flags)
		}
	}
	for _, relationship := range []helpRequiredCombination{
		{When: "--version", Requires: []string{"--lib"}},
		{When: "--platform", Requires: []string{"--lib"}},
		{When: "--env", Requires: []string{"--desktop"}},
		{When: "--lib", ConflictsWith: []string{"--target", "--desktop"}},
		{When: "--desktop", ConflictsWith: []string{"--target", "--lib", "--version", "--platform", "--output"}},
	} {
		if !containsHelpRelationship(build.RequiredCombinations, relationship) {
			t.Errorf("required_combinations missing %#v: %#v", relationship, build.RequiredCombinations)
		}
	}
	if build.SideEffectClass != "local_artifacts" || build.AppRootRequirement != "required" {
		t.Fatalf("operational classification = %q %q", build.SideEffectClass, build.AppRootRequirement)
	}
	wantSchemas := map[string]string{
		"application": "scenery.build.result",
		"library":     "scenery.library.build.result",
		"desktop":     "scenery.build.desktop",
	}
	if len(build.OutputSchemas) != len(wantSchemas) {
		t.Fatalf("output schemas = %#v", build.OutputSchemas)
	}
	for _, schema := range build.OutputSchemas {
		if wantSchemas[schema.Mode] != schema.Kind {
			t.Errorf("unexpected output schema = %#v", schema)
		}
		if schema.SchemaRevision != newCLIPayloadIdentity(schema.Kind).SchemaRevision {
			t.Errorf("%s schema revision = %q", schema.Kind, schema.SchemaRevision)
		}
	}
	wantExits := map[string]int{"success": 0, "invalid_request": 2, "failed_precondition": 3, "internal": 10}
	if len(build.CommonExitCategories) != len(wantExits) {
		t.Fatalf("common exit categories = %#v", build.CommonExitCategories)
	}
	for _, exit := range build.CommonExitCategories {
		if wantExits[exit.Category] != exit.ExitCode {
			t.Errorf("unexpected exit category = %#v", exit)
		}
	}
	for _, command := range []string{
		"scenery inspect build -o json",
		"scenery inspect paths -o json",
		"scenery check -o json",
		"scenery generate --check -o json",
	} {
		if !containsHelpString(build.RelatedCommands, command) {
			t.Errorf("related commands missing %q: %#v", command, build.RelatedCommands)
		}
	}
	if len(build.Notes) != 0 {
		t.Fatalf("build machine help contains prose notes: %#v", build.Notes)
	}

	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.help.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, manifest); len(diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %v", diagnostics)
	}
}

func TestHelpJSONKeepsFullManifestAndSelectsNestedCommands(t *testing.T) {
	output := captureStdout(t, func() error {
		return helpCommand([]string{"-o=json"})
	})
	var manifest helpManifest
	if err := decodeCLIJSON([]byte(output), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Commands) != len(helpCommands) {
		t.Fatalf("commands = %d, want %d", len(manifest.Commands), len(helpCommands))
	}

	output = captureStdout(t, func() error {
		return helpCommand([]string{"inspect", "harness", "timing", "-o", "json"})
	})
	if err := decodeCLIJSON([]byte(output), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Commands) != 1 || manifest.Commands[0].Command != "inspect harness" {
		t.Fatalf("nested scoped commands = %#v", manifest.Commands)
	}
	if _, ok := findHelpCommand([]string{"build", "unknown"}); ok {
		t.Fatal("build accepted an unknown help subtopic")
	}
}

func TestHelpBuildHumanOutputRemainsHuman(t *testing.T) {
	output := captureStdout(t, func() error {
		return helpCommand([]string{"build"})
	})
	if !strings.Contains(output, "Usage:") || !strings.Contains(output, "scenery build --desktop") {
		t.Fatalf("human help = %q", output)
	}
	if strings.Contains(output, `"kind":"scenery.cli"`) {
		t.Fatalf("human help rendered JSON: %q", output)
	}
}

func TestUnknownScopedHelpIsAnInvalidRequestEnvelope(t *testing.T) {
	err := helpCommand([]string{"build", "unknown", "-o", "json"})
	if err == nil || cliExitCode(err) != 2 {
		t.Fatalf("help error = %v, exit = %d", err, cliExitCode(err))
	}
	var output strings.Builder
	rendered := renderMachineError(&output, []string{"help", "build", "unknown", "-o", "json"}, err)
	if rendered == nil || cliExitCode(rendered) != 2 {
		t.Fatalf("rendered error = %v, exit = %d", rendered, cliExitCode(rendered))
	}
	var envelope struct {
		OK          bool `json:"ok"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if decodeErr := json.Unmarshal([]byte(output.String()), &envelope); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8001" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestBuildRequiredFlagCombinationsAreEnforced(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"--version", "v1.2.3"}, want: "--version requires --lib"},
		{args: []string{"--platform", "host"}, want: "--platform requires --lib"},
		{args: []string{"--env", "production"}, want: "--env is only supported with --desktop"},
		{args: []string{"--lib", "geometry", "--target", "production"}, want: "--lib cannot be combined with --target"},
		{args: []string{"--desktop", "--output", "dist"}, want: "--desktop cannot be combined with --output"},
		{args: []string{"--lib="}, want: "--lib requires a non-empty selector"},
	}
	for _, test := range tests {
		err := buildCommand(test.args)
		if err == nil || !strings.HasPrefix(err.Error(), "invalid_request:") || !strings.Contains(err.Error(), test.want) {
			t.Errorf("buildCommand(%#v) error = %v, want invalid_request containing %q", test.args, err, test.want)
		}
	}
}

func containsHelpString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsHelpRelationship(values []helpRequiredCombination, want helpRequiredCombination) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, want) {
			return true
		}
	}
	return false
}
