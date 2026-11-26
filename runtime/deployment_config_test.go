package runtime

import (
	"encoding/json"
	"sync"
	"testing"

	"scenery.sh/internal/contract"
)

func setConfigSnapshotForTest(t *testing.T, snapshot *ConfigSnapshot) {
	t.Helper()
	previous := configSnapshotState.snapshot
	configSnapshotState.once = sync.Once{}
	configSnapshotState.once.Do(func() {})
	configSnapshotState.snapshot, configSnapshotState.err = snapshot, nil
	t.Cleanup(func() {
		configSnapshotState.once = sync.Once{}
		configSnapshotState.once.Do(func() {})
		configSnapshotState.snapshot = previous
	})
}

func TestDeploymentConfigResolvesTypedValuesAndSecrets(t *testing.T) {
	setConfigSnapshotForTest(t, &ConfigSnapshot{Kind: ConfigSnapshotKind, AppID: "clean-tech", Environment: "local", Revision: "cfg-1",
		Values:  map[string]json.RawMessage{"designs.simulation_concurrency": json.RawMessage("8"), "designs.weather_pack_root": json.RawMessage("null"), "maps.key": json.RawMessage(`"browser-key"`)},
		Secrets: map[string][]byte{"designs.api_token": []byte("token")},
		Public:  []string{"maps.key"},
	})
	var concurrency uint32
	if err := ResolveDeploymentConfig("designs.simulation_concurrency", &concurrency, "uint32", false); err != nil || concurrency != 8 {
		t.Fatalf("uint32 = %d %v", concurrency, err)
	}
	var root contract.Optional[contract.HostPath]
	if err := ResolveDeploymentConfig("designs.weather_pack_root", &root, "optional(host_path)", true); err != nil || root.Set {
		t.Fatalf("configured absence = %+v %v", root, err)
	}
	var missing string
	if err := ResolveDeploymentConfig("designs.model_path", &missing, "host_path", false); err == nil {
		t.Fatal("a missing required input resolved")
	}
	if value, err := ConfigSecretRef("designs.api_token").Reveal(); err != nil || string(value) != "token" {
		t.Fatalf("secret = %q %v", value, err)
	}
	if _, err := ConfigSecretRef("designs.unset").Reveal(); err == nil {
		t.Fatal("an unconfigured secret revealed")
	}
}
