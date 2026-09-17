package build

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestDevelopmentProcessManifestsFollowEachEntrypointImportClosure(t *testing.T) {
	root := t.TempDir()
	module := &goListModule{Path: "example.test/app", Dir: root, GoMod: filepath.Join(root, "go.mod")}
	packages := []struct {
		dir     string
		imports []string
	}{
		{dir: "scenery_internal_main", imports: []string{"example.test/app/echo", "example.test/app/greeter"}},
		{dir: "scenery_internal_processes/host", imports: []string{"fmt"}},
		{dir: "scenery_internal_processes/services/echo_echo", imports: []string{"example.test/app/echo"}},
		{dir: "scenery_internal_processes/services/greeter_greeter", imports: []string{"example.test/app/greeter"}},
		{dir: "echo", imports: []string{"example.test/app/echo/scenerycontract", "example.test/app/shared"}},
		{dir: "echo/scenerycontract"},
		{dir: "greeter", imports: []string{"example.test/app/echo/scenerycontract"}},
		{dir: "shared"},
	}
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/app\n\ngo 1.27\n")
	var listed bytes.Buffer
	for _, item := range packages {
		write(item.dir+"/source.go", "package "+filepath.Base(item.dir)+"\n")
		if err := json.NewEncoder(&listed).Encode(goListPackage{
			Dir: filepath.Join(root, filepath.FromSlash(item.dir)), ImportPath: "example.test/app/" + item.dir,
			GoFiles: []string{"source.go"}, Imports: item.imports, Module: module,
		}); err != nil {
			t.Fatal(err)
		}
	}
	result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development"}}
	discover := func() *BuildInputManifest {
		t.Helper()
		manifest, err := buildInputManifestFromGoListObserved(context.Background(), result, listed.Bytes(), &buildInputDigestStats{})
		if err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	packageInputs := func(manifest *BuildInputManifest) []string {
		var inputs []string
		for _, entry := range manifest.Entries {
			if strings.HasPrefix(entry.Identity, "package/") {
				inputs = append(inputs, strings.TrimSuffix(strings.TrimPrefix(entry.Identity, "package/example.test/app/"), "/source.go"))
			}
		}
		return inputs
	}
	process := func(manifest *BuildInputManifest, name string) *BuildInputManifest {
		t.Helper()
		processManifest, _, err := manifest.developmentProcessInputs(name)
		if err != nil {
			t.Fatal(err)
		}
		return processManifest
	}
	identity := func(manifest *BuildInputManifest, name string) string {
		t.Helper()
		digest, _, err := manifest.developmentProcessDigest(name)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	before := discover()
	if inputs := packageInputs(before); slices.ContainsFunc(inputs, func(input string) bool { return strings.HasPrefix(input, "scenery_internal_processes") }) {
		t.Fatalf("application manifest includes process entrypoints: %v", inputs)
	}
	for name, want := range map[string][]string{
		"host":            {"scenery_internal_processes/host"},
		"echo_echo":       {"echo", "echo/scenerycontract", "scenery_internal_processes/services/echo_echo", "shared"},
		"greeter_greeter": {"echo/scenerycontract", "greeter", "scenery_internal_processes/services/greeter_greeter"},
	} {
		got := packageInputs(process(before, name))
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s process inputs = %v, want %v", name, got, want)
		}
	}
	write("echo/source.go", "package echo\n\nconst edited = true\n")
	afterEcho := discover()
	if identity(afterEcho, "echo_echo") == identity(before, "echo_echo") || identity(afterEcho, "greeter_greeter") != identity(before, "greeter_greeter") ||
		identity(afterEcho, "host") != identity(before, "host") {
		t.Fatal("an echo implementation edit did not change exactly the echo process identity")
	}
	if got := packageInputs(process(afterEcho, "echo_echo")); !slices.Contains(got, "echo") {
		t.Fatalf("echo process inputs after the edit = %v", got)
	}
	write("echo/scenerycontract/source.go", "package scenerycontract\n\nconst edited = true\n")
	afterContract := discover()
	if identity(afterContract, "echo_echo") == identity(afterEcho, "echo_echo") || identity(afterContract, "greeter_greeter") == identity(afterEcho, "greeter_greeter") {
		t.Fatal("an edit to a package both processes compile did not change both identities")
	}
	// Two entrypoints whose closures differ never share an identity, and the
	// memoized closure digests stay stable across repeated identification.
	repeated := identity(afterContract, "echo_echo")
	if identity(afterContract, "echo_echo") == identity(afterContract, "greeter_greeter") || identity(afterContract, "echo_echo") != repeated {
		t.Fatal("process identities collapsed or were unstable")
	}
	if _, _, err := afterContract.developmentProcessDigest("missing_missing"); err == nil {
		t.Fatal("unknown development process was identified")
	}
	if _, _, err := afterContract.developmentProcessInputs("missing_missing"); err == nil {
		t.Fatal("unknown development process was projected")
	}
}

func TestDevelopmentProcessBuildArgsMoveEveryLinkerFlagIntoEachEntrypoint(t *testing.T) {
	pending := []*DevelopmentProcess{
		{Name: "echo_echo", Package: "./scenery_internal_processes/services/echo_echo", Identity: DevelopmentProcessIdentity{ContractRevision: "c", ImplementationRevision: "i1", BuildInputDigest: "b1", GoTarget: "development"}},
		{Name: "host", Package: "./scenery_internal_processes/host", Identity: DevelopmentProcessIdentity{ContractRevision: "c", ImplementationRevision: "i2", BuildInputDigest: "b2", GoTarget: "development"}},
	}
	args := developmentProcessBuildArgs([]string{"-ldflags", "-s=false", " -trimpath ", "-ldflags=-X=main.flavor=dev"}, "/out", pending)
	for _, arg := range args {
		if arg == "-ldflags" || arg == "-s=false" || arg == "-ldflags=-X=main.flavor=dev" {
			t.Fatalf("build arguments kept the configured linker flag %q as a top-level argument: %q", arg, args)
		}
	}
	if !slices.Contains(args, "-trimpath") || args[len(args)-2] != pending[0].Package || args[len(args)-1] != pending[1].Package {
		t.Fatalf("build arguments = %q", args)
	}
	for _, process := range pending {
		prefix := "-ldflags=" + process.Package + "="
		index := slices.IndexFunc(args, func(arg string) bool { return strings.HasPrefix(arg, prefix) })
		if index < 0 {
			t.Fatalf("no package-scoped linker flags for %s: %q", process.Name, args)
		}
		value := strings.TrimPrefix(args[index], prefix)
		for _, want := range []string{"-w", "-s=false", "-X=main.flavor=dev", "linkedImplementationRevision=" + process.Identity.ImplementationRevision} {
			if !strings.Contains(value, want) {
				t.Fatalf("%s linker flags %q lack %q", process.Name, value, want)
			}
		}
	}
}

// An edit that relinks one service must not read the executables of unchanged
// processes, however many or large they are.
func TestRetainedDevelopmentProcessDigestsDoNotRereadUnchangedExecutables(t *testing.T) {
	root := t.TempDir()
	var hashed []string
	previous := developmentProcessFileDigest
	developmentProcessFileDigest = func(path string) (string, int64, error) {
		hashed = append(hashed, filepath.Base(path))
		return previous(path)
	}
	t.Cleanup(func() { developmentProcessFileDigest = previous })
	// link publishes an executable with the digest its link produced.
	link := func(path string, content []byte) string {
		t.Helper()
		if err := os.WriteFile(path, content, 0o755); err != nil {
			t.Fatal(err)
		}
		digest, _, err := previous(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := publishDevelopmentProcessDigest(path, digest); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	var paths []string
	digests := map[string]string{}
	for index, size := range []int{1, 64 << 10, 256 << 10} {
		path := filepath.Join(root, "service-"+string(rune('a'+index)))
		digests[path] = link(path, bytes.Repeat([]byte{byte(index)}, size))
		paths = append(paths, path)
	}
	for _, path := range paths {
		digest, ok, err := retainedDevelopmentProcessDigest(path)
		if err != nil || !ok || digest != digests[path] {
			t.Fatalf("first digest of %s = %s, %v, %v", path, digest, ok, err)
		}
	}
	if len(hashed) != 0 {
		t.Fatalf("reusing just linked executables hashed %v", hashed)
	}
	// A relinked executable replaces its file and its record; nothing else is
	// read again.
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	digests[paths[1]] = link(paths[1], []byte("relinked"))
	for _, path := range paths {
		digest, ok, err := retainedDevelopmentProcessDigest(path)
		if err != nil || !ok || digest != digests[path] {
			t.Fatalf("second digest of %s = %s, %v, %v", path, digest, ok, err)
		}
	}
	if len(hashed) != 0 {
		t.Fatalf("one-service edit hashed %v", hashed)
	}
	if _, ok, err := retainedDevelopmentProcessDigest(filepath.Join(root, "missing")); ok || err != nil {
		t.Fatalf("missing executable = %v, %v", ok, err)
	}
}

// Rehashing an executable observes its bytes; it must not make them the
// verified output of the linked identity its path names.
func TestModifiedDevelopmentProcessExecutableIsNotReused(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service-a")
	if err := os.WriteFile(path, []byte("behavior-A"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, _, err := developmentProcessFileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishDevelopmentProcessDigest(path, digest); err != nil {
		t.Fatal(err)
	}
	forget := func() {
		developmentProcessDigests.Lock()
		delete(developmentProcessDigests.values, path)
		developmentProcessDigests.Unlock()
	}
	t.Cleanup(forget)
	// Same length, different behavior, a new stamp.
	if err := os.WriteFile(path, []byte("behavior-B"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := retainedDevelopmentProcessDigest(path); ok || err != nil {
		t.Fatalf("a modified executable was reused: %v, %v", ok, err)
	}
	// A supervisor that starts later has nothing remembered and still refuses.
	forget()
	if _, ok, err := retainedDevelopmentProcessDigest(path); ok || err != nil {
		t.Fatalf("a modified executable was reused after a restart: %v, %v", ok, err)
	}
	// An executable without the record its link publishes is not trusted.
	if err := os.Remove(path + developmentProcessDigestSuffix); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := retainedDevelopmentProcessDigest(path); ok || err != nil {
		t.Fatalf("an executable without its published digest was reused: %v, %v", ok, err)
	}
	// Restored bytes match the published digest again.
	if err := os.WriteFile(path, []byte("behavior-A"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishDevelopmentProcessDigest(path, digest); err != nil {
		t.Fatal(err)
	}
	forget()
	if got, ok, err := retainedDevelopmentProcessDigest(path); !ok || err != nil || got != digest {
		t.Fatalf("the published executable was not reused: %s, %v, %v", got, ok, err)
	}
}

// The Go command reports package directories through the working directory it
// resolves itself, which is the canonical form of a workspace reached through
// a symbolic link, as a temporary directory under Darwin's /var is.
func TestDevelopmentProcessEntrypointsAreFoundThroughALinkedWorkspace(t *testing.T) {
	canonical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(canonical, linked); err != nil {
		t.Fatal(err)
	}
	module := &goListModule{Path: "example.test/app", Dir: canonical, GoMod: filepath.Join(canonical, "go.mod")}
	if err := os.WriteFile(filepath.Join(canonical, "go.mod"), []byte("module example.test/app\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var listed bytes.Buffer
	for _, dir := range []string{"scenery_internal_main", "scenery_internal_processes/host", "scenery_internal_processes/services/echo_echo"} {
		path := filepath.Join(canonical, filepath.FromSlash(dir))
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "main.go"), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(&listed).Encode(goListPackage{Dir: path, ImportPath: "example.test/app/" + dir, GoFiles: []string{"main.go"}, Module: module}); err != nil {
			t.Fatal(err)
		}
	}
	result := &Result{AppRoot: linked, Dir: linked, Target: &compiler.GoBuildTarget{Name: "development"}}
	manifest, err := buildInputManifestFromGoListObserved(context.Background(), result, listed.Bytes(), &buildInputDigestStats{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{DevelopmentProcessHost, "echo_echo"} {
		if _, _, err := manifest.developmentProcessDigest(name); err != nil {
			t.Fatalf("process %s was not found through the linked workspace: %v", name, err)
		}
	}
}
