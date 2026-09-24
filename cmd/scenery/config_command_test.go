package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/graph"
)

// memorySecrets is an in-memory secret backend for tests.
type memorySecrets struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (m *memorySecrets) Name() string                { return "memory" }
func (m *memorySecrets) Ready(context.Context) error { return nil }
func (m *memorySecrets) Create(_ context.Context, environment, key string, value []byte) (appconfig.SecretVersion, error) {
	version, err := appconfig.NewSecretVersion("memory")
	if err != nil {
		return version, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[environment+"|"+key+"|"+version.Version] = append([]byte(nil), value...)
	return version, nil
}
func (m *memorySecrets) Resolve(_ context.Context, environment, key string, version appconfig.SecretVersion) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[environment+"|"+key+"|"+version.Version]
	if !ok {
		return nil, appconfig.ErrSecretBackendUnavailable
	}
	return value, nil
}
func (m *memorySecrets) Remove(_ context.Context, environment, key string, version appconfig.SecretVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, environment+"|"+key+"|"+version.Version)
	return nil
}
func (m *memorySecrets) Versions(_ context.Context, environment string) (map[string][]appconfig.SecretVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string][]appconfig.SecretVersion{}
	for id := range m.values {
		parts := strings.Split(id, "|")
		if parts[0] == environment {
			result[parts[1]] = append(result[parts[1]], appconfig.SecretVersion{Backend: "memory", Version: parts[2]})
		}
	}
	return result, nil
}

// receiverTransport serves requests with an in-process receiver over a
// separate target home, as the SSH transport would on the target.
type receiverTransport struct {
	home    string
	secrets *memorySecrets
	down    bool
	calls   int
}

func (r *receiverTransport) Exchange(ctx context.Context, target string, request []byte) ([]byte, error) {
	r.calls++
	if r.down {
		return nil, unavailableErrorf("configuration target %s is unreachable over SSH: connection refused; no local value was used", target)
	}
	response := serveConfigRequest(ctx, r.home, bytes.NewReader(request), func(*appconfig.Store) (appconfig.SecretBackend, error) { return r.secrets, nil })
	return json.Marshal(response)
}

type configTestEnv struct {
	root, home string
	local      *memorySecrets
	target     *receiverTransport
}

func configTestCatalog(root string, cfg app.Config) (appconfig.Catalog, error) {
	manifest := &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/module/designs", Kind: "scenery.module", Name: "designs", Module: "app", Spec: map[string]any{
			"inputs": map[string]any{"simulation_concurrency": map[string]any{"$scalar": "int", "value": "2"}},
			"interface_inputs": map[string]any{
				"simulation_concurrency": map[string]any{"type": map[string]any{"$ref": "uint32"}, "phase": "deployment", "minimum": map[string]any{"$scalar": "int", "value": "1"}},
				"weather_pack_root":      map[string]any{"type": map[string]any{"$expression": "optional(host_path)"}, "phase": "deployment"},
				"api_token":              map[string]any{"type": map[string]any{"$expression": `resource_ref("secret")`}, "phase": "deployment", "sensitive": true},
			},
		}},
		{Address: "designs/service/designs", Kind: "scenery.service", Name: "designs", Module: "designs", Spec: map[string]any{
			"config_schema": []any{
				map[string]any{"name": "simulation_concurrency", "input": "simulation_concurrency", "type": "uint32", "phase": "deployment"},
				map[string]any{"name": "weather_pack_root", "input": "weather_pack_root", "type": "optional(host_path)", "phase": "deployment"},
				map[string]any{"name": "api_token", "input": "api_token", "type": `resource_ref("secret")`, "phase": "deployment", "sensitive": true},
			},
		}},
	}}
	return appconfig.BuildCatalog(manifest, configFrameworkOptions(cfg))
}

