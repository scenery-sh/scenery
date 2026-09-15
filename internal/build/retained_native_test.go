package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/nativebuilddriver"
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

func TestRetainedNativeCompilerRespectsSelectedGoDriver(t *testing.T) {
	stock := filepath.Dir(stockGoDriverPath())
	if !usesStockGoDriver([]string{"PATH=" + stock}) {
		t.Fatalf("runtime Go driver at %s was not recognized", stock)
	}
	wrapperRoot := t.TempDir()
	wrapper := filepath.Join(wrapperRoot, "go")
	if err := os.WriteFile(wrapper, []byte("wrapper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if usesStockGoDriver([]string{"PATH=" + wrapperRoot + string(os.PathListSeparator) + stock}) {
		t.Fatal("custom Go driver was bypassed by the retained backend")
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

func TestRetainedNativeGraphRefreshFlagsForceOnlyWorkspacePackages(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"service", "scenery_internal_main"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspaceAlias := filepath.Join(parent, "workspace-alias")
	if err := os.Symlink(workspace, workspaceAlias); err != nil {
		t.Fatal(err)
	}
	packages := map[string]nativebuilddriver.Package{
		"example/app/service": {Dir: filepath.Join(workspaceAlias, "service")},
		"example/app":         {Dir: filepath.Join(workspace, "scenery_internal_main")},
		"example/dependency":  {Dir: filepath.Join(t.TempDir(), "dependency")},
	}
	token := filepath.Join(t.TempDir(), "refresh")
	got, ok := retainedNativeGraphRefreshFlags(workspace, packages, token, []string{"GOFLAGS=-tags=fixture"}, []string{"-tags=fixture"})
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
		if _, ok := retainedNativeGraphRefreshFlags(workspace, packages, token, nil, configured); ok {
			t.Fatalf("configured compiler flags %#v did not require bootstrap", configured)
		}
	}
	if _, ok := retainedNativeGraphRefreshFlags(workspace, packages, token, []string{"GOFLAGS=-gcflags=all=-N"}, nil); ok {
		t.Fatal("GOFLAGS compiler settings did not require bootstrap")
	}
}
