package appconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/graph"
)

// testManifest is a compiled-manifest projection of one module instance
// "designs" with three configurable inputs and one wiring input.
func testManifest() *graph.Manifest {
	return &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/module/designs", Kind: "scenery.module", Name: "designs", Module: "app", Spec: map[string]any{
			"inputs": map[string]any{"simulation_concurrency": map[string]any{"$scalar": "int", "value": "2"}},
			"interface_inputs": map[string]any{
				"database":               map[string]any{"type": map[string]any{"$expression": `resource_ref("data_source")`}, "phase": "deployment"},
				"simulation_concurrency": map[string]any{"type": map[string]any{"$ref": "uint32"}, "phase": "deployment", "minimum": map[string]any{"$scalar": "int", "value": "1"}},
				"weather_pack_root":      map[string]any{"type": map[string]any{"$expression": "optional(host_path)"}, "phase": "deployment"},
				"model_path":             map[string]any{"type": map[string]any{"$ref": "host_path"}, "phase": "deployment"},
				"api_token":              map[string]any{"type": map[string]any{"$expression": `resource_ref("secret")`}, "phase": "deployment", "sensitive": true},
				"page_size":              map[string]any{"type": map[string]any{"$ref": "uint32"}, "default": map[string]any{"$scalar": "int", "value": "50"}},
			},
		}},
		{Address: "designs/service/designs", Kind: "scenery.service", Name: "designs", Module: "designs", Spec: map[string]any{
			"config_schema": []any{
				map[string]any{"name": "concurrency", "input": "simulation_concurrency", "type": "uint32", "phase": "deployment", "sensitive": false},
				map[string]any{"name": "weather_pack_root", "input": "weather_pack_root", "type": "optional(host_path)", "phase": "deployment", "sensitive": false},
				map[string]any{"name": "api_token", "input": "api_token", "type": `resource_ref("secret")`, "phase": "deployment", "sensitive": true},
			},
		}},
	}}
}

