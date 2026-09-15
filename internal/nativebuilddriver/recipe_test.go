package nativebuilddriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if relative, ok := workspaceRelative(root, goFile); !ok {
		t.Fatalf("fixture file is not within workspace: relative=%q root=%q file=%q", relative, root, goFile)
	}
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

func TestAdvanceMovesTheBaselineAndReusesEarlierPackageResults(t *testing.T) {
	recipe, files := newRetainedRecipeFixture(t)
	linkAlias := filepath.Join(recipe.Root, "go-cache", "a.a")
	recipe.Link.Imports[linkAlias] = "example/a"
	recipe.ArchiveByOld[linkAlias] = recipe.ArchiveByOld[recipe.Compiles["example/a"].Output.Original]
	buildA := retainedFixtureBuild(t, recipe, files, "a")
	next, err := recipe.Advance(buildA, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if next.Current.Files[files["a"]] == recipe.Current.Files[files["a"]] {
		t.Fatal("successful A source identity was not committed")
	}
	aArchive := next.ArchiveByOld[next.Compiles["example/a"].Output.Original]
	if aArchive == recipe.ArchiveByOld[recipe.Compiles["example/a"].Output.Original] {
		t.Fatal("successful A archive was not committed")
	}
	if next.ArchiveByOld[linkAlias] != aArchive {
		t.Fatalf("linker alias retained stale A archive: %q != %q", next.ArchiveByOld[linkAlias], aArchive)
	}

	currentB := cloneCapture(next.Current)
	currentB.Files[files["b"]] = "sha256:" + fmt.Sprintf("%064x", 0xb)
	changed, reason := next.eligible(currentB)
	if reason != "" || !reflect.DeepEqual(changed, []string{"example/b"}) {
		t.Fatalf("B after committed A: changed=%v reason=%q", changed, reason)
	}
	rebuilt, err := next.rebuildOrder(changed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt, []string{"example/b", "example/main"}) {
		t.Fatalf("B build accumulated A: %v", rebuilt)
	}
	if next.ArchiveByOld[next.Compiles["example/a"].Output.Original] != aArchive {
		t.Fatal("committed A archive changed while planning B")
	}

	failed := retainedFixtureBuild(t, next, files, "c")
	failed.archiveOutputs["example/c"] = filepath.Join(t.TempDir(), "missing")
	if _, err := next.Advance(failed, filepath.Join(t.TempDir(), "failed-state")); err == nil {
		t.Fatal("missing candidate archive was committed")
	}
	if next.Current.Files[files["c"]] != recipe.Current.Files[files["c"]] {
		t.Fatal("failed advance mutated the receiver")
	}
}

func TestCompileArgsRebindsRetainedEmbedConfiguration(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workspace, "embed.go")
	snapshot := filepath.Join(root, "snapshot.go")
	embedOriginal := filepath.Join(root, "deleted-bootstrap", "embedcfg")
	embedCopy := filepath.Join(root, "retained", "embedcfg")
	importCfg := filepath.Join(root, "retained", "importcfg")
	for path, data := range map[string]string{source: "package embed\n", snapshot: "package embed\n", embedCopy: `{\"Patterns\":{},\"Files\":{}}`, importCfg: ""} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	action := &CompileAction{Package: "example/embed", Argv: []string{"-o", "old", "-p", "example/embed", "-importcfg", "old-importcfg", "-embedcfg", embedOriginal, source}, OutputAt: 0, ImportCfgAt: 5,
		Files: map[int]FileCopy{5: {Original: importCfg, Copy: importCfg}, 7: {Original: embedOriginal, Copy: embedCopy}, 8: {Original: source, Copy: snapshot}}}
	recipe := &Recipe{Workspace: workspace}
	capture := Capture{Digest: "sha256:current", Files: map[string]string{source: "sha256:source"}, SnapshotFiles: map[string]string{source: snapshot}}
	args, err := recipe.compileArgs(action, capture, nil, filepath.Join(root, "out.a"), filepath.Join(root, "generation"))
	if err != nil {
		t.Fatal(err)
	}
	if args[7] != embedCopy || args[7] == embedOriginal {
		t.Fatalf("embedcfg was not rebound: %q", args[7])
	}
	if _, err := os.Stat(args[7]); err != nil {
		t.Fatalf("rebound embedcfg is unavailable: %v", err)
	}
}

