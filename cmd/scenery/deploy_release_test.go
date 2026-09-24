package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
)

type deployReleaseTestTarget struct {
	t       *testing.T
	home    string
	secrets *memorySecrets
	store   *appconfig.Store
}

func newDeployReleaseTestTarget(t *testing.T) *deployReleaseTestTarget {
	t.Helper()
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	commandAgentPathsOverride = &paths
	configCatalogOverride = configTestCatalog
	appconfig.DurableFlush = func(*os.File) error { return nil }
	deployStateDurable = false
	t.Cleanup(func() {
		deployStateDurable = true
		commandAgentPathsOverride, configCatalogOverride = nil, nil
		appconfig.DurableFlush = (*os.File).Sync
	})
	store, err := appconfig.OpenStore(home, "clean-tech")
	if err != nil {
		t.Fatal(err)
	}
	return &deployReleaseTestTarget{t: t, home: home, secrets: &memorySecrets{values: map[string][]byte{}}, store: store}
}

func (target *deployReleaseTestTarget) request(operation, id string) deployRemoteResponse {
	target.t.Helper()
	encoded, _ := json.Marshal(deployRemoteRequest{Kind: deployRequestKind, Protocol: deployProtocolVersion, AppID: "clean-tech", Environment: "production", Operation: operation, DeploymentID: id})
	return serveDeployRequest(context.Background(), target.home, bytes.NewReader(encoded), func(*appconfig.Store) (appconfig.SecretBackend, error) { return target.secrets, nil })
}

func (target *deployReleaseTestTarget) set(key string, value string) string {
	target.t.Helper()
	result, err := target.store.Mutate(context.Background(), "production", appconfig.Mutation{Key: key, Value: json.RawMessage(value)})
	if err != nil {
		target.t.Fatal(err)
	}
	return result.Revision
}

func (target *deployReleaseTestTarget) setSecret(key, value string) {
	target.t.Helper()
	version, err := target.secrets.Create(context.Background(), "production", key, []byte(value))
	if err != nil {
		target.t.Fatal(err)
	}
	if _, err := target.store.Mutate(context.Background(), "production", appconfig.Mutation{Key: key, Secret: &version}); err != nil {
		target.t.Fatal(err)
	}
}

// stage writes the release's source as rsync would.
func (target *deployReleaseTestTarget) stage(id, marker string) {
	target.t.Helper()
	layout := deployLayout{home: target.home, appID: "clean-tech", environment: "production"}
	source := filepath.Join(layout.release(id), "source")
	writeTestAppFile(target.t, source, app.PrimaryConfigFilename, `{"name":"clean-tech","id":"clean-tech","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["prod"]}}}}`)
	writeTestAppFile(target.t, source, "release.txt", marker)
	writeTestAppFile(target.t, source, "only-"+marker+".txt", marker)
}

func (target *deployReleaseTestTarget) deploy(id, marker string, succeed bool) deployRemoteResponse {
	target.t.Helper()
	if response := target.request("begin", id); !response.OK {
		target.t.Fatalf("begin %s: %+v", id, response)
	}
	target.stage(id, marker)
	if response := target.request("validate", id); !response.OK {
		target.t.Fatalf("validate %s: %+v", id, response)
	}
	if response := target.request("activate", id); !response.OK {
		target.t.Fatalf("activate %s: %+v", id, response)
	}
	operation := "commit"
	if !succeed {
		operation = "rollback"
	} else {
		target.runActive()
	}
	response := target.request(operation, id)
	if !response.OK {
		target.t.Fatalf("%s %s: %+v", operation, id, response)
	}
	return response
}

// runActive stands in for the stable root's runtime applying the installed
// revision, as its supervisor's pin records.
func (target *deployReleaseTestTarget) runActive() {
	target.t.Helper()
	active := target.active()
	layout := deployLayout{home: target.home, appID: "clean-tech", environment: "production"}
	if err := target.store.PinRecord("production", devConfigHolder(layout.sourceRoot()), appconfig.Pin{Revision: active.ConfigRevision, Desired: active.ConfigRevision, State: "applied"}); err != nil {
		target.t.Fatal(err)
	}
}

func (target *deployReleaseTestTarget) active() *deploymentActiveRecord {
	target.t.Helper()
	record, err := readDeploymentActive(target.home, "clean-tech", "production")
	if err != nil {
		target.t.Fatal(err)
	}
	return record
}

