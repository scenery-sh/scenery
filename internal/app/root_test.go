package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverRootAcceptsNeonPostgresConfig(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".onlava.json", `{
		"name": "neonapp",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"version": "17",
					"isolation": "branch",
					"project": "neonapp",
					"parent_branch": "main",
					"branch_policy": "worktree",
					"branch_name_template": "{app}/{git_branch}",
					"ttl": "168h",
					"database": "neonapp",
					"role": "cloud_admin",
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
	if svc.Kind != "neon" || svc.Mode != "self-hosted" || svc.Isolation != "branch" || svc.Project != "neonapp" {
		t.Fatalf("service = %+v", svc)
	}
}

func TestDiscoverRootRejectsUnknownNeonField(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".onlava.json", `{
		"name": "neonapp",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"unknown_neon_field": true
				}
			}
		}
	}`)

	_, _, err := DiscoverRoot(root)
	if err == nil || !strings.Contains(err.Error(), `unknown .onlava.json field "dev.services.postgres.unknown_neon_field"`) {
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