func testCatalog(t *testing.T) Catalog {
	t.Helper()
	catalog, err := BuildCatalog(testManifest(), FrameworkOptions{StandardAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestCatalogDerivesConfigurableInputs(t *testing.T) {
	catalog := testCatalog(t)
	var keys []string
	for _, input := range catalog.Inputs {
		keys = append(keys, input.Key)
	}
	want := "auth.cookie_domain,auth.email_from,auth.jwt_secret,designs.api_token,designs.model_path,designs.simulation_concurrency,designs.weather_pack_root"
	if strings.Join(keys, ",") != want {
		t.Fatalf("catalog keys = %s", strings.Join(keys, ","))
	}
	concurrency, _ := catalog.Lookup("designs.simulation_concurrency")
	if string(concurrency.Default) != "2" || concurrency.Optional || concurrency.Constraints["minimum"] != "1" || len(concurrency.Consumers) != 1 || concurrency.Consumers[0] != (Consumer{Service: "designs/service/designs", Field: "concurrency"}) {
		t.Fatalf("simulation_concurrency = %#v", concurrency)
	}
	weather, _ := catalog.Lookup("designs.weather_pack_root")
	if !weather.Optional || weather.Default != nil || weather.Required(true) {
		t.Fatalf("weather_pack_root = %#v", weather)
	}
	model, _ := catalog.Lookup("designs.model_path")
	if !model.Required(false) {
		t.Fatalf("model_path must be required: %#v", model)
	}
	token, _ := catalog.Lookup("designs.api_token")
	if !token.Sensitive || token.Default != nil {
		t.Fatalf("api_token = %#v", token)
	}
	jwt, _ := catalog.Lookup("auth.jwt_secret")
	if jwt.Required(false) || !jwt.Required(true) {
		t.Fatalf("auth.jwt_secret requiredness = %#v", jwt)
	}
	again, err := BuildCatalog(testManifest(), FrameworkOptions{StandardAuth: true})
	if err != nil || again.Revision != catalog.Revision {
		t.Fatalf("catalog revision is not deterministic: %v %s %s", err, again.Revision, catalog.Revision)
	}
}

func TestParseTextIsTyped(t *testing.T) {
	catalog := testCatalog(t)
	concurrency, _ := catalog.Lookup("designs.simulation_concurrency")
	if value, err := ParseText(concurrency, "8"); err != nil || string(value) != "8" {
		t.Fatalf("uint32 8 = %s %v", value, err)
	}
	for _, text := range []string{"0", "-1", "eight", " 8", "8.5"} {
		if _, err := ParseText(concurrency, text); err == nil {
			t.Fatalf("uint32 %q accepted", text)
		}
	}
	weather, _ := catalog.Lookup("designs.weather_pack_root")
	if value, err := ParseText(weather, "/Volumes/Drive01/PSM"); err != nil || string(value) != `"/Volumes/Drive01/PSM"` {
		t.Fatalf("host path = %s %v", value, err)
	}
	for _, text := range []string{"relative/packs", "/a/../b", "~/packs", ""} {
		if _, err := ParseText(weather, text); err == nil {
			t.Fatalf("host path %q accepted", text)
		}
	}
	if value, err := CanonicalValue(weather, json.RawMessage("null")); err != nil || string(value) != "null" {
		t.Fatalf("optional null = %s %v", value, err)
	}
	if _, err := CanonicalValue(concurrency, json.RawMessage("null")); err == nil {
		t.Fatal("required null accepted")
	}
	token, _ := catalog.Lookup("designs.api_token")
	if _, err := ParseText(token, "plaintext"); err == nil || strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("secret argument = %v", err)
	}
}

func TestResolveAppliesDefaultsThenEnvironment(t *testing.T) {
	catalog := testCatalog(t)
	document := NewDocument("clean-tech", "local")
	document.Values["designs.weather_pack_root"] = json.RawMessage(`"/Volumes/Drive01/PSM"`)
	document.Values["designs.future_input"] = json.RawMessage(`true`)
	document.Secrets["auth.jwt_secret"] = SecretVersion{Backend: "keychain", Version: strings.Repeat("a", 32)}
	document.Revision = document.contentRevision()
	resolution := Resolve(catalog, document, false)
	check := func(key, state, source, value string) {
		t.Helper()
		entry, ok := resolution.Entry(key)
		if !ok || entry.State != state || entry.Source != source || string(entry.Value) != value {
			t.Fatalf("%s = %#v", key, entry)
		}
	}
	check("designs.simulation_concurrency", StateDefault, SourceDefault, "2")
	check("designs.weather_pack_root", StateConfigured, SourceEnvironment, `"/Volumes/Drive01/PSM"`)
	check("designs.model_path", StateMissing, SourceNone, "")
	check("auth.cookie_domain", StateAbsent, SourceNone, "")
	if entry, _ := resolution.Entry("auth.jwt_secret"); entry.Secret == nil || entry.State != StateConfigured || entry.Value != nil {
		t.Fatalf("secret entry = %#v", entry)
	}
	if strings.Join(resolution.Unused, ",") != "designs.future_input" || resolution.Valid() {
		t.Fatalf("unused=%v valid=%v", resolution.Unused, resolution.Valid())
	}
	document.Values["designs.model_path"] = json.RawMessage(`"/srv/models"`)
	document.Values["designs.simulation_concurrency"] = json.RawMessage(`"eight"`)
	resolution = Resolve(catalog, document, false)
	check("designs.simulation_concurrency", StateInvalid, SourceEnvironment, "")
	if resolution.Valid() {
		t.Fatal("an incompatible known type must fail candidate validation")
	}
	encoded, _ := json.Marshal(resolution)
	if strings.Contains(string(encoded), strings.Repeat("a", 32)) {
		t.Fatal("resolution JSON exposes a secret version")
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir(), "clean-tech")
	if err != nil {
		t.Fatal(err)
	}
	store.flush = func(*os.File) error { return nil }
	return store
}

func TestStoreMutatesOneKeyAtomically(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	empty, exists, err := store.Read("local")
	if err != nil || exists || len(empty.Values) != 0 {
		t.Fatalf("empty read = %#v %v %v", empty, exists, err)
	}
	first, err := store.Mutate(ctx, "local", Mutation{Key: "designs.weather_pack_root", Value: json.RawMessage(`"/Volumes/Drive01/PSM"`)})
	if err != nil || !first.Changed || first.PreviousRevision != empty.Revision {
		t.Fatalf("first mutation = %#v %v", first, err)
	}
	same, err := store.Mutate(ctx, "local", Mutation{Key: "designs.weather_pack_root", Value: json.RawMessage(`"/Volumes/Drive01/PSM"`)})
	if err != nil || same.Changed || same.Revision != first.Revision {
		t.Fatalf("no-op mutation = %#v %v", same, err)
	}
	if absent, err := store.Mutate(ctx, "local", Mutation{Key: "designs.other", Unset: true}); err != nil || absent.Changed {
		t.Fatalf("unset of an absent key = %#v %v", absent, err)
	}
	if _, err := store.Mutate(ctx, "local", Mutation{Key: "designs.simulation_concurrency", Value: json.RawMessage(`8`), ExpectRevision: empty.Revision}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale expected revision = %v", err)
	}
	current, _, err := store.Read("local")
	if err != nil || current.Revision != first.Revision || len(current.Values) != 1 {
		t.Fatalf("conflict mutated the store: %#v %v", current, err)
	}
	retained, err := store.ReadRevision("local", first.Revision)
	if err != nil || string(retained.Values["designs.weather_pack_root"]) != `"/Volumes/Drive01/PSM"` {
		t.Fatalf("history = %#v %v", retained, err)
	}
	if other, _, err := store.Read("production"); err != nil || len(other.Values) != 0 {
		t.Fatalf("environments share values: %#v %v", other, err)
	}
	info, err := os.Stat(filepath.Join(store.Dir(), "environments", "local.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("document mode = %v %v", info, err)
	}
}

func TestStoreKeepsConcurrentDifferentKeyWrites(t *testing.T) {
	store := testStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Mutate(context.Background(), "local", Mutation{Key: fmt.Sprintf("designs.key_%02d", index), Value: json.RawMessage(fmt.Sprint(index))})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	document, _, err := store.Read("local")
	if err != nil || len(document.Values) != 8 {
		t.Fatalf("concurrent writes lost updates: %d %v", len(document.Values), err)
	}
}

func TestStoreRejectsCorruptAndUnsafeState(t *testing.T) {
	store := testStore(t)
	if _, err := store.Mutate(context.Background(), "local", Mutation{Key: "designs.a", Value: json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(store.Dir(), "environments", "local.json")
	valid, _ := os.ReadFile(documentPath)
	for name, content := range map[string]string{
		"truncated": string(valid[:len(valid)/2]),
		"tampered":  strings.Replace(string(valid), `"designs.a": 1`, `"designs.a": 2`, 1),
		"duplicate": strings.Replace(string(valid), `"designs.a": 1`, `"designs.a": 1, "designs.a": 2`, 1),
	} {
		if err := os.WriteFile(documentPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Read("local"); err == nil {
			t.Fatalf("%s document was accepted", name)
		}
		if _, err := store.Mutate(context.Background(), "local", Mutation{Key: "designs.b", Value: json.RawMessage(`1`)}); err == nil {
			t.Fatalf("mutation over a %s document succeeded", name)
		}
	}
	if err := os.Remove(documentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.json"), documentPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read("local"); err == nil {
		t.Fatal("symlinked document was followed")
	}
	for _, environment := range []string{"../production", "Local", "", "a/b"} {
		if _, _, err := store.Read(environment); err == nil {
			t.Fatalf("environment %q accepted", environment)
		}
	}
	if _, err := OpenStore(t.TempDir(), "../clean-tech"); err == nil {
		t.Fatal("unsafe application id accepted")
	}
	if err := os.Chmod(store.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read("production"); err == nil {
		t.Fatal("group-readable store was read")
	}
}

func TestStorePruneKeepsPinsAndReportsOrphanSecrets(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	backendVersions := []SecretVersion{}
	var pinned string
	base := time.Now().Add(-time.Hour)
	for index := range RetainedUnpinnedRevisions + 6 {
		version, err := NewSecretVersion("file")
		if err != nil {
			t.Fatal(err)
		}
		backendVersions = append(backendVersions, version)
		result, err := store.Mutate(ctx, "production", Mutation{Key: "auth.jwt_secret", Secret: &version})
		if err != nil {
			t.Fatal(err)
		}
		history := filepath.Join(store.Dir(), "environment-history", "production", result.Revision+".json")
		stamp := base.Add(time.Duration(index) * time.Second)
		if err := os.Chtimes(history, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			pinned = result.Revision
			if err := store.Pin("production", "deployment-active", pinned); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := store.Prune(ctx, "production", map[string][]SecretVersion{"auth.jwt_secret": backendVersions})
	if err != nil {
		t.Fatal(err)
	}
	// 22 revisions: desired + pinned first + 16 newest unpinned survive; 4 go.
	if len(result.RemovedRevisions) != 4 {
		t.Fatalf("removed %d revisions: %v", len(result.RemovedRevisions), result.RemovedRevisions)
	}
	if _, err := store.ReadRevision("production", pinned); err != nil {
		t.Fatalf("pinned revision pruned: %v", err)
	}
	orphans := result.OrphanSecrets["auth.jwt_secret"]
	if len(orphans) != 4 {
		t.Fatalf("orphan secret versions = %d", len(orphans))
	}
	for _, orphan := range orphans {
		if orphan == backendVersions[0] || orphan == backendVersions[len(backendVersions)-1] {
			t.Fatalf("live secret version reported as orphan: %v", orphan)
		}
	}
	if err := store.Unpin("production", "deployment-active"); err != nil {
		t.Fatal(err)
	}
	if pins, err := store.Pins("production"); err != nil || len(pins) != 0 {
		t.Fatalf("pins after unpin = %v %v", pins, err)
	}
}
