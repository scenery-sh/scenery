package nativebuilddriver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEligibilitySupportsOnlyFrozenGraphBodyEdits(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "service.go")
	modFile := filepath.Join(root, "go.mod")
	base := Capture{
		GoVersion: "go fixture", GoToolDigest: "sha256:go", BuildFlags: []string{"-tags=fixture"}, Environment: map[string]string{"GOOS": "darwin"},
		Packages: map[string]Package{
			"example/app": {ImportPath: "example/app", Name: "app", Dir: root, Imports: []string{"fmt"}, ImportMap: map[string]string{"fmt": "fmt"}, GoFiles: []string{"service.go"}, Module: &struct {
				GoMod   string
				Replace *struct{ GoMod string }
			}{GoMod: modFile}},
		},
		Files: map[string]string{goFile: "sha256:a", modFile: "sha256:m"}, Syntax: map[string]string{goFile: "sha256:syntax"},
	}
	recipe := &Recipe{Bootstrap: base, ToolDigests: map[string]string{}}
	body := cloneCapture(base)
	body.Files[goFile] = "sha256:b"
	changed, reason := recipe.eligible(body)
	if reason != "" || !reflect.DeepEqual(changed, []string{"example/app"}) {
		t.Fatalf("body edit: changed=%v reason=%q", changed, reason)
	}

	tests := []struct {
		name, want string
		change     func(*Capture)
	}{
		{"import", "package_selection_changed", func(value *Capture) {
			pkg := value.Packages["example/app"]
			pkg.Imports = []string{"os"}
			value.Packages["example/app"] = pkg
		}},
		{"directive", "unsupported_input_changed", func(value *Capture) { value.Files[goFile] = "sha256:b"; value.Syntax[goFile] = "sha256:new-syntax" }},
		{"file-added", "input_added", func(value *Capture) { value.Files[filepath.Join(root, "new.go")] = "sha256:new" }},
		{"module", "unsupported_input_changed", func(value *Capture) { value.Files[modFile] = "sha256:new-mod" }},
		{"flags", "build_configuration_changed", func(value *Capture) { value.BuildFlags = []string{"-tags=other"} }},
		{"environment", "build_configuration_changed", func(value *Capture) { value.Environment["GOOS"] = "linux" }},
		{"toolchain", "toolchain_changed", func(value *Capture) { value.GoVersion = "go other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneCapture(base)
			test.change(&value)
			if _, got := recipe.eligible(value); got != test.want {
				t.Fatalf("reason=%q want %q", got, test.want)
			}
		})
	}
}

func TestRebuildOrderIncludesTransitiveConsumers(t *testing.T) {
	recipe := &Recipe{Bootstrap: Capture{Packages: map[string]Package{
		"leaf": {Imports: nil}, "adapter": {Imports: []string{"leaf"}}, "main": {Imports: []string{"adapter"}}, "other": {Imports: nil},
	}}}
	got, err := recipe.rebuildOrder([]string{"leaf"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"leaf", "adapter", "main"}) {
		t.Fatalf("order=%v", got)
	}
}

func TestOwnerRejectsForeignAndStaleRequestsBeforeBuild(t *testing.T) {
	owner := &Owner{Recipe: &Recipe{}, Session: "session", Workspace: "/owned"}
	foreign, err := owner.build(context.Background(), OwnerRequest{Protocol: ProtocolVersion, Session: "other", Workspace: "/owned", Sequence: 1, Build: BuildRequest{Workspace: "/owned"}})
	if err != nil || foreign.Reason != "foreign_owner_identity" {
		t.Fatalf("foreign=%+v err=%v", foreign, err)
	}
	stale, err := owner.build(context.Background(), OwnerRequest{Protocol: ProtocolVersion, Session: "session", Workspace: "/owned", Sequence: 0, Build: BuildRequest{Workspace: "/owned"}})
	if err != nil || stale.Reason != "stale_generation" {
		t.Fatalf("stale=%+v err=%v", stale, err)
	}
}

func TestCopyRegularRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyRegular(link, filepath.Join(root, "copy")); err == nil {
		t.Fatal("symlink capture unexpectedly succeeded")
	}
}

func TestSplitQuotedPreservesSpacesAndRepeatedFlags(t *testing.T) {
	got, err := splitQuoted(`-X=one=a -X='two=b c' -s`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-X=one=a", "-X=two=b c", "-s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func cloneCapture(value Capture) Capture {
	data, _ := json.Marshal(value)
	var result Capture
	_ = json.Unmarshal(data, &result)
	return result
}