func TestDeployReleasePinsCapturedConfigurationAndRollsBackExactly(t *testing.T) {
	target := newDeployReleaseTestTarget(t)
	target.setSecret("designs.api_token", "token-1")
	first := target.set("designs.simulation_concurrency", "8")
	firstID, secondID := strings.Repeat("1", 32), strings.Repeat("2", 32)

	if response := target.request("begin", firstID); !response.OK || response.ConfigRevision != first {
		t.Fatalf("begin = %+v", response)
	}
	// A desired change after capture does not change what this deployment runs.
	second := target.set("designs.simulation_concurrency", "9")
	target.stage(firstID, "first")
	for _, operation := range []string{"validate", "activate"} {
		if response := target.request(operation, firstID); !response.OK {
			t.Fatalf("%s = %+v", operation, response)
		}
	}
	if response := target.request("commit", firstID); response.OK || !strings.Contains(response.Error, "does not run release") {
		t.Fatalf("commit without a running release = %+v", response)
	}
	target.runActive()
	if response := target.request("commit", firstID); !response.OK {
		t.Fatalf("commit = %+v", response)
	}
	active := target.active()
	if active.State != "active" || active.ConfigRevision != first || active.DeploymentID != firstID {
		t.Fatalf("active = %+v", active)
	}
	layout := deployLayout{home: target.home, appID: "clean-tech", environment: "production"}
	writeTestAppFile(t, layout.sourceRoot(), ".scenery/sessions/state.json", "runtime-owned")
	if data, _ := os.ReadFile(filepath.Join(layout.sourceRoot(), "release.txt")); string(data) != "first" {
		t.Fatalf("stable root = %q", data)
	}
	// Reboot and resume run the active revision, not the newer desired one.
	cfg := app.Config{Name: "clean-tech", ID: "clean-tech", Envs: map[string]app.EnvConfig{"production": {Deploy: &app.EnvDeployConfig{SSH: []string{"prod"}}}}}
	env, _ := cfg.ResolveEnv("production")
	document, err := devConfigDocument(target.store, cfg, env)
	if err != nil || document.Revision != first {
		t.Fatalf("resume configuration = %s %v, want %s (desired %s)", document.Revision, err, first, second)
	}

	// A second release whose activation fails restores the first release's
	// exact source and configuration.
	restored := target.deploy(secondID, "second", false)
	if restored.Previous != firstID || restored.ConfigRevision != first {
		t.Fatalf("rollback = %+v", restored)
	}
	active = target.active()
	if active.DeploymentID != firstID || active.ConfigRevision != first || active.State != "active" {
		t.Fatalf("restored active = %+v", active)
	}
	if data, _ := os.ReadFile(filepath.Join(layout.sourceRoot(), "release.txt")); string(data) != "first" {
		t.Fatalf("restored source = %q", data)
	}
	if _, err := os.Stat(filepath.Join(layout.sourceRoot(), "only-second.txt")); !os.IsNotExist(err) {
		t.Fatalf("second release file survived rollback: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(layout.sourceRoot(), ".scenery/sessions/state.json")); string(data) != "runtime-owned" {
		t.Fatal("release synchronization touched runtime-owned state")
	}
	pins, err := target.store.Pins("production")
	if err != nil || pins["deployment-active"] != first || pins["deployment-"+secondID] != "" {
		t.Fatalf("pins = %v %v", pins, err)
	}
	if _, err := target.store.ReadRevision("production", first); err != nil {
		t.Fatalf("active revision not retained: %v", err)
	}
}

func TestDeployReleaseRejectsInvalidCandidatesWithoutTouchingTheRuntime(t *testing.T) {
	target := newDeployReleaseTestTarget(t)
	id := strings.Repeat("3", 32)
	target.set("designs.simulation_concurrency", "8")
	if response := target.request("begin", id); !response.OK {
		t.Fatalf("begin = %+v", response)
	}
	target.stage(id, "candidate")
	response := target.request("validate", id)
	if response.OK || len(response.Problems) != 1 || !strings.Contains(response.Problems[0], "designs.api_token") {
		t.Fatalf("validate = %+v", response)
	}
	if response := target.request("activate", id); response.OK {
		t.Fatal("an unvalidated release was activated")
	}
	if response := target.request("abort", id); !response.OK {
		t.Fatalf("abort = %+v", response)
	}
	if target.active() != nil {
		t.Fatal("an invalid candidate became installed")
	}
	if pins, _ := target.store.Pins("production"); len(pins) != 0 {
		t.Fatalf("aborted release left pins: %v", pins)
	}
}

func TestDeployAndConfigurationRefuseALegacyCheckoutInTheStore(t *testing.T) {
	target := newDeployReleaseTestTarget(t)
	writeTestAppFile(t, filepath.Join(target.home, "apps", "clean-tech"), app.PrimaryConfigFilename, `{"name":"clean-tech"}`)
	if response := target.request("begin", strings.Repeat("4", 32)); response.OK || !strings.Contains(response.Error, "deploy-root-migration") {
		t.Fatalf("begin over a legacy checkout = %+v", response)
	}
	encoded, _ := json.Marshal(configRemoteRequest{Kind: configRequestKind, Protocol: configProtocolVersion, AppID: "clean-tech", Environment: "production", Operation: "set", OperationID: strings.Repeat("a", 32), Key: "designs.simulation_concurrency", Value: json.RawMessage("8")})
	if response := serveConfigRequest(context.Background(), target.home, bytes.NewReader(encoded), nil); response.OK || !strings.Contains(response.Error, "legacy deployment checkout") {
		t.Fatalf("config set over a legacy checkout = %+v", response)
	}
}