func TestUnsupportedNativeFrontierIsRejectedBeforeExecution(t *testing.T) {
	recipe := &Recipe{Current: Capture{Protocol: ProtocolVersion, Digest: "sha256:current", Packages: map[string]Package{
		"example/leaf": {ImportPath: "example/leaf", SFiles: []string{"leaf.s"}},
		"example/main": {ImportPath: "example/main", Imports: []string{"example/leaf"}},
	}}, Compiles: map[string]*CompileAction{"example/leaf": {}, "example/main": {}}}
	rebuilt, err := recipe.rebuildOrder([]string{"example/leaf"})
	if err != nil {
		t.Fatal(err)
	}
	if reason := recipe.unsupportedFrontier(rebuilt); reason != "unsupported_native_action_frontier" {
		t.Fatalf("reason=%q rebuilt=%v", reason, rebuilt)
	}
}

func TestRetainToolDigestHashesEachDistinctToolOnce(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "compile")
	if err := os.WriteFile(tool, []byte("tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := &Recipe{ToolDigests: map[string]string{}}
	calls := 0
	digest := func(path string) (string, int64, error) {
		calls++
		return FileDigest(path)
	}
	for range 200 {
		if err := retainToolDigestWith(recipe, tool, digest); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("tool was fully hashed %d times", calls)
	}
}

func newRetainedRecipeFixture(t *testing.T) (*Recipe, map[string]string) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	capture := Capture{Protocol: ProtocolVersion, Workspace: workspace, Digest: "sha256:bootstrap", GoVersion: "go fixture", GoToolDigest: "sha256:go", Packages: map[string]Package{}, Files: map[string]string{}, FileStamps: map[string]FileStamp{}, Syntax: map[string]string{}, Directories: map[string]string{}, SnapshotFiles: map[string]string{}, Environment: map[string]string{}, RequestEnv: map[string]string{}}
	for _, name := range []string{"a", "b", "c", "main"} {
		path := filepath.Join(workspace, name+".go")
		data := []byte("package " + name + "\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		digest, _, _ := FileDigest(path)
		files[name] = path
		capture.Files[path], capture.Syntax[path], capture.SnapshotFiles[path] = digest, "sha256:syntax", path
		imports := []string(nil)
		if name == "main" {
			imports = []string{"example/a", "example/b", "example/c"}
		}
		capture.Packages["example/"+name] = Package{ImportPath: "example/" + name, Name: name, Dir: workspace, Imports: imports, GoFiles: []string{name + ".go"}}
	}
	tool := filepath.Join(root, "compile")
	if err := os.WriteFile(tool, []byte("tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	toolDigest, _, _ := FileDigest(tool)
	recipe := &Recipe{Protocol: ProtocolVersion, Root: root, Workspace: workspace, Bootstrap: cloneCaptureValue(capture), Current: cloneCaptureValue(capture), ToolDigests: map[string]string{tool: toolDigest}, Retained: map[string]RetainedFile{}, Support: map[string]RetainedFile{}, Compiles: map[string]*CompileAction{}, ArchiveByOld: map[string]string{}}
	for _, name := range []string{"a", "b", "c", "main"} {
		cfg := filepath.Join(root, name+".importcfg")
		if err := os.WriteFile(cfg, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		cfgDigest, cfgBytes, _ := FileDigest(cfg)
		cfgInfo, _ := os.Lstat(cfg)
		recipe.Support[cfg] = RetainedFile{Digest: cfgDigest, Bytes: cfgBytes, Stamp: fileStamp(cfgInfo)}
		archive := filepath.Join(root, name+".a")
		if err := os.WriteFile(archive, []byte("archive-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		archiveDigest, archiveBytes, _ := FileDigest(archive)
		archiveInfo, _ := os.Lstat(archive)
		recipe.Retained[archive] = RetainedFile{Digest: archiveDigest, Bytes: archiveBytes, Stamp: fileStamp(archiveInfo)}
		recipe.RetainedBytes += archiveBytes
		recipe.ArchiveByOld[archive] = archive
		recipe.Compiles["example/"+name] = &CompileAction{Package: "example/" + name, Tool: tool, Argv: []string{"-o", archive, "-p", "example/" + name, "-importcfg", cfg, files[name]}, Files: map[int]FileCopy{5: {Original: cfg, Copy: cfg, Digest: cfgDigest, Bytes: cfgBytes}, 6: {Original: files[name], Copy: files[name]}}, Output: FileCopy{Original: archive, Copy: archive, Digest: archiveDigest, Bytes: archiveBytes}, ImportCfgAt: 5, OutputAt: 0}
	}
	linkCfg := filepath.Join(root, "link.importcfg")
	if err := os.WriteFile(linkCfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linkCfgDigest, linkCfgBytes, _ := FileDigest(linkCfg)
	linkCfgInfo, _ := os.Lstat(linkCfg)
	recipe.Support[linkCfg] = RetainedFile{Digest: linkCfgDigest, Bytes: linkCfgBytes, Stamp: fileStamp(linkCfgInfo)}
	mainArchive := recipe.Compiles["example/main"].Output
	recipe.Link = &LinkAction{Tool: tool, Argv: []string{"-o", filepath.Join(root, "binary"), "-importcfg", linkCfg, mainArchive.Original}, Files: map[int]FileCopy{3: {Original: linkCfg, Copy: linkCfg, Digest: linkCfgDigest, Bytes: linkCfgBytes}, 4: mainArchive}, OutputAt: 0, ImportCfgAt: 3, MainAt: 4, Imports: map[string]string{}}
	recipe.RetentionLimit = recipe.RetainedBytes*2 + 512<<20
	if err := recipe.rebuildSupportAccounting(); err != nil {
		t.Fatal(err)
	}
	if err := recipe.Validate(); err != nil {
		t.Fatal(err)
	}
	return recipe, files
}

func retainedFixtureBuild(t *testing.T, recipe *Recipe, files map[string]string, name string) BuildResult {
	t.Helper()
	capture := cloneCaptureValue(recipe.Current)
	data := []byte("package " + name + "\n// changed\n")
	snapshot := filepath.Join(t.TempDir(), name+".go")
	if err := os.WriteFile(snapshot, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, _, _ := FileDigest(snapshot)
	capture.Files[files[name]], capture.SnapshotFiles[files[name]], capture.Digest = digest, snapshot, "sha256:"+name
	outputs, artifacts := map[string]string{}, map[string]string{}
	for _, pkg := range []string{"example/" + name, "example/main"} {
		output := filepath.Join(t.TempDir(), filepath.Base(pkg)+".a")
		if err := os.WriteFile(output, []byte("new-"+pkg+"-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		archiveDigest, _, _ := FileDigest(output)
		outputs[pkg], artifacts[pkg] = output, archiveDigest
	}
	return BuildResult{Status: "supported_and_rebuilt", capture: capture, archiveOutputs: outputs, ActionArtifacts: artifacts}
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

func TestPruneUnreferencedKeepsOnlyPublishedRecipeState(t *testing.T) {
	root := t.TempDir()
	kept := filepath.Join(root, "artifacts", "kept.a")
	stale := filepath.Join(root, "artifacts", "stale.a")
	keptSupport := filepath.Join(root, "support", "kept.cfg")
	staleSnapshot := filepath.Join(root, "snapshots", "stale.go")
	for path, data := range map[string]string{kept: "kept", stale: "stale", keptSupport: "support", staleSnapshot: "snapshot"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recipe := &Recipe{Retained: map[string]RetainedFile{kept: {}}, Support: map[string]RetainedFile{keptSupport: {}}}
	if err := recipe.PruneUnreferenced(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{kept, keptSupport} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("published state %s: %v", path, err)
		}
	}
	for _, path := range []string{stale, staleSnapshot} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale state %s still exists: %v", path, err)
		}
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
