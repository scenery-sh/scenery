package nativebuilddriver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	recipe := &Recipe{Workspace: root, Bootstrap: base, ToolDigests: map[string]string{}}
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

func TestSourceSelectionIdentityIncludesFunctionDirectives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.go")
	plain := []byte("package app\n\nfunc Value() string { return \"a\" }\n")
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := sourceSelectionIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	withDirective := []byte("package app\n\n//go:noinline\nfunc Value() string { return \"a\" }\n")
	if err := os.WriteFile(path, withDirective, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := sourceSelectionIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("function directive did not change source selection identity")
	}
}

func TestSamePathResolvesDirectorySymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if !samePath(filepath.Join(real, "."), alias) {
		t.Fatalf("expected %q and %q to identify the same directory", real, alias)
	}
}

func TestRetainedSupportValidationDetectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "importcfg")
	if err := os.WriteFile(path, []byte("packagefile a=/tmp/a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]RetainedFile{path: {Digest: digest, Bytes: size, Stamp: fileStamp(info)}}
	if err := validateRetainedFiles(expected); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("packagefile b=/tmp/b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := validateRetainedFiles(expected); err == nil {
		t.Fatal("corrupt support artifact was accepted")
	}
}

func TestRetainedCaptureHashesTheFrozenDomainWithoutGoDiscovery(t *testing.T) {
	root := t.TempDir()
	service := filepath.Join(root, "service.go")
	module := filepath.Join(root, "go.mod")
	baselineSource := []byte("package app\n\nfunc Value() string { return \"a\" }\n")
	if err := os.WriteFile(service, baselineSource, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte("module example/app\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goTool, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	goDigest, _, err := FileDigest(goTool)
	if err != nil {
		t.Fatal(err)
	}
	serviceDigest, _, _ := FileDigest(service)
	moduleDigest, _, _ := FileDigest(module)
	syntax, err := sourceSelectionIdentity(service)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "service.go")
	if _, err := CopyRegular(service, snapshot); err != nil {
		t.Fatal(err)
	}
	directories := map[string]string{}
	if err := captureDirectory(directories, root); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	base := Capture{
		GoVersion: "go fixture", GoToolDigest: goDigest, BuildFlags: []string{"-tags=fixture"}, Environment: map[string]string{"GOOS": "darwin"}, RequestEnv: relevantRequestEnvironment(env),
		Packages: map[string]Package{"example/app": {ImportPath: "example/app", Name: "app", Dir: root, GoFiles: []string{"service.go"}}},
		Files:    map[string]string{service: serviceDigest, module: moduleDigest}, Syntax: map[string]string{service: syntax}, Directories: directories,
		SnapshotFiles: map[string]string{service: snapshot},
	}
	recipe := &Recipe{Workspace: root, Bootstrap: base, ToolDigests: map[string]string{}}
	if err := os.WriteFile(service, []byte("package app\n\nfunc Value() string { return \"b\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, []string{"-tags=fixture"})
	if err != nil {
		t.Fatal(err)
	}
	changed, reason := recipe.eligible(capture)
	if reason != "" || !reflect.DeepEqual(changed, []string{"example/app"}) {
		t.Fatalf("body edit: changed=%v reason=%q", changed, reason)
	}
	if capture.SnapshotFiles[service] == snapshot {
		t.Fatal("changed source reused the bootstrap snapshot")
	}

	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err = recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, []string{"-tags=fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := recipe.eligible(capture); reason != "package_selection_changed" {
		t.Fatalf("added source reason=%q", reason)
	}

	if err := os.Remove(filepath.Join(root, "added.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service, baselineSource, 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err = recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, []string{"-tags=fixture"})
	if err != nil {
		t.Fatal(err)
	}
	changed, reason = recipe.eligible(capture)
	if reason != "" || len(changed) != 0 {
		t.Fatalf("A/B/A restoration: changed=%v reason=%q", changed, reason)
	}
}

func TestRetainedCaptureFailsClosedForUnsupportedChanges(t *testing.T) {
	newFixture := func(t *testing.T) (*Recipe, string, string, string, []string) {
		t.Helper()
		root := t.TempDir()
		service := filepath.Join(root, "service.go")
		module := filepath.Join(root, "go.mod")
		if err := os.WriteFile(service, []byte("package app\n\nimport \"fmt\"\n\nfunc Value() string { return fmt.Sprint(\"a\") }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(module, []byte("module example/app\n\ngo 1.27\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		goTool, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		goDigest, _, _ := FileDigest(goTool)
		serviceDigest, _, _ := FileDigest(service)
		moduleDigest, _, _ := FileDigest(module)
		syntax, _ := sourceSelectionIdentity(service)
		directories := map[string]string{}
		if err := captureDirectory(directories, root); err != nil {
			t.Fatal(err)
		}
		env := os.Environ()
		base := Capture{
			GoVersion: "go fixture", GoToolDigest: goDigest, Environment: map[string]string{"GOOS": "darwin"}, RequestEnv: relevantRequestEnvironment(env),
			Packages: map[string]Package{"example/app": {ImportPath: "example/app", Name: "app", Dir: root, Imports: []string{"fmt"}, GoFiles: []string{"service.go"}}},
			Files:    map[string]string{service: serviceDigest, module: moduleDigest}, Syntax: map[string]string{service: syntax}, Directories: directories,
		}
		return &Recipe{Workspace: root, Bootstrap: base, ToolDigests: map[string]string{}}, goTool, service, module, env
	}

	tests := []struct {
		name, want string
		mutate     func(t *testing.T, recipe *Recipe, service, module string, env []string) []string
	}{
		{"import", "unsupported_input_changed", func(t *testing.T, _ *Recipe, service, _ string, env []string) []string {
			if err := os.WriteFile(service, []byte("package app\n\nimport \"os\"\n\nfunc Value() string { return os.Getenv(\"A\") }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"directive", "unsupported_input_changed", func(t *testing.T, _ *Recipe, service, _ string, env []string) []string {
			if err := os.WriteFile(service, []byte("//go:build darwin\n\npackage app\n\nfunc Value() string { return \"a\" }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"malformed", "package_selection_changed", func(t *testing.T, _ *Recipe, service, _ string, env []string) []string {
			if err := os.WriteFile(service, []byte("package"), 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"module", "unsupported_input_changed", func(t *testing.T, _ *Recipe, _ string, module string, env []string) []string {
			if err := os.WriteFile(module, []byte("module example/other\n\ngo 1.27\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"missing", "input_missing", func(t *testing.T, _ *Recipe, service, _ string, env []string) []string {
			if err := os.Remove(service); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"symlink", "input_not_regular", func(t *testing.T, _ *Recipe, service, module string, env []string) []string {
			if err := os.Remove(service); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(module, service); err != nil {
				t.Fatal(err)
			}
			return env
		}},
		{"environment", "build_configuration_changed", func(_ *testing.T, _ *Recipe, _, _ string, env []string) []string {
			return append(env, "GOEXPERIMENT=fieldtrack")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recipe, goTool, service, module, env := newFixture(t)
			env = test.mutate(t, recipe, service, module, env)
			capture, err := recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, reason := recipe.eligible(capture); reason != test.want {
				t.Fatalf("reason=%q want %q", reason, test.want)
			}
		})
	}
}

func TestRetainedCaptureRehashesExternalInputAfterMetadataRestoration(t *testing.T) {
	workspace := t.TempDir()
	external := filepath.Join(t.TempDir(), "dependency.txt")
	service := filepath.Join(workspace, "service.go")
	if err := os.WriteFile(service, []byte("package app\n\nfunc Value() string { return \"a\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("aa"), 0o600); err != nil {
		t.Fatal(err)
	}
	serviceDigest, _, _ := FileDigest(service)
	externalDigest, _, _ := FileDigest(external)
	serviceInfo, _ := os.Lstat(service)
	externalInfo, _ := os.Lstat(external)
	externalStamp := fileStamp(externalInfo)
	if externalStamp.ChangeTimeNano == 0 {
		t.Skip("platform has no change time; retained capture hashes external bytes directly")
	}
	syntax, _ := sourceSelectionIdentity(service)
	directories := map[string]string{}
	if err := captureDirectory(directories, workspace); err != nil {
		t.Fatal(err)
	}
	goTool, _ := os.Executable()
	goDigest, _, _ := FileDigest(goTool)
	env := os.Environ()
	base := Capture{
		GoVersion: "go fixture", GoToolDigest: goDigest, Environment: map[string]string{}, RequestEnv: relevantRequestEnvironment(env),
		Packages:   map[string]Package{"example/app": {ImportPath: "example/app", Name: "app", Dir: workspace, GoFiles: []string{"service.go"}}},
		Files:      map[string]string{service: serviceDigest, external: externalDigest},
		FileStamps: map[string]FileStamp{service: fileStamp(serviceInfo), external: externalStamp},
		Syntax:     map[string]string{service: syntax}, Directories: directories,
	}
	recipe := &Recipe{Workspace: workspace, Bootstrap: base, ToolDigests: map[string]string{}}
	originalModTime := externalInfo.ModTime()
	if err := os.WriteFile(external, []byte("bb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(external, originalModTime, originalModTime); err != nil {
		t.Fatal(err)
	}
	capture, err := recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := recipe.eligible(capture); reason != "unsupported_input_changed" {
		t.Fatalf("external B reason=%q", reason)
	}
	if capture.FileStamps[external].ChangeTimeNano == externalStamp.ChangeTimeNano {
		t.Fatal("external content mutation did not change ctime")
	}

	time.Sleep(time.Millisecond)
	if err := os.WriteFile(external, []byte("aa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(external, originalModTime, originalModTime); err != nil {
		t.Fatal(err)
	}
	capture, err = recipe.RetainedCapture(context.Background(), goTool, t.TempDir(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, reason := recipe.eligible(capture)
	if reason != "" || len(changed) != 0 || capture.Files[external] != externalDigest {
		t.Fatalf("external A/B/A: changed=%v reason=%q digest=%q", changed, reason, capture.Files[external])
	}
}

func TestEligibilityRejectsChangedToolIdentity(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "compile")
	if err := os.WriteFile(tool, []byte("tool-a"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, _, _ := FileDigest(tool)
	base := Capture{GoVersion: "go fixture", GoToolDigest: "sha256:go", Packages: map[string]Package{}, Files: map[string]string{}}
	recipe := &Recipe{Bootstrap: base, ToolDigests: map[string]string{tool: digest}}
	if err := os.WriteFile(tool, []byte("tool-b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, reason := recipe.eligible(base); reason != "tool_identity_changed" {
		t.Fatalf("reason=%q", reason)
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
