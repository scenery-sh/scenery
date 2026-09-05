package edge

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

// publishTestFrontend publishes a minimal production build and returns its
// `current` symlink path.
func publishTestFrontend(t *testing.T, artifactsRoot, appID, name string) string {
	t.Helper()
	source := t.TempDir()
	writePublishFixture(t, source, map[string]string{
		"index.html":            "<html>app-" + name + "</html>",
		"assets/app-abc123.js":  "console.log(1)",
		"models/scene.glb":      strings.Repeat("g", 4096),
		"nested/doc/index.html": "<html>nested</html>",
	})
	record, err := PublishFrontendArtifact(PublishInput{
		ArtifactsRoot: artifactsRoot, AppID: appID, Frontend: name, SourceDir: source, ReleaseID: "r1",
	})
	if err != nil {
		t.Fatalf("publish fixture: %v", err)
	}
	return record.CurrentPath
}

func TestCaddyConfigRendersStaticFrontendRoutes(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "microgrid-platform", "platform")
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain: "platform.onegraph.dev",
			Frontends: []StaticFrontendRoute{
				{Name: "platform", Root: current, BasePath: "/", OwnsRoot: true},
			},
		}},
		HTTPListenPort: "19080",
	})
	for _, want := range []string{
		"platform.onegraph.dev:19443 {",
		"@scenery_blocked path /runtime /runtime/* /dashboard /dashboard/* /console /console/* /__scenery /__scenery/*",
		"handle /api/* {",
		"root * " + current,
		"respond @fe_platform_method \"method not allowed\" 405",
		"encode zstd gzip",
		"rewrite @fe_platform_fallback /index.html",
		"header @fe_platform_immutable Cache-Control \"public, max-age=31536000, immutable\"",
		"header @fe_platform_revalidate Cache-Control \"no-cache\"",
		"file_server",
		"header_up X-Scenery-Public-Edge 1",
		"/platform/api /platform/api/*",
		"/platform/runtime /platform/runtime/*",
		"/platform/__scenery /platform/__scenery/*",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("static Caddy config missing %q:\n%s", want, config)
		}
	}
	for _, absent := range []string{"redir /platform /platform/ 308", "handle_path /platform/* {"} {
		if strings.Contains(config, absent) {
			t.Fatalf("root frontend retained named mount %q:\n%s", absent, config)
		}
	}
	// The root-owning frontend serves `/` statically: no catch-all proxy
	// after the static handles, but the /api handle still proxies.
	if got := strings.Count(config, "X-Scenery-Public-Edge 1"); got != 1 {
		t.Fatalf("expected exactly one public agent proxy (for /api), got %d:\n%s", got, config)
	}
}

func TestCaddyConfigPreservesNamedMountForRootArtifactBuiltWithNamedBase(t *testing.T) {
	t.Parallel()

	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "microgrid-platform", "platform")
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain: "platform.onegraph.dev",
			Frontends: []StaticFrontendRoute{
				{Name: "platform", Root: current, BasePath: "/platform", OwnsRoot: true},
			},
		}},
	})
	for _, want := range []string{
		"redir /platform /platform/ 308",
		"handle_path /platform/* {",
		"\thandle {\n",
		"root * " + current,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("named-base root config missing %q:\n%s", want, config)
		}
	}
}

func TestCaddyConfigStaticFrontendWithoutRootKeepsAgentCatchAll(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	current := publishTestFrontend(t, artifacts, "app", "web")
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain:    "app.example.com",
			Frontends: []StaticFrontendRoute{{Name: "web", Root: current, BasePath: "/web"}},
		}},
	})
	if got := strings.Count(config, "X-Scenery-Public-Edge 1"); got != 2 {
		t.Fatalf("expected /api proxy plus catch-all proxy, got %d:\n%s", got, config)
	}
}

func TestCaddyConfigSkipsInvalidOrIncompleteStaticFrontends(t *testing.T) {
	t.Parallel()
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:  "127.0.0.1:19443",
		Upstream:    "127.0.0.1:9440",
		AdminSocket: "/tmp/scenery-caddy.sock",
		Token:       "secret-token",
		PublicDomains: []PublicDomainSite{{
			Domain: "app.example.com",
			Frontends: []StaticFrontendRoute{
				{Name: "web", Root: filepath.Join(t.TempDir(), "missing", "current"), BasePath: "/web"},
				{Name: "../escape", Root: "/tmp", BasePath: "/../escape"},
				{Name: "api", Root: "/tmp", BasePath: "/api"},
			},
		}},
	})
	if strings.Contains(config, "handle_path") || strings.Contains(config, "file_server") {
		t.Fatalf("unpublishable frontends must fall back to the agent proxy:\n%s", config)
	}
	if !strings.Contains(config, "app.example.com:19443 {") {
		t.Fatalf("domain site missing:\n%s", config)
	}
}

func TestCaddyConfigPublicDirectBindsPublicPorts(t *testing.T) {
	t.Parallel()
	config := CaddyConfig(CaddyConfigOptions{
		ListenAddr:    "127.0.0.1:19443",
		Upstream:      "127.0.0.1:9440",
		AdminSocket:   "/tmp/scenery-caddy.sock",
		Token:         "secret-token",
		PublicDomains: []PublicDomainSite{{Domain: "app.example.com"}},
		PublicDirect:  true,
	})
	for _, want := range []string{
		"http_port 80",
		"https_port 443",
		"\napp.example.com {\n\tbind 0.0.0.0",
		"\nhttp://app.example.com {",
		"https://:19443 {",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("direct public config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "app.example.com:19443") {
		t.Fatalf("direct mode must not use the loopback forwarder port:\n%s", config)
	}
}

func TestPublicDomainSitesForDeployRegistryCarriesFrontends(t *testing.T) {
	t.Parallel()
	sites := publicDomainSitesForDeployRegistry(localagent.DeployRegistry{
		Targets: []localagent.DeployTarget{
			{Domain: "a.dev", Enabled: true, Frontends: []localagent.DeployTargetFrontend{
				{Name: "web", Path: "/x/current", BasePath: "/", Root: true},
			}},
			{Domain: "b.dev", Enabled: true},
		},
	})
	if len(sites) != 2 || len(sites[0].Frontends) != 1 || sites[0].Frontends[0].Name != "web" || !sites[0].Frontends[0].OwnsRoot {
		t.Fatalf("sites = %+v", sites)
	}
	if len(sites[1].Frontends) != 0 {
		t.Fatalf("target without publication metadata must stay proxy-only: %+v", sites[1])
	}
}

func TestValidateCaddyConfigUsesConfiguredRunnerInProcess(t *testing.T) {
	t.Parallel()

	var gotBinary string
	var gotArgs []string
	err := validateCaddyConfigWithRunner("/managed/caddy", "/state/Caddyfile", func(binary string, args []string) ([]byte, error) {
		gotBinary = binary
		gotArgs = append([]string(nil), args...)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBinary != "/managed/caddy" || strings.Join(gotArgs, " ") != "validate --config /state/Caddyfile --adapter caddyfile" {
		t.Fatalf("validation command = %q %v", gotBinary, gotArgs)
	}
	err = validateCaddyConfigWithRunner("caddy", "/state/Caddyfile", func(string, []string) ([]byte, error) {
		return []byte("invalid config\n"), fmt.Errorf("exit status 1")
	})
	if err == nil || !strings.Contains(err.Error(), "exit status 1: invalid config") {
		t.Fatalf("validation error = %v", err)
	}
}
