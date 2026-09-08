package edge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func TestDNSMasqConfigUsesWildcardDevDomain(t *testing.T) {
	t.Parallel()

	config := DNSMasqConfig([]string{"local.dev"}, "127.0.0.1:53535", "127.0.0.1")
	for _, want := range []string{
		"bind-interfaces",
		"listen-address=127.0.0.1",
		"port=53535",
		"address=/local.dev/127.0.0.1",
		"no-resolv",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("dnsmasq config missing %q:\n%s", want, config)
		}
	}
}

func TestDNSMasqConfigSupportsMultipleDomains(t *testing.T) {
	t.Parallel()

	config := DNSMasqConfig([]string{"onlv.dev", "local.dev", "onlv.dev"}, "127.0.0.1:53535", "127.0.0.1")
	for _, want := range []string{
		"address=/local.dev/127.0.0.1",
		"address=/onlv.dev/127.0.0.1",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("dnsmasq config missing %q:\n%s", want, config)
		}
	}
	if strings.Count(config, "address=/onlv.dev/127.0.0.1") != 1 {
		t.Fatalf("dnsmasq config should de-duplicate domains:\n%s", config)
	}
}

func TestDNSConfigServesDomain(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dnsmasq.conf")
	if err := os.WriteFile(path, []byte(DNSMasqConfig([]string{"local.dev", "onlv.dev"}, "127.0.0.1:53535", "127.0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	if !DNSConfigServesDomain(path, "onlv.dev") {
		t.Fatal("expected config to serve onlv.dev")
	}
	if DNSConfigServesDomain(path, "other.dev") {
		t.Fatal("did not expect config to serve other.dev")
	}
}

func TestDNSResolverFile(t *testing.T) {
	t.Parallel()

	got := DNSResolverFile("local.dev", "127.0.0.1", "53535")
	for _, want := range []string{
		"Managed by scenery edge dns",
		"domain local.dev",
		"nameserver 127.0.0.1",
		"port 53535",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolver file missing %q:\n%s", want, got)
		}
	}
}

func TestLoadDNSStateMigratesResolverOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	legacy := []byte(`{"schema_version":"scenery.edge.dns.state.v1","status":"running","pid":42,"domain":"local.dev","listen":"127.0.0.1:53535","address":"127.0.0.1","resolver_path":"/etc/resolver/local.dev","updated_at":"2026-07-13T00:00:00Z"}`)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &fields); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyDNSState(fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["schema_version"]; exists {
		t.Fatal("legacy schema version survived conversion")
	}
	converted, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var current DNSState
	if err := json.Unmarshal(converted, &current); err != nil {
		t.Fatal(err)
	}
	current.ArtifactIdentity = machine.NewArtifactIdentity("scenery.edge.dns-state", dnsStateDescriptor)
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := LoadDNSState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "scenery.edge.dns-state" || state.PID != 42 || state.ResolverPath != "/etc/resolver/local.dev" {
		t.Fatalf("migrated state = %+v", state)
	}
	if _, err := os.Stat(path + ".legacy.bak"); !os.IsNotExist(err) {
		t.Fatalf("current state read unexpectedly created a migration backup: %v", err)
	}
}