func newConfigTestEnv(t *testing.T) *configTestEnv {
	t.Helper()
	env := &configTestEnv{root: t.TempDir(), home: t.TempDir(), local: &memorySecrets{values: map[string][]byte{}}}
	env.target = &receiverTransport{home: t.TempDir(), secrets: &memorySecrets{values: map[string][]byte{}}}
	config := `{"name":"clean-tech","id":"clean-tech","envs":{"local":{"default":true},"devtools":{},"production":{"deploy":{"ssh":["prod-host"]}}}}`
	if err := os.WriteFile(filepath.Join(env.root, app.PrimaryConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := localagent.PathsForHome(env.home)
	commandAgentPathsOverride = &paths
	configCatalogOverride = configTestCatalog
	configSecretBackendOverride = func(*appconfig.Store) (appconfig.SecretBackend, error) { return env.local, nil }
	configRemoteTransportOverride = env.target
	t.Cleanup(func() {
		commandAgentPathsOverride, configCatalogOverride, configSecretBackendOverride, configRemoteTransportOverride = nil, nil, nil, nil
		configStdin = os.Stdin
	})
	return env
}

func (e *configTestEnv) run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	configStdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	args = append(args, "--app-root", e.root)
	var err error
	switch args[0] {
	case "show":
		err = configShowCommand(&stdout, args[1:])
	case "set":
		err = configSetCommand(&stdout, args[1:])
	case "unset":
		err = configUnsetCommand(&stdout, args[1:])
	}
	return stdout.String(), err
}

func decodeConfigShow(t *testing.T, output string) configShowResult {
	t.Helper()
	var result configShowResult
	if err := decodeCLIJSON([]byte(output), &result); err != nil {
		t.Fatalf("decode %s: %v", output, err)
	}
	return result
}

func showInput(result configShowResult, key string) configShowInput {
	for _, input := range result.Inputs {
		if input.Key == key {
			return input
		}
	}
	return configShowInput{}
}

func TestConfigSetShowUnsetLocal(t *testing.T) {
	env := newConfigTestEnv(t)
	if _, err := env.run(t, "", "set", "designs.weather_pack_root", "/Volumes/Drive01/PSM", "--env", "local"); err != nil {
		t.Fatal(err)
	}
	output, err := env.run(t, "", "show", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	shown := decodeConfigShow(t, output)
	weather := showInput(shown, "designs.weather_pack_root")
	if shown.Environment != "local" || shown.Authority != "local" || weather.State != "configured" || string(weather.Value) != `"/Volumes/Drive01/PSM"` {
		t.Fatalf("show = %+v", shown)
	}
	if concurrency := showInput(shown, "designs.simulation_concurrency"); concurrency.Source != "default" || string(concurrency.Value) != "2" {
		t.Fatalf("default = %+v", concurrency)
	}
	if other, _, err := mustStore(t, env).Read("devtools"); err != nil || len(other.Values) != 0 {
		t.Fatalf("devtools inherited local values: %+v %v", other, err)
	}
	if _, err := env.run(t, "", "unset", "designs.weather_pack_root", "--env", "local"); err != nil {
		t.Fatal(err)
	}
	output, _ = env.run(t, "", "show", "designs.weather_pack_root", "-o", "json")
	if weather := showInput(decodeConfigShow(t, output), "designs.weather_pack_root"); weather.State != "absent" {
		t.Fatalf("unset did not restore absence: %+v", weather)
	}
}

func TestConfigRejectsAmbiguousAndUntypedWrites(t *testing.T) {
	env := newConfigTestEnv(t)
	for _, args := range [][]string{
		{"set", "designs.simulation_concurrency", "8"},
		{"set", "designs.simulation_concurrency", "0", "--env", "local"},
		{"set", "designs.simulation_concurrency", "eight", "--env", "local"},
		{"set", "designs.unknown", "1", "--env", "local"},
		{"set", "designs.simulation_concurrency", "8", "--env", "local", "--scope", "worktree"},
		{"set", "designs.simulation_concurrency", "8", "--env", "local", "--null"},
		{"set", "designs.simulation_concurrency", "--env", "local", "--null"},
		{"set", "designs.api_token", "plaintext-token", "--env", "local"},
		{"unset", "designs.weather_pack_root"},
		{"set", "designs.weather_pack_root", "relative/path", "--env", "local"},
	} {
		if _, err := env.run(t, "", args...); err == nil || cliExitCode(err) != 2 {
			t.Fatalf("%v: err=%v code=%d", args, err, cliExitCode(err))
		} else if strings.Contains(err.Error(), "plaintext-token") {
			t.Fatalf("%v: diagnostic echoes the secret", args)
		}
	}
	if document, exists, err := mustStore(t, env).Read("local"); err != nil || exists || len(document.Values) != 0 {
		t.Fatalf("rejected writes changed the store: %+v %v %v", document, exists, err)
	}
	// A secret without --stdin and without a terminal fails instead of waiting.
	if _, err := env.run(t, "", "set", "designs.api_token", "--env", "local"); err == nil || !strings.Contains(err.Error(), "--stdin") {
		t.Fatalf("secret without terminal = %v", err)
	}
}

func TestConfigSecretStoresExactBytesAndNeverShowsThem(t *testing.T) {
	env := newConfigTestEnv(t)
	secret := "s3cr3t value\n"
	output, err := env.run(t, secret, "set", "designs.api_token", "--env", "local", "--stdin", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "s3cr3t") {
		t.Fatal("change output contains the secret")
	}
	document, _, err := mustStore(t, env).Read("local")
	if err != nil || len(document.Secrets) != 1 || len(document.Values) != 0 {
		t.Fatalf("document = %+v %v", document, err)
	}
	version := document.Secrets["designs.api_token"]
	stored, err := env.local.Resolve(context.Background(), "local", "designs.api_token", version)
	if err != nil || string(stored) != secret {
		t.Fatalf("stored secret = %q %v", stored, err)
	}
	raw, _ := os.ReadFile(filepath.Join(env.home, "apps", "clean-tech", "environments", "local.json"))
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatal("environment document contains the secret")
	}
	output, err = env.run(t, "", "show", "-o", "json")
	if err != nil || strings.Contains(output, "s3cr3t") || strings.Contains(output, version.Version) {
		t.Fatalf("show leaks secret material: %v", err)
	}
	if token := showInput(decodeConfigShow(t, output), "designs.api_token"); token.SecretNote != "configured" || token.Value != nil {
		t.Fatalf("secret input = %+v", token)
	}
}

func TestConfigDeployableEnvironmentUsesItsTarget(t *testing.T) {
	env := newConfigTestEnv(t)
	output, err := env.run(t, "", "set", "designs.simulation_concurrency", "8", "--env", "production", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var change configChangeResult
	if err := decodeCLIJSON([]byte(output), &change); err != nil || change.Authority != "target" || change.Target != "prod-host" || !change.Changed {
		t.Fatalf("change = %+v %v", change, err)
	}
	targetStore, _ := appconfig.OpenStore(env.target.home, "clean-tech")
	if document, _, err := targetStore.Read("production"); err != nil || string(document.Values["designs.simulation_concurrency"]) != "8" {
		t.Fatalf("target document = %+v %v", document, err)
	}
	if document, exists, _ := mustStore(t, env).Read("production"); exists || len(document.Values) != 0 {
		t.Fatal("production value was written on the workstation")
	}
	if _, err := env.run(t, "prod-token", "set", "designs.api_token", "--env", "production", "--stdin"); err != nil {
		t.Fatal(err)
	}
	output, err = env.run(t, "", "show", "--env", "production", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	shown := decodeConfigShow(t, output)
	if shown.Authority != "target" || showInput(shown, "designs.api_token").SecretNote != "configured" || strings.Contains(output, "prod-token") {
		t.Fatalf("remote show = %s", output)
	}
	env.target.down = true
	before := env.target.calls
	if _, err := env.run(t, "", "set", "designs.simulation_concurrency", "9", "--env", "production"); err == nil || cliExitCode(err) != 4 {
		t.Fatalf("unreachable target = %v", err)
	}
	if _, err := env.run(t, "", "show", "--env", "production"); err == nil {
		t.Fatal("show fell back without its target")
	}
	if env.target.calls != before+2 {
		t.Fatalf("target calls = %d", env.target.calls-before)
	}
	if _, exists, _ := mustStore(t, env).Read("production"); exists {
		t.Fatal("unreachable target caused a local write")
	}
}

func TestConfigExpectRevisionAndUnusedKeys(t *testing.T) {
	env := newConfigTestEnv(t)
	store := mustStore(t, env)
	if _, err := store.Mutate(context.Background(), "local", appconfig.Mutation{Key: "designs.future_input", Value: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	stale, _, _ := store.Read("local")
	if _, err := env.run(t, "", "set", "designs.simulation_concurrency", "4", "--env", "local", "--expect-revision", stale.Revision); err != nil {
		t.Fatal(err)
	}
	_, err := env.run(t, "", "set", "designs.simulation_concurrency", "5", "--env", "local", "--expect-revision", stale.Revision)
	var coded *codedCLIError
	if err == nil || !errors.As(err, &coded) || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("stale expected revision = %v", err)
	}
	output, err := env.run(t, "", "show", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	shown := decodeConfigShow(t, output)
	if strings.Join(shown.Unused, ",") != "designs.future_input" || strings.Contains(output, `"designs.future_input":true`) {
		t.Fatalf("unused = %+v", shown.Unused)
	}
	if concurrency := showInput(shown, "designs.simulation_concurrency"); string(concurrency.Value) != "4" {
		t.Fatalf("stale write mutated: %+v", concurrency)
	}
}

func mustStore(t *testing.T, env *configTestEnv) *appconfig.Store {
	t.Helper()
	store, err := appconfig.OpenStore(env.home, "clean-tech")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
