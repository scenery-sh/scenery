package build

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/parse"
)

func TestPrepareAndCompileNativeContractApplication(t *testing.T) {
	appRoot := copyVNextBuildFixture(t, "native")

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
	runGeneratedTypeScriptClientAgainstBinary(t, appRoot, result.Binary)
}

func runGeneratedTypeScriptClientAgainstBinary(t *testing.T, appRoot, binary string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	logPath := filepath.Join(t.TempDir(), "reference-server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	server := exec.CommandContext(ctx, binary)
	server.Dir = appRoot
	server.Env = append(os.Environ(), "SCENERY_LISTEN_ADDR="+address)
	server.Stdout = logFile
	server.Stderr = logFile
	if err := server.Start(); err != nil {
		t.Fatalf("start generated reference server: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	stopped := false
	stopServer := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
		_ = logFile.Sync()
	}
	defer stopServer()
	serverOutput := func() string {
		stopServer()
		data, _ := os.ReadFile(logPath)
		return string(data)
	}

	baseURL := "http://" + address
	baseURLPath := filepath.Join(appRoot, "typescript_reference_server_url.txt")
	if err := os.WriteFile(baseURLPath, []byte(baseURL), 0o600); err != nil {
		t.Fatalf("write generated reference server URL: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, requestErr := http.Get(baseURL + "/__scenery_reference_ready")
		if requestErr == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("generated reference server did not become ready: %v\n%s", requestErr, serverOutput())
		}
		time.Sleep(25 * time.Millisecond)
	}

	bun := exec.Command("bun", "test", "./typescript_reference_server.test.ts")
	bun.Dir = appRoot
	bunOutput, err := bun.CombinedOutput()
	if err != nil {
		t.Fatalf("generated TypeScript client against generated Go server: %v\n%s\nserver:\n%s", err, bunOutput, serverOutput())
	}
	if !bytes.Contains(bunOutput, []byte("1 pass")) {
		t.Fatalf("generated TypeScript client proof did not report one pass:\n%s", bunOutput)
	}
}

func TestPrepareAndCompileNativeContractWithLegacyGoBridge(t *testing.T) {
	appRoot := copyVNextBuildFixture(t, "bridge")

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

func copyVNextBuildFixture(t *testing.T, name string) string {
	t.Helper()
	repository := repoRoot(t)
	fixture := filepath.Join(repository, "internal", "vnext", "testdata", name)
	appRoot := t.TempDir()
	if err := copyTree(fixture, appRoot); err != nil {
		t.Fatalf("copy %s fixture: %v", name, err)
	}
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read %s fixture go.mod: %v", name, err)
	}
	updated := []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(repository), 1))
	if bytes.Equal(updated, goMod) {
		t.Fatalf("%s fixture does not contain the expected local scenery replace", name)
	}
	if err := os.WriteFile(goModPath, updated, 0o644); err != nil {
		t.Fatalf("rewrite %s fixture go.mod: %v", name, err)
	}
	return appRoot
}
