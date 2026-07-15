package edge

import (
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestCaddyConfigUsesStableAgentRouterContract(t *testing.T) {
	t.Parallel()

	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		PublicPort:  "443",
		Upstream:    "127.0.0.1:9440",
		AskURL:      "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
	})
	for _, want := range []string{
		"default_bind 127.0.0.1",
		"auto_https disable_redirects",
		"local_certs",
		"ask http://127.0.0.1:9440/v1/tls/allow",
		"admin unix///tmp/scenery-caddy.sock",
		"strict_sni_host on",
		"https://:19443 {",
		"reverse_proxy 127.0.0.1:9440",
		"flush_interval -1",
		"lb_try_duration 5s",
		"lb_try_interval 250ms",
		"header_up Host {host}",
		"header_up X-Forwarded-Proto https",
		"header_up X-Forwarded-Port 443",
		"header_up X-Scenery-Edge-Token secret-token",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("Caddy config missing %q:\n%s", want, config)
		}
	}
}

func TestCaddyConfigUsesPrivateListenPortAndPublicForwardedPort(t *testing.T) {
	t.Parallel()

	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19555",
		PublicPort:  "443",
		Upstream:    "127.0.0.1:9440",
		AskURL:      "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
	})
	for _, want := range []string{
		"default_bind 127.0.0.1",
		"https://:19555 {",
		"header_up X-Forwarded-Port 443",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("private listener config missing %q:\n%s", want, config)
		}
	}
}

func TestCaddyConfigAddsPublicACMESites(t *testing.T) {
	t.Parallel()

	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:     "127.0.0.1:19443",
		PublicPort:     "443",
		Upstream:       "127.0.0.1:9440",
		AskURL:         "http://127.0.0.1:9440/v1/tls/allow",
		AdminSocket:    "/tmp/scenery-caddy.sock",
		Token:          "secret-token",
		PublicDomains:  []PublicDomainSite{{Domain: "z.onlv.dev"}, {Domain: "onlv.dev"}, {Domain: "onlv.dev"}},
		ACMEEmail:      "ops@example.com",
		ACMECA:         "staging",
		StorageDir:     "/tmp/scenery-caddy-data",
		HTTPListenPort: "19080",
	})
	for _, want := range []string{
		"storage file_system /tmp/scenery-caddy-data",
		"email ops@example.com",
		"http_port 19080",
		"https_port 19443",
		"onlv.dev:19443 {",
		"z.onlv.dev:19443 {",
		"issuer acme {",
		"ca https://acme-staging-v02.api.letsencrypt.org/directory",
		"header_up X-Scenery-Public-Edge 1",
		"http://onlv.dev:19080 {",
		"redir https://{host}{uri} 308",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("public Caddy config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "local_certs") {
		t.Fatalf("public Caddy config should keep internal issuer per-site, not global local_certs:\n%s", config)
	}
	if strings.Count(config, "\nonlv.dev:19443 {") != 1 {
		t.Fatalf("public Caddy config should de-duplicate domains:\n%s", config)
	}
	if strings.Index(config, "onlv.dev:19443 {") > strings.Index(config, "z.onlv.dev:19443 {") {
		t.Fatalf("public Caddy domains should be sorted:\n%s", config)
	}
}

func TestPublicDomainSitesForDeployRegistryUsesEnabledTargets(t *testing.T) {
	t.Parallel()

	sites := publicDomainSitesForDeployRegistry(localagent.DeployRegistry{
		Targets: []localagent.DeployTarget{
			{Domain: "z.onlv.dev", Enabled: true},
			{Domain: "off.onlv.dev", Enabled: false},
			{Domain: "onlv.dev", Enabled: true},
			{Domain: "onlv.dev", Enabled: true},
		},
	})
	if len(sites) != 2 || sites[0].Domain != "onlv.dev" || sites[1].Domain != "z.onlv.dev" {
		t.Fatalf("sites = %+v", sites)
	}
}

func TestCaddyConfigForRegistryUsesDeployTargets(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	registry := localagent.EmptyDeployRegistry()
	registry.ACMEEmail = "ops@example.com"
	registry.ACMECA = "staging"
	registry.Targets = []localagent.DeployTarget{
		{Domain: "onlv.dev", Enabled: true},
		{Domain: "off.onlv.dev", Enabled: false},
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatal(err)
	}
	config, err := CaddyConfigForRegistry(paths, "127.0.0.1:19443", "127.0.0.1:19080", "127.0.0.1:9440", "/tmp/scenery-caddy.sock", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"storage file_system " + filepath.Join(paths.EdgeDir, "caddy-data"),
		"email ops@example.com",
		"onlv.dev:19443 {",
		"http://onlv.dev:19080 {",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("registry Caddy config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "off.onlv.dev") {
		t.Fatalf("registry Caddy config included disabled target:\n%s", config)
	}
}
