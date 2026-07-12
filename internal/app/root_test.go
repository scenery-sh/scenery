package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiscoverRootAcceptsDatabaseServices(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "dbapp",
		"dev": {
			"services": {
				"auth": {},
				"billing-data": {}
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	services := cfg.DatabaseServices()
	if len(services) != 2 {
		t.Fatalf("DatabaseServices count = %d, want 2", len(services))
	}
	auth, ok := cfg.DatabaseService("auth")
	if !ok || auth.Schema != "auth" {
		t.Fatalf("auth service = %+v ok=%v", auth, ok)
	}
	billing, ok := cfg.DatabaseService("billing-data")
	if !ok || billing.Schema != "billing_data" {
		t.Fatalf("billing service = %+v ok=%v", billing, ok)
	}
}

func TestDiscoverRootAcceptsBuildGoFlags(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "nativeapp",
		"build": {
			"go_flags": ["-tags=roofmapnet_native", "-gcflags=all=-N -l"]
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if got, want := strings.Join(cfg.Build.GoFlags, "\x00"), "-tags=roofmapnet_native\x00-gcflags=all=-N -l"; got != want {
		t.Fatalf("Build.GoFlags = %#v, want %#v", cfg.Build.GoFlags, []string{"-tags=roofmapnet_native", "-gcflags=all=-N -l"})
	}
}

func TestDiscoverRootAcceptsDeployConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "deployapp",
		"deploy": {
			"domain": "onlv.dev",
			"root": "web"
		},
		"frontends": {
			"web": {
				"root": "web"
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if cfg.Deploy.Domain != "onlv.dev" || cfg.Deploy.Root != "web" {
		t.Fatalf("Deploy = %+v", cfg.Deploy)
	}
}

func TestDiscoverRootAcceptsWatchIgnoreConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/", "tmp/*.go"]
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if got, want := strings.Join(cfg.Watch.Ignore, "\x00"), "reference/\x00tmp/*.go"; got != want {
		t.Fatalf("Watch.Ignore = %#v, want %#v", cfg.Watch.Ignore, []string{"reference/", "tmp/*.go"})
	}
}

func TestDiscoverRootFindsParentFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"canonical"}`)
	child := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	appRoot, cfg, err := DiscoverRoot(child)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if appRoot != root || cfg.Name != "canonical" {
		t.Fatalf("appRoot = %q cfg.Name = %q, want %q canonical", appRoot, cfg.Name, root)
	}
}

func TestDiscoverRootAcceptsStorageConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"cell_id": "onlv",
			"share": "worktree",
			"default": "app",
			"stores": {
				"app": {
					"kind": "local",
					"access": "auth",
					"tenant_scoped": true,
					"max_object_bytes": 1073741824
				}
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if got := cfg.StorageCellID(); got != "onlv" {
		t.Fatalf("StorageCellID = %q, want onlv", got)
	}
	store := cfg.Storage.Stores["app"]
	if cfg.Storage.Default != "app" || store.Kind != "local" || store.Access != "auth" || !store.TenantScoped || store.MaxObjectBytes != 1073741824 {
		t.Fatalf("storage = %+v store = %+v", cfg.Storage, store)
	}
}

func TestStorageCellIDIsDerivedFromAppIdentity(t *testing.T) {
	cfg := Config{Name: "ONLV Pulse"}
	if got := cfg.StorageCellID(); got != "onlv-pulse" {
		t.Fatalf("derived storage cell ID = %q, want onlv-pulse", got)
	}
	cfg = Config{Name: "from-name", ID: "explicit-id"}
	if got := cfg.StorageCellID(); got != "explicit-id" {
		t.Fatalf("derived storage cell ID from ID = %q, want explicit-id", got)
	}
	cfg.Storage.CellID = "shared-cell"
	if got := cfg.StorageCellID(); got != "shared-cell" {
		t.Fatalf("configured storage cell ID = %q, want shared-cell", got)
	}
}

func TestDiscoverRootRejectsUnknownBuildField(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "nativeapp",
		"build": {
			"go_flags": ["-tags=roofmapnet_native"],
			"shell": "GOFLAGS=-tags=roofmapnet_native"
		}
	}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .scenery.json field "build.shell"`) {
		t.Fatalf("DiscoverRoot unknown field error = %v", err)
	}
}

func TestDiscoverRootRejectsRemovedProxyConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"proxyapp","proxy":{"frontends":{"web":{"root":"web"}}}}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .scenery.json field "proxy"`) {
		t.Fatalf("DiscoverRoot proxy error = %v", err)
	}
}

func TestDiscoverRootRejectsInvalidWatchIgnoreConfig(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{name: "empty", pattern: "", want: "watch.ignore contains an empty pattern"},
		{name: "absolute", pattern: "/tmp/cache", want: `watch.ignore pattern "/tmp/cache" must be app-root-relative`},
		{name: "parent", pattern: "../reference", want: `watch.ignore pattern "../reference" must be app-root-relative`},
		{name: "negated", pattern: "!reference/", want: `watch.ignore pattern "!reference/" is invalid`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeAppTestFile(t, root, ".scenery.json", `{
				"name": "watchapp",
				"watch": {
					"ignore": [`+strconv.Quote(tt.pattern)+`]
				}
			}`)

			_, _, err := DiscoverRoot(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DiscoverRoot invalid watch.ignore error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDiscoverRootRejectsReservedDatabaseServiceNames(t *testing.T) {
	for _, name := range []string{"scenery", "public", "information-schema", "pg-users"} {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", `{"name":"pgapp","dev":{"services":{`+strconv.Quote(name)+`:{}}}}`)

		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), "plan 0097") {
			t.Fatalf("DiscoverRoot reserved %s error = %v", name, err)
		}
	}
}

func TestDiscoverRootRejectsDatabaseServiceSchemaCollisions(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"pgapp","dev":{"services":{"foo-bar":{},"foo_bar":{}}}}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), "foo-bar") || !strings.Contains(err.Error(), "foo_bar") || !strings.Contains(err.Error(), `Postgres schema "foo_bar"`) {
		t.Fatalf("DiscoverRoot schema collision error = %v", err)
	}
}

func TestDiscoverRootRejectsInvalidDeployConfig(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "uppercase domain",
			config: `{"name":"deployapp","deploy":{"domain":"Onlv.dev"}}`,
			want:   "deploy.domain must be lowercase",
		},
		{
			name:   "localhost",
			config: `{"name":"deployapp","deploy":{"domain":"localhost"}}`,
			want:   "deploy.domain must not be localhost",
		},
		{
			name:   "ip",
			config: `{"name":"deployapp","deploy":{"domain":"192.168.1.10"}}`,
			want:   "deploy.domain must not be an IP address",
		},
		{
			name:   "bad fqdn",
			config: `{"name":"deployapp","deploy":{"domain":"notadomain"}}`,
			want:   `deploy.domain "notadomain" must be a valid lowercase FQDN`,
		},
		{
			name:   "reserved root",
			config: `{"name":"deployapp","deploy":{"domain":"onlv.dev","root":"runtime"}}`,
			want:   `deploy.root "runtime" is reserved by Scenery`,
		},
		{
			name:   "unknown root",
			config: `{"name":"deployapp","deploy":{"domain":"onlv.dev","root":"web"}}`,
			want:   `deploy.root "web" must be "api" or a configured frontend`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeAppTestFile(t, root, ".scenery.json", tt.config)

			_, _, err := DiscoverRoot(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DiscoverRoot error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDiscoverRootRejectsInvalidStorageConfig(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", `{
			"name": "storageapp",
			"storage": {
				"stores": {
					"app": {
						"kind": "local",
						"bucket": "example"
					}
				}
			}
		}`)
		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), `unknown .scenery.json field "storage.stores.app.bucket"`) {
			t.Fatalf("DiscoverRoot unknown field error = %v", err)
		}
	})

	t.Run("missing stores", func(t *testing.T) {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", `{
			"name": "storageapp",
			"storage": {
				"default": "app"
			}
		}`)
		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), "storage.stores must define at least one store") {
			t.Fatalf("DiscoverRoot missing stores error = %v", err)
		}
	})

	t.Run("unsupported kind", func(t *testing.T) {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", `{
			"name": "storageapp",
			"storage": {
				"stores": {
					"app": {
						"kind": "s3"
					}
				}
			}
		}`)
		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), `storage.stores.app.kind "s3" is not supported`) {
			t.Fatalf("DiscoverRoot unsupported kind error = %v", err)
		}
	})

	t.Run("default missing", func(t *testing.T) {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", `{
			"name": "storageapp",
			"storage": {
				"default": "missing",
				"stores": {
					"app": {
						"kind": "local"
					}
				}
			}
		}`)
		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), `storage.default "missing" does not match a configured store`) {
			t.Fatalf("DiscoverRoot default error = %v", err)
		}
	})
}

func TestDiscoverRootAcceptsEmptyStorageKindAsLocal(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"storage": {
			"stores": {
				"app": {
					"access": "auth"
				}
			}
		}
	}`)
	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if store := cfg.Storage.Stores["app"]; store.Kind != "" {
		t.Fatalf("store kind = %q, want empty (treated as local)", store.Kind)
	}
}

func writeAppTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
