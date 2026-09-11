package build

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestNativeKernelProjectionMatchesIndependentConsumedGraph(t *testing.T) {
	root := t.TempDir()
	module := &goListModule{Path: "example.test/app", GoMod: filepath.Join(root, "go.mod")}
	writeBuildTestFile(t, root, "go.mod", "module example.test/app\ngo 1.26\n")
	writeBuildTestFile(t, root, "native/input", "explicit native input")
	packages := []goListPackage{
		{Dir: filepath.Join(root, "scenery_framework_kernel"), ImportPath: "example.test/app/scenery_framework_kernel", Imports: []string{"example.test/shared", "unsafe"}, GoFiles: []string{"main.go"}, Module: module},
		{Dir: filepath.Join(root, "shared"), ImportPath: "example.test/shared", GoFiles: []string{"value.go"}, CgoFiles: []string{"cgo.go"}, CFiles: []string{"value.c"}, HFiles: []string{"value.h"}, EmbedFiles: []string{"asset.txt"}, Module: &goListModule{Path: "example.test/shared", Replace: &goListModule{GoMod: filepath.Join(root, "shared/go.mod")}}},
		{ImportPath: "unsafe", Standard: true},
		{Dir: filepath.Join(root, "app"), ImportPath: "example.test/app/app", Imports: []string{"example.test/shared"}, GoFiles: []string{"api.go"}, Module: module},
		{Dir: filepath.Join(root, "shared/child"), ImportPath: "example.test/shared/child", GoFiles: []string{"unused.go"}, Module: module},
	}
	for _, pkg := range packages {
		for _, file := range consumedPackageFiles(pkg) {
			relative, err := filepath.Rel(root, filepath.Join(pkg.Dir, file))
			if err != nil {
				t.Fatal(err)
			}
			writeBuildTestFile(t, root, relative, relative)
		}
	}
	writeBuildTestFile(t, root, "shared/go.mod", "module example.test/shared\ngo 1.26\n")
	result := &Result{AppRoot: root, Dir: root, Target: &compiler.GoBuildTarget{Name: "development", Effective: map[string]any{"native_inputs": []any{"native"}}}}
	capture := func(packages []goListPackage) []byte {
		t.Helper()
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		for _, pkg := range packages {
			if err := encoder.Encode(pkg); err != nil {
				t.Fatal(err)
			}
		}
		return output.Bytes()
	}
	measure := func() (*BuildInputManifest, *BuildInputManifest) {
		t.Helper()
		output := capture(packages)
		full, err := buildInputManifestFromGoList(result, output)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := projectNativeKernelInputs(result, output, full)
		if err != nil {
			t.Fatal(err)
		}
		independent, err := buildInputManifestFromGoList(result, capture(packages[:3]))
		if err != nil || independent.Digest != projected.Digest {
			t.Fatalf("kernel projection differs from independent discovery: %v", err)
		}
		return full, projected
	}
	fullBefore, kernelBefore := measure()
	writeBuildTestFile(t, root, "app/api.go", "semantic native edit")
	fullAfter, kernelAfter := measure()
	if fullBefore.Digest == fullAfter.Digest || kernelBefore.Digest != kernelAfter.Digest {
		t.Fatal("native edit must invalidate the full target while preserving the independent kernel")
	}
	writeBuildTestFile(t, root, "shared/asset.txt", "changed embedded dependency")
	_, kernelChanged := measure()
	if kernelAfter.Digest == kernelChanged.Digest {
		t.Fatal("kernel reused despite changed consumed dependency bytes")
	}
	packages[1].GoFiles = append(packages[1].GoFiles, "added.go")
	writeBuildTestFile(t, root, "shared/added.go", "new consumed source")
	_, kernelAdded := measure()
	if kernelAdded.Digest == kernelChanged.Digest {
		t.Fatal("new dependency member omitted from kernel identity")
	}
	full, _ := measure()
	if _, err := projectNativeKernelInputs(result, capture(append(slices.Clone(packages[:1]), packages[2:]...)), full); err == nil {
		t.Fatal("incomplete dependency graph accepted")
	}
	missing := *full
	missing.Entries = slices.DeleteFunc(slices.Clone(full.Entries), func(input BuildInput) bool { return input.Identity == "package/example.test/shared/value.h" })
	if _, err := projectNativeKernelInputs(result, capture(packages), &missing); err == nil {
		t.Fatal("missing consumed native header evidence accepted")
	}
	if _, err := projectNativeKernelInputs(result, capture(packages[1:]), full); err == nil {
		t.Fatal("missing kernel entrypoint accepted")
	}
}
