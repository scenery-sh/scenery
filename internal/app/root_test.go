package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverRootAcceptsPostgresBranchConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "pgapp",
		"dev": {
			"services": {
				"postgres": {
					"kind": "postgres",
					"mode": "local",
					"version": "18",
					"isolation": "database",
					"project": "pgapp",
					"parent_branch": "main",
					"parent_database": "pgapp_main",
					"branch_policy": "worktree",
					"branch_name_template": "{app}/{git_branch}",
					"branch_strategy": "template_database",
					"ttl": "168h",
					"database": "pgapp",
					"role": "scenery",
					"database_url_env": "DatabaseURL"
				}
			}
		}
	}`)

	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	svc := cfg.Dev.Services["postgres"]
	if svc.Kind != "postgres" || svc.Mode != "local" || svc.Isolation != "database" || svc.Project != "pgapp" ||
		svc.ParentBranch != "main" || svc.ParentDatabase != "pgapp_main" || svc.BranchPolicy != "worktree" ||
		svc.BranchNameTemplate != "{app}/{git_branch}" || svc.BranchStrategy != "template_database" ||
		svc.TTL != "168h" || svc.Database != "pgapp" || svc.Role != "scenery" || svc.DatabaseURLEnv != "DatabaseURL" {
		t.Fatalf("service = %+v", svc)
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

func TestConfigDatabaseURLEnv(t *testing.T) {
	t.Parallel()

	if got := (Config{}).DatabaseURLEnv(); got != "DatabaseURL" {
		t.Fatalf("default database URL env = %q, want DatabaseURL", got)
	}
	cfg := Config{Dev: DevConfig{Services: map[string]DevServiceConfig{
		"postgres": {DatabaseURLEnv: "AppDB"},
	}}}
	if got := cfg.DatabaseURLEnv(); got != "AppDB" {
		t.Fatalf("configured database URL env = %q, want AppDB", got)
	}
	cfg = Config{Dev: DevConfig{Services: map[string]DevServiceConfig{
		"main-db": {Kind: "postgres", DatabaseURLEnv: "PrimaryDB"},
	}}}
	if got := cfg.DatabaseURLEnv(); got != "PrimaryDB" {
		t.Fatalf("named Postgres database URL env = %q, want PrimaryDB", got)
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

func TestDiscoverRootRejectsUnknownPostgresBranchField(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{
		"name": "pgapp",
		"dev": {
			"services": {
				"postgres": {
					"kind": "postgres",
					"unknown_postgres_field": true
				}
			}
		}
	}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .scenery.json field "dev.services.postgres.unknown_postgres_field"`) {
		t.Fatalf("DiscoverRoot unknown field error = %v", err)
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
