package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/parse"
)

func TestPrepareAndCompileNativeContractApplication(t *testing.T) {
	repository := repoRoot(t)
	fixture := filepath.Join(repository, "internal", "vnext", "testdata", "native")
	parent := filepath.Dir(fixture)
	appRoot, err := os.MkdirTemp(parent, "native-build-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(appRoot) })
	if err := copyTree(fixture, appRoot); err != nil {
		t.Fatalf("copy native fixture: %v", err)
	}

	appModel, err := parse.App(appRoot, "nativeapp")
	if err != nil {
		t.Fatalf("parse native app: %v", err)
	}
	result, err := Prepare(appRoot, appModel, appcfg.Config{Name: "nativeapp"})
	if err != nil {
		t.Fatalf("prepare native app: %v", err)
	}

	mainSource, err := os.ReadFile(filepath.Join(result.Dir, "scenery_internal_main", "main.go"))
	if err != nil {
		t.Fatalf("read generated main: %v", err)
	}
	for _, fragment := range []string{
		`scenerycomposition "example.test/nativeapp/internal/scenerygen/composition"`,
		"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
		"sceneryruntime.NewContractRegistry",
		"scenerycomposition.Register(contractRegistry)",
		"contractRegistry.Seal()",
	} {
		if !strings.Contains(string(mainSource), fragment) {
			t.Fatalf("generated main missing %q:\n%s", fragment, mainSource)
		}
	}
	if slices.Contains(result.GeneratedFiles, "house/scenery.gen.go") {
		t.Fatalf("native service received legacy generated registration: %v", result.GeneratedFiles)
	}
	if _, err := os.Stat(filepath.Join(result.Dir, "house", "scenery.gen.go")); !os.IsNotExist(err) {
		t.Fatalf("native service legacy registration exists, stat error = %v", err)
	}

	if err := Compile(result); err != nil {
		t.Fatalf("compile native app: %v", err)
	}
	if _, err := os.Stat(result.Binary); err != nil {
		t.Fatalf("native app binary missing: %v", err)
	}
	bundle, err := ReadVNextRuntimeBundle(appRoot, "development")
	if err != nil {
		t.Fatalf("runtime bundle: %v", err)
	}
	if bundle.ContractRevision == "" || bundle.ImplementationRevision == "" || bundle.BuildInput == nil || bundle.BuildInput.Digest == "" {
		t.Fatalf("runtime bundle is incomplete: %#v", bundle)
	}
}

func TestPrepareAndCompileNativeContractWithLegacyGoBridge(t *testing.T) {
	repository := repoRoot(t)
	fixture := filepath.Join(repository, "internal", "vnext", "testdata", "bridge")
	parent := filepath.Dir(fixture)
	appRoot, err := os.MkdirTemp(parent, "bridge-build-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(appRoot) })
	if err := copyTree(fixture, appRoot); err != nil {
		t.Fatalf("copy bridge fixture: %v", err)
	}

	appModel, err := parse.App(appRoot, "bridgeapp")
	if err != nil {
		t.Fatalf("parse bridge app: %v", err)
	}
	result, err := Prepare(appRoot, appModel, appcfg.Config{Name: "bridgeapp"})
	if err != nil {
		t.Fatalf("prepare bridge app: %v", err)
	}
	if slices.Contains(result.GeneratedFiles, "bridge/scenery.gen.go") {
		t.Fatalf("bridge service received duplicate legacy registration: %v", result.GeneratedFiles)
	}
	bridgeSource, err := os.ReadFile(filepath.Join(result.Dir, "bridge", "scenery.bridge.gen.go"))
	if err != nil {
		t.Fatalf("read bridge helper: %v", err)
	}
	for _, fragment := range []string{"SceneryVNextBridgeInitialize", "SceneryVNextBridgeEcho", "SceneryVNextLegacyCallEchoInput", "SceneryVNextLegacyCallEchoOutput", "service.Echo(ctx, payload)"} {
		if !strings.Contains(string(bridgeSource), fragment) {
			t.Fatalf("bridge helper missing %q:\n%s", fragment, bridgeSource)
		}
	}
	adapterSource, err := os.ReadFile(filepath.Join(result.Dir, "internal", "scenerygen", "bridge_bridge_adapter", "adapter.gen.go"))
	if err != nil {
		t.Fatalf("read bridge adapter: %v", err)
	}
	for _, fragment := range []string{"RegisterEndpointChecked", `Name: "Echo"`, "SceneryVNextLegacyCallEchoOutput"} {
		if !strings.Contains(string(adapterSource), fragment) {
			t.Fatalf("bridge adapter missing %q:\n%s", fragment, adapterSource)
		}
	}
	if err := Compile(result); err != nil {
		t.Fatalf("compile bridge app: %v", err)
	}
}
