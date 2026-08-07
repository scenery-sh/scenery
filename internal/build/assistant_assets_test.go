package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/toolchain"
)

func TestAssistantAssetTargetUsesExplicitTargetPlatformAndManagedNodePath(t *testing.T) {
	target := &compiler.GoBuildTarget{Context: parse.GoTargetContext{GOOS: "linux", GOARCH: "amd64"}}
	platform, err := assistantAssetTargetPlatform(target)
	if err != nil {
		t.Fatal(err)
	}
	if platform.String() != "linux/amd64" {
		t.Fatalf("target platform = %s, want linux/amd64", platform.String())
	}
	if got := managedNodeExecutable("/managed/node"); got != "/managed/node/bin/node" {
		t.Fatalf("managed node path = %s", got)
	}
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		t.Fatal(err)
	}
	node, ok := manifest.Artifact("node")
	if !ok {
		t.Fatal("bundled manifest has no node artifact")
	}
	if _, ok := node.PlatformArtifact(platform); !ok {
		t.Fatal("bundled node artifact has no explicit linux/amd64 platform")
	}
	if got := managedNodeExecutable("/host/node"); strings.Contains(got, runtime.GOROOT()) {
		t.Fatal("managed node path unexpectedly uses host Go installation")
	}
}

func TestAssistantAssetAuthoredPackageAndLockAreImmutable(t *testing.T) {
	root := t.TempDir()
	packagePath := filepath.Join(root, "package.json")
	lockPath := filepath.Join(root, "package-lock.json")
	packageData := []byte(`{"name":"assistant"}`)
	lockData := []byte(`{"lockfileVersion":3}`)
	if err := os.WriteFile(packagePath, packageData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, lockData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileUnchanged(packagePath, packageData); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileUnchanged(lockPath, lockData); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(`{"lockfileVersion":3,"changed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileUnchanged(lockPath, lockData); err == nil {
		t.Fatal("mutated package lock was accepted")
	}
}

func TestCopyDeterministicCapsuleNormalizesAbsolutePathsAndBuildMetadata(t *testing.T) {
	makeSource := func(name string) string {
		root := filepath.Join(t.TempDir(), name)
		for _, rel := range []string{".output/server", ".scenery"} {
			if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		write := func(rel, body string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeBytes := func(rel string, body []byte) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, rel), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		buildID := "mse413nq-0bc9355e-ba97-4232-948e-e7e47071c5d6"
		if name == "second" {
			buildID = "mse4349i-1fc5f786-14b3-412c-a9eb-7479e39329e0"
		}
		write(".output/server/index.mjs", `const roots = "/PLACEHOLDER/agent"; const build = ".eve/builds/`+buildID+`/host";`)
		write(".output/nitro.json", `{"preset":"node-server","date":"2026-08-04T03:04:46.761Z"}`)
		writeBytes(".output/server/native.node", []byte{0x00, 0x7f, '.', 'e', 'v', 'e', '/', 'b', 'u', 'i', 'l', 'd', 's', '/', 'b', 'i', 'n', 'a', 'r', 'y', '/', 0xff})
		write(".scenery/bootstrap.mjs", "// generated\n")
		return root
	}
	firstSource := makeSource("first")
	secondSource := makeSource("second")
	firstIndex, _ := os.ReadFile(filepath.Join(firstSource, ".output/server/index.mjs"))
	if err := os.WriteFile(filepath.Join(firstSource, ".output/server/index.mjs"), bytes.ReplaceAll(firstIndex, []byte("/PLACEHOLDER"), []byte(firstSource)), 0o644); err != nil {
		t.Fatal(err)
	}
	secondIndex, _ := os.ReadFile(filepath.Join(secondSource, ".output/server/index.mjs"))
	if err := os.WriteFile(filepath.Join(secondSource, ".output/server/index.mjs"), bytes.ReplaceAll(secondIndex, []byte("/PLACEHOLDER"), []byte(secondSource)), 0o644); err != nil {
		t.Fatal(err)
	}
	firstCapsule := filepath.Join(t.TempDir(), "capsule")
	secondCapsule := filepath.Join(t.TempDir(), "capsule")
	if err := copyDeterministicCapsule(firstSource, firstCapsule); err != nil {
		t.Fatal(err)
	}
	if err := copyDeterministicCapsule(secondSource, secondCapsule); err != nil {
		t.Fatal(err)
	}
	firstArchive, err := runtimeassets.BuildArchive(firstCapsule)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := runtimeassets.BuildArchive(secondCapsule)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive.Data, secondArchive.Data) {
		t.Fatal("capsule archive changed with source workspace path")
	}
	index, err := os.ReadFile(filepath.Join(firstCapsule, ".output/server/index.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(index, []byte(firstSource)) || !bytes.Contains(index, []byte("/scenery-assistant")) {
		t.Fatalf("absolute source path was not normalized: %s", index)
	}
	if !bytes.Contains(index, []byte(".eve/builds/build/host")) || bytes.Contains(index, []byte("mse413nq-")) {
		t.Fatalf("Eve build identifier was not normalized: %s", index)
	}
	native, err := os.ReadFile(filepath.Join(firstCapsule, ".output/server/native.node"))
	if err != nil {
		t.Fatal(err)
	}
	wantNative := []byte{0x00, 0x7f, '.', 'e', 'v', 'e', '/', 'b', 'u', 'i', 'l', 'd', 's', '/', 'b', 'i', 'n', 'a', 'r', 'y', '/', 0xff}
	if !bytes.Equal(native, wantNative) {
		t.Fatalf("binary capsule dependency was rewritten: %x", native)
	}
	metadata, err := os.ReadFile(filepath.Join(firstCapsule, ".output/nitro.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(metadata, &value); err != nil {
		t.Fatal(err)
	}
	if value["date"] != "1970-01-01T00:00:00.000Z" {
		t.Fatalf("nitro date was not normalized: %#v", value["date"])
	}
}

func TestCopyDeterministicCapsuleFailureCleansPartialDestination(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(source, "bad")); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "capsule")
	if err := copyDeterministicCapsule(source, destination); err == nil {
		t.Fatal("escaping source symlink should fail")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed capsule destination was not removed: %v", err)
	}
}
