package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := LoadDNSState(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "scenery.edge.dns-state" || state.PID != 42 || state.ResolverPath != "/etc/resolver/local.dev" {
		t.Fatalf("migrated state = %+v", state)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}
