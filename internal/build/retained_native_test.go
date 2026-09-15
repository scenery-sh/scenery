package build

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
)

func TestRetainedNativeCompilerIsDefaultOnlyForDevelopment(t *testing.T) {
	tests := []struct {
		name   string
		result *Result
		want   bool
	}{
		{name: "development", result: &Result{Target: &compiler.GoBuildTarget{Role: "development"}}, want: true},
		{name: "production role", result: &Result{Target: &compiler.GoBuildTarget{Role: "production"}}},
		{name: "ephemeral", result: &Result{Target: &compiler.GoBuildTarget{Role: "development"}, Ephemeral: true}},
		{name: "production assets", result: &Result{Target: &compiler.GoBuildTarget{Role: "development"}, ProductionAssets: true}},
		{name: "missing target", result: &Result{}},
		{name: "missing result"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldUseRetainedNativeCompiler(test.result); got != test.want {
				t.Fatalf("shouldUseRetainedNativeCompiler() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRetainedNativeCompilerDoesNotFallBackForWrappedPath(t *testing.T) {
	originalExecutable, originalRun := retainedNativeExecutable, runRetainedNativeCompiler
	defer func() { retainedNativeExecutable, runRetainedNativeCompiler = originalExecutable, originalRun }()
	retainedNativeExecutable = func() (string, bool) { return "/fixture/scenery", true }
	calls := 0
	runRetainedNativeCompiler = func(context.Context, *Result, string) error {
		calls++
		return nil
	}
	result := &Result{Target: &compiler.GoBuildTarget{Role: "development"}, GoEnvironment: []string{"PATH=/fixture/wrapper"}}
	if err := compileApplicationBinaryContext(context.Background(), result); err != nil || calls != 1 {
		t.Fatalf("development dispatch calls=%d err=%v", calls, err)
	}
}

func TestDecodeRetainedNativeJSONIsStrict(t *testing.T) {
	var decoded struct {
		Value string `json:"value"`
	}
	if err := decodeRetainedNativeJSON([]byte(`{"value":"ok"}`), &decoded); err != nil || decoded.Value != "ok" {
		t.Fatalf("valid JSON: decoded=%+v err=%v", decoded, err)
	}
	for _, input := range []string{
		`{"value":"ok","extra":true}`,
		`{"value":"ok"} {"value":"again"}`,
		`{"value":`,
	} {
		if err := decodeRetainedNativeJSON([]byte(input), &decoded); err == nil {
			t.Fatalf("decodeRetainedNativeJSON(%q) succeeded", input)
		}
	}
}

func TestPruneRetainedNativeDirectoriesKeepsCurrentWithoutLooping(t *testing.T) {
	root := t.TempDir()
	var directories []string
	for index, name := range []string{"current", "middle", "newest"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		directories = append(directories, path)
	}
	if err := pruneRetainedNativeDirectories(root, directories[0], 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directories[0]); err != nil {
		t.Fatalf("current directory was removed: %v", err)
	}
	if _, err := os.Stat(directories[1]); !os.IsNotExist(err) {
		t.Fatalf("old non-current directory still exists: %v", err)
	}
	if _, err := os.Stat(directories[2]); err != nil {
		t.Fatalf("newest directory was removed: %v", err)
	}
}

func TestRetainedNativeRootIsStableAndWorkspaceScoped(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "app")
	first, err := retainedNativeRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	again, err := retainedNativeRoot(filepath.Join(workspace, "."))
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("same workspace roots differ: %q != %q", first, again)
	}
	other, err := retainedNativeRoot(filepath.Join(t.TempDir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	if first == other {
		t.Fatalf("distinct workspaces share retained state root %q", first)
	}
}

func TestRetainedNativeCacheEntryLoadsRecipeOnce(t *testing.T) {
	entry := &retainedNativeCacheEntry{}
	calls := 0
	load := func() (*retainedNativeLoaded, error) {
		calls++
		return &retainedNativeLoaded{recipePath: "recipe.json"}, nil
	}
	first, err := entry.load(load)
	if err != nil {
		t.Fatal(err)
	}
	second, err := entry.load(load)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first != second {
		t.Fatalf("calls=%d first=%p second=%p", calls, first, second)
	}
}

func TestRetainedNativeGraphRefreshFlagsForceOnlySelectedPackages(t *testing.T) {
	packages := []string{"example/app/service", "example/app"}
	token := filepath.Join(t.TempDir(), "refresh")
	got, ok := retainedNativeGraphRefreshFlags(packages, token, []string{"GOFLAGS=-tags=fixture"}, []string{"-tags=fixture"})
	if !ok {
		t.Fatal("ordinary build flags rejected graph refresh")
	}
	mapping := filepath.Clean(token) + "=>" + filepath.Clean(token)
	want := []string{
		"-gcflags=example/app=-trimpath=" + mapping,
		"-gcflags=example/app/service=-trimpath=" + mapping,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flags = %#v, want %#v", got, want)
	}
	for _, configured := range [][]string{{"-gcflags=all=-N"}, {"-gcflags", "all=-N"}} {
		if _, ok := retainedNativeGraphRefreshFlags(packages, token, nil, configured); ok {
			t.Fatalf("configured compiler flags %#v did not require bootstrap", configured)
		}
	}
	if _, ok := retainedNativeGraphRefreshFlags(packages, token, []string{"GOFLAGS=-gcflags=all=-N"}, nil); ok {
		t.Fatal("GOFLAGS compiler settings did not require bootstrap")
	}
}
