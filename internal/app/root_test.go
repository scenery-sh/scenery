package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiscoverRootAcceptsSQLiteServices(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "sqliteapp",
		"dev": {
			"services": {
				"auth": {
					"kind": "sqlite"
				},
				"billing": {
					"kind": "sqlite",
					"database": "billing-data",
					"database_url_env": "BILLING_DB"
				}
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	services := cfg.SQLiteServices()
	if len(services) != 2 {
		t.Fatalf("SQLiteServices count = %d, want 2", len(services))
	}
	auth, ok := cfg.SQLiteService("auth")
	if !ok || auth.DatabaseURLEnv != "AUTH_DATABASE_URL" || auth.DatabasePathEnv != "AUTH_DATABASE_PATH" || auth.FileLabel != "auth" {
		t.Fatalf("auth service = %+v ok=%v", auth, ok)
	}
	billing, ok := cfg.SQLiteService("billing")
	if !ok || billing.DatabaseURLEnv != "BILLING_DB" || billing.DatabasePathEnv != "BILLING_DATABASE_PATH" || billing.FileLabel != "billing-data" {
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

func TestDiscoverRootAcceptsWatchIgnoreConfigAlias(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".config.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if len(cfg.Watch.Ignore) != 1 || cfg.Watch.Ignore[0] != "reference/" {
		t.Fatalf("Watch.Ignore = %#v, want reference/", cfg.Watch.Ignore)
	}
}

func TestDiscoverRootAcceptsConfigJSONAlias(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".config.json", `{"name":"aliasapp","id":"alias-id"}`)

	appRoot, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if appRoot != root {
		t.Fatalf("app root = %q, want %q", appRoot, root)
	}
	if cfg.Name != "aliasapp" || cfg.ID != "alias-id" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if got, want := cfg.SourcePath(root), filepath.Join(root, ".config.json"); got != want {
		t.Fatalf("SourcePath = %q, want %q", got, want)
	}
	if got := cfg.SourceRelPath(root); got != ".config.json" {
		t.Fatalf("SourceRelPath = %q, want .config.json", got)
	}
}

func TestDiscoverRootPrefersSceneryJSONOverConfigAlias(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"canonical"}`)
	writeAppTestFile(t, root, ".config.json", `{"name":"alias"}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if cfg.Name != "canonical" {
		t.Fatalf("cfg.Name = %q, want canonical", cfg.Name)
	}
	if got, want := cfg.SourcePath(root), filepath.Join(root, ".scenery.json"); got != want {
		t.Fatalf("SourcePath = %q, want %q", got, want)
	}
}

func TestDiscoverRootIgnoresUnrelatedConfigJSONBelowAppRoot(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"canonical"}`)
	child := filepath.Join(root, "apps", "web")
	writeAppTestFile(t, child, ".config.json", `{"compilerOptions":{"jsx":"react-jsx"}}`)

	appRoot, cfg, err := DiscoverRoot(child)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if appRoot != root || cfg.Name != "canonical" {
		t.Fatalf("appRoot = %q cfg.Name = %q, want %q canonical", appRoot, cfg.Name, root)
	}
}

func TestDiscoverRootReportsAliasUnknownFieldsAsConfigJSON(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".config.json", `{"name":"aliasapp","extra":true}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .config.json field "extra"`) {
		t.Fatalf("DiscoverRoot alias unknown field error = %v", err)
	}
}

func TestDiscoverRootRejectsRemovedSyncDevService(t *testing.T) {
	removed := "elec" + "tric"
	for _, cfg := range []string{
		`{"name":"app","dev":{"services":{"sync":{"kind":"` + removed + `"}}}}`,
		`{"name":"app","dev":{"services":{"` + removed + `":{}}}}`,
	} {
		root := t.TempDir()
		writeAppTestFile(t, root, ".scenery.json", cfg)

		_, _, err := DiscoverRoot(root)
		if err == nil || !strings.Contains(err.Error(), "removed legacy sync service") {
			t.Fatalf("DiscoverRoot error = %v", err)
		}
	}
}

func TestConfigDatabaseURLEnv(t *testing.T) {
	t.Parallel()

	if got := (Config{}).DatabaseURLEnv(); got != "DatabaseURL" {
		t.Fatalf("default database URL env = %q, want DatabaseURL", got)
	}
	cfg := Config{Dev: DevConfig{Services: map[string]DevServiceConfig{
		"auth": {Kind: "sqlite", DatabaseURLEnv: "AppDB"},
	}}}
	if got := cfg.DatabaseURLEnv(); got != "AppDB" {
		t.Fatalf("configured database URL env = %q, want AppDB", got)
	}
	cfg = Config{Dev: DevConfig{Services: map[string]DevServiceConfig{
		"auth":    {Kind: "sqlite", DatabaseURLEnv: "AuthDB"},
		"billing": {Kind: "sqlite", DatabaseURLEnv: "BillingDB"},
	}}}
	if got := cfg.DatabaseURLEnv(); got != "DatabaseURL" {
		t.Fatalf("ambiguous database URL env = %q, want DatabaseURL", got)
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
					"kind": "zerofs",
					"access": "auth",
					"tenant_scoped": true,
					"max_object_bytes": 1073741824
				}
			}
		},
		"dev": {
			"services": {
				"storage": {
					"kind": "zerofs",
					"mode": "local",
					"route": "storage",
					"image": "ghcr.io/zerofs/zerofs:latest",
					"env": {
						"ZEROFS_WEBUI": "true"
					}
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
	if cfg.Storage.Default != "app" || store.Kind != "zerofs" || store.Access != "auth" || !store.TenantScoped || store.MaxObjectBytes != 1073741824 {
		t.Fatalf("storage = %+v store = %+v", cfg.Storage, store)
	}
	if cfg.Dev.Services["storage"].Kind != "zerofs" {
		t.Fatalf("dev storage service = %+v", cfg.Dev.Services["storage"])
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

func TestDiscoverRootRejectsRemovedDatabaseService(t *testing.T) {
	root := t.TempDir()
	removedKind := "post" + "gres"
	writeAppTestFile(t, root, ".scenery.json", fmt.Sprintf(`{
		"name": "pgapp",
		"dev": {
			"services": {
				"%[1]s": {
					"kind": "%[1]s"
				}
			}
		}
	}`, removedKind))

	_, _, err := DiscoverRoot(root)
	want := fmt.Sprintf(`dev.services.%[1]s kind "%[1]s" is not supported`, removedKind)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("DiscoverRoot removed database service error = %v", err)
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
						"kind": "zerofs",
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
						"kind": "zerofs"
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
