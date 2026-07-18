package app

import (
	"encoding/json"
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
		"frontends": {
			"web": {
				"root": "web"
			}
		},
		"envs": {
			"local": {"default": true, "frontends": {"web": {"serve": "development"}}},
			"production": {
				"domain": "onlv.dev",
				"frontends": {"web": {"serve": "production"}},
				"deploy": {"root": "web", "ssh": ["some-id"]}
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	env, err := cfg.EnvForSSHTarget("some-id")
	if err != nil || env.Domain != "onlv.dev" || env.Deploy.Root != "web" || strings.Join(env.Deploy.SSH, ",") != "some-id" {
		t.Fatalf("production env = %+v, err = %v", env, err)
	}
}

func TestResolveEnvAppliesFrontendModesAndDotenvStack(t *testing.T) {
	cfg := Config{
		Frontends: map[string]FrontendConfig{"web": {Root: "web"}},
		Envs: map[string]EnvConfig{
			"local":      {Default: true, Frontends: map[string]EnvFrontendConfig{"web": {Serve: "development"}}, Libraries: map[string]EnvLibraryConfig{"maps3d": {Linkage: "source"}}},
			"production": {Frontends: map[string]EnvFrontendConfig{"web": {Serve: "production"}}, Deploy: &EnvDeployConfig{SSH: []string{"prod"}}},
		},
	}
	local, err := cfg.ResolveEnv("")
	if err != nil || local.Name != "local" || local.Frontends["web"].Serve != "development" || strings.Join(local.DotEnvFiles(), ",") != ".env,.env.local" {
		t.Fatalf("local = %+v, err = %v", local, err)
	}
	if local.Libraries["maps3d"].Linkage != "source" {
		t.Fatalf("local libraries = %#v", local.Libraries)
	}
	production, err := cfg.EnvForSSHTarget("prod")
	if err != nil || production.Name != "production" || production.Frontends["web"].Serve != "production" || strings.Join(production.DotEnvFiles(), ",") != ".env,.env.production,.env.local,.env.production.local" {
		t.Fatalf("production = %+v, err = %v", production, err)
	}
}

func TestDiscoverRootRejectsInvalidEnvironmentOwnership(t *testing.T) {
	tests := []struct{ name, config, want string }{
		{"missing envs", `{"name":"demo"}`, "envs must declare environments"},
		{"two defaults", `{"name":"demo","envs":{"local":{"default":true},"other":{"default":true}}}`, "exactly one default"},
		{"duplicate target", `{"name":"demo","envs":{"local":{"default":true},"staging":{"deploy":{"ssh":["host"]}},"production":{"deploy":{"ssh":["host"]}}}}`, "duplicates target"},
		{"old deploy", `{"name":"demo","envs":{"local":{"default":true}},"deploy":{}}`, `unknown .scenery.json field "deploy"`},
		{"old routing", `{"name":"demo","envs":{"local":{"default":true}},"dev":{"routing":{}}}`, `unknown .scenery.json field "dev.routing"`},
		{"old serve", `{"name":"demo","frontends":{"web":{"root":"web","serve":"production"}},"envs":{"local":{"default":true}}}`, `unknown .scenery.json field "frontends.web.serve"`},
		{"invalid library linkage", `{"name":"demo","envs":{"local":{"default":true,"libraries":{"maps3d":{"linkage":"dynamic"}}}}}`, `linkage must be "source" or "shared"`},
		{"shared library missing manifest", `{"name":"demo","envs":{"local":{"default":true,"libraries":{"maps3d":{"linkage":"shared"}}}}}`, "manifest is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(tt.config), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := DiscoverRoot(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
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

func TestDiscoverRootRejectsRemovedStandardAuthConfigFields(t *testing.T) {
	tests := []struct {
		name   string
		config string
		path   string
	}{
		{name: "database url env", config: `{"name":"authapp","auth":{"database_url_env":"DATABASE_URL"}}`, path: "auth.database_url_env"},
		{name: "jwt secret env", config: `{"name":"authapp","auth":{"jwt_secret_env":"JWT_SECRET"}}`, path: "auth.jwt_secret_env"},
		{name: "refresh cookie name", config: `{"name":"authapp","auth":{"refresh_cookie_name":"custom_refresh"}}`, path: "auth.refresh_cookie_name"},
		{name: "cookie domain env", config: `{"name":"authapp","auth":{"auth_cookie_domain_env":"AUTH_COOKIE_DOMAIN"}}`, path: "auth.auth_cookie_domain_env"},
		{name: "public app url env", config: `{"name":"authapp","auth":{"public_app_url_env":"SCENERY_PUBLIC_APP_URL"}}`, path: "auth.public_app_url_env"},
		{name: "api base url env", config: `{"name":"authapp","auth":{"api_base_url_env":"SCENERY_API_BASE_URL"}}`, path: "auth.api_base_url_env"},
		{name: "email from env", config: `{"name":"authapp","auth":{"email_from_env":"AUTH_EMAIL_FROM"}}`, path: "auth.email_from_env"},
		{name: "google client id env", config: `{"name":"authapp","auth":{"google_oauth":{"client_id_env":"GOOGLE_OAUTH_CLIENT_ID"}}}`, path: "auth.google_oauth.client_id_env"},
		{name: "google client secret env", config: `{"name":"authapp","auth":{"google_oauth":{"client_secret_env":"GOOGLE_OAUTH_CLIENT_SECRET"}}}`, path: "auth.google_oauth.client_secret_env"},
		{name: "google token cipher key env", config: `{"name":"authapp","auth":{"google_oauth":{"token_cipher_key_env":"AUTH_TOKEN_CIPHER_KEY"}}}`, path: "auth.google_oauth.token_cipher_key_env"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeAppTestFile(t, root, ".scenery.json", tt.config)

			_, _, err := DiscoverRoot(root)
			want := `unknown .scenery.json field "` + tt.path + `"`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("DiscoverRoot removed auth field error = %v, want %q", err, want)
			}
		})
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
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"Onlv.dev","deploy":{}}}}`,
			want:   "envs.production.domain must be lowercase",
		},
		{
			name:   "localhost",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"localhost","deploy":{}}}}`,
			want:   "envs.production.domain: must not be localhost",
		},
		{
			name:   "ip",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"192.168.1.10","deploy":{}}}}`,
			want:   "envs.production.domain: must not be an IP address",
		},
		{
			name:   "bad fqdn",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"notadomain","deploy":{}}}}`,
			want:   `envs.production.domain: "notadomain" must be a valid lowercase FQDN`,
		},
		{
			name:   "reserved root",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"onlv.dev","deploy":{"root":"runtime"}}}}`,
			want:   `envs.production.deploy.root "runtime" is reserved by Scenery`,
		},
		{
			name:   "unknown root",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"domain":"onlv.dev","deploy":{"root":"web"}}}}`,
			want:   `envs.production.deploy.root "web" must be "api" or a configured frontend`,
		},
		{
			name:   "unsafe ssh target",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["-oProxyCommand=bad"]}}}}`,
			want:   `envs.production.deploy.ssh[0] "-oProxyCommand=bad" must be a safe OpenSSH host alias`,
		},
		{
			name:   "reserved ssh target",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["status"]}}}}`,
			want:   `envs.production.deploy.ssh[0] "status" must be a safe OpenSSH host alias`,
		},
		{
			name:   "duplicate ssh target",
			config: `{"name":"deployapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id","some-id"]}}}}`,
			want:   `envs.production.deploy.ssh[1] duplicates target "some-id"`,
		},
		{
			name:   "unsafe app id",
			config: `{"name":"My App","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`,
			want:   `app id "My App" must start with a lowercase letter or number`,
		},
		{
			name:   "traversing app id",
			config: `{"name":"..","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`,
			want:   `app id ".." must start with a lowercase letter or number`,
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

func TestEnvUICatalogOnlyOnLocal(t *testing.T) {
	root := t.TempDir()
	config := `{"name":"demo","frontends":{"web":{"root":"web"}},"envs":{"local":{"default":true},"production":{"ui_catalog":"../ui","domain":"demo.example.com","frontends":{"web":{"serve":"production"}},"deploy":{"ssh":["host"]}}}}`
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), "ui_catalog is a local development override") {
		t.Fatalf("error = %v, want ui_catalog rejection", err)
	}

	accepted := t.TempDir()
	config = `{"name":"demo","envs":{"local":{"default":true,"ui_catalog":"../ui"}}}`
	if err := os.WriteFile(filepath.Join(accepted, ".scenery.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := DiscoverRoot(accepted)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if env.UICatalog != "../ui" {
		t.Fatalf("UICatalog = %q, want ../ui", env.UICatalog)
	}
}

