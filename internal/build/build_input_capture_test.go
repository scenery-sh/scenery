package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestBuildInputCaptureReadsSharedModuleOnceAndRecapturesChanges(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "go.mod")
	beforeModule := []byte("module example.test/app\n// revision 1\n")
	if err := os.WriteFile(module, beforeModule, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	expected := map[string]string{}
	for _, name := range []string{"one", "two", "three"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "value.go")
		if err := os.WriteFile(file, []byte("package "+name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		pkg := goListPackage{Dir: dir, ImportPath: "example.test/app/" + name, GoFiles: []string{"value.go"}, Module: &goListModule{Path: "example.test/app", GoMod: module}}
		if err := json.NewEncoder(&output).Encode(pkg); err != nil {
			t.Fatal(err)
		}
		if err := addBuildInput(expected, "package/"+pkg.ImportPath+"/value.go", file); err != nil {
			t.Fatal(err)
		}
	}
	if err := addBuildInput(expected, "module/example.test/app/go.mod", module); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("example.test/app@\x00\x00"))
	expected["module/example.test/app"] = "sha256:" + hex.EncodeToString(sum[:])
	result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development"}}
	calls := map[string]int{}
	capture := func(entries map[string]string, identity, path string) error {
		calls[path]++
		return addBuildInput(entries, identity, path)
	}
	before, err := captureBuildInputManifest(result, output.Bytes(), capture)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != newBuildInputManifest("development", expected).Digest {
		t.Fatal("unique capture changed the manifest")
	}
	for path, count := range calls {
		if count != 1 {
			t.Errorf("captured %s %d times, want once", path, count)
		}
	}
	info, err := os.Stat(module)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, bytes.Replace(beforeModule, []byte("revision 1"), []byte("revision 2"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(module, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := captureBuildInputManifest(result, output.Bytes(), capture)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatal("independent capture reused stale module bytes")
	}
	if calls[module] != 2 {
		t.Fatalf("two captures read the shared module %d times, want twice", calls[module])
	}
}

func TestBuildInputCaptureRejectsAmbiguousIdentityBeforeReading(t *testing.T) {
	for _, kind := range []string{"module path", "module metadata", "package path"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			first := goListPackage{Dir: root, ImportPath: "example.test/app/one", GoFiles: []string{"value.go"}, Module: &goListModule{Path: "example.test/app", GoMod: filepath.Join(root, "go.mod")}}
			second := first
			copiedModule := *first.Module
			second.Module = &copiedModule
			switch kind {
			case "module path":
				second.ImportPath = "example.test/app/two"
				second.Module.GoMod = filepath.Join(root, "other", "go.mod")
			case "module metadata":
				second.ImportPath = "example.test/app/two"
				second.Module.Version = "v1.0.0"
			case "package path":
				second.Dir = filepath.Join(root, "other")
			}
			var output bytes.Buffer
			for _, pkg := range []goListPackage{first, second} {
				if err := json.NewEncoder(&output).Encode(pkg); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			capture := func(entries map[string]string, identity, path string) error {
				reads++
				entries[identity] = "same bytes"
				return nil
			}
			_, err := captureBuildInputManifest(&Result{AppRoot: root, Target: &compiler.GoBuildTarget{Name: "development"}}, output.Bytes(), capture)
			if err == nil || !strings.Contains(err.Error(), "identity collision") {
				t.Fatalf("ambiguous identity: %v", err)
			}
			if reads != 0 {
				t.Fatalf("read %d inputs before rejecting ambiguous membership", reads)
			}
		})
	}
}
