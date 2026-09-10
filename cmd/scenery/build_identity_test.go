package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGenerationVerificationRequiresDevelopmentApplication(t *testing.T) {
	for _, args := range [][]string{
		{"--verify-generation"},
		{"--verify-generation", "--development", "--desktop"},
		{"--verify-generation", "--development", "--lib", "geometry"},
	} {
		if err := buildCommand(io.Discard, args); err == nil || !strings.Contains(err.Error(), "requires a --development application build") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestInspectMissingGenerationIsPreconditionNotInternal(t *testing.T) {
	isolateCommandCacheRoot(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"app","id":"app-id","envs":{"local":{"default":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"build", "--verify-generation", "--app-root", root, "-o", "json"}
	err := runSceneryInspect(args, io.Discard)
	if err == nil {
		t.Fatal("accepted missing generation")
	}
	var output strings.Builder
	_ = renderMachineError(&output, append([]string{"inspect"}, args...), err)
	if !strings.Contains(output.String(), `"code":"SCN8003"`) || strings.Contains(output.String(), "report_token") {
		t.Fatalf("unexpected diagnostic: %s", output.String())
	}
}

func TestInspectGenerationVerificationIsBuildOnly(t *testing.T) {
	if _, err := parseInspectArgs([]string{"build", "--verify-generation", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseInspectArgs([]string{"app", "--verify-generation", "-o", "json"}); err == nil {
		t.Fatal("accepted build verification for another subject")
	}
}