func TestResolvedEnvUICatalogDir(t *testing.T) {
	appRoot := t.TempDir()

	env := ResolvedEnv{Name: "local"}
	if dir, missing, err := env.UICatalogDir(appRoot); dir != "" || missing || err != nil {
		t.Fatalf("unset override = (%q, %v, %v), want empty", dir, missing, err)
	}

	env.UICatalog = "no-such-dir"
	if dir, missing, err := env.UICatalogDir(appRoot); dir != "" || !missing || err != nil {
		t.Fatalf("missing dir = (%q, %v, %v), want missing=true", dir, missing, err)
	}

	if err := os.MkdirAll(filepath.Join(appRoot, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.UICatalog = "bad"
	if _, _, err := env.UICatalogDir(appRoot); err == nil || !strings.Contains(err.Error(), "not a UI catalog root") {
		t.Fatalf("implausible dir error = %v, want catalog-root rejection", err)
	}

	catalog := filepath.Join(appRoot, "catalog")
	if err := os.MkdirAll(catalog, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"index.ts", "package.json"} {
		if err := os.WriteFile(filepath.Join(catalog, marker), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env.UICatalog = "catalog"
	dir, missing, err := env.UICatalogDir(appRoot)
	if err != nil || missing || dir != catalog {
		t.Fatalf("valid dir = (%q, %v, %v), want %q", dir, missing, err, catalog)
	}
}

func writeAppTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if rel == ".scenery.json" {
		var cfg map[string]any
		if json.Unmarshal([]byte(contents), &cfg) == nil {
			if _, ok := cfg["envs"]; !ok {
				cfg["envs"] = map[string]any{"local": map[string]any{"default": true}}
				if encoded, err := json.Marshal(cfg); err == nil {
					contents = string(encoded)
				}
			}
		}
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
