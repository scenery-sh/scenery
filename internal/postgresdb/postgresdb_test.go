package postgresdb

import (
	"strings"
	"testing"
)

func TestDatabaseNameForIsStableAndKeepsHash(t *testing.T) {
	a := DatabaseNameFor("My App With A Very Very Very Long Name", "reports", "/tmp/worktree-a")
	b := DatabaseNameFor("My App With A Very Very Very Long Name", "reports", "/tmp/worktree-a")
	c := DatabaseNameFor("My App With A Very Very Very Long Name", "reports", "/tmp/worktree-b")
	if a != b {
		t.Fatalf("DatabaseNameFor not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("DatabaseNameFor did not distinguish worktrees: %q", a)
	}
	if len(a) > 63 {
		t.Fatalf("DatabaseNameFor length = %d, want <= 63: %q", len(a), a)
	}
	if suffix := a[len(a)-12:]; strings.Trim(suffix, "0123456789abcdef") != "" {
		t.Fatalf("DatabaseNameFor suffix = %q, want hex hash", suffix)
	}
}

func TestParseURLAndRedactURL(t *testing.T) {
	if _, err := ParseURL("postgres://user:secret@localhost:5432/app"); err != nil {
		t.Fatalf("ParseURL returned error: %v", err)
	}
	if _, err := ParseURL("sqlite:///tmp/app.sqlite"); err == nil {
		t.Fatalf("ParseURL accepted non-postgres URL")
	}
	redacted := RedactURL("postgres://user:secret@localhost/app?sslmode=disable")
	if strings.Contains(redacted, "secret") || !strings.Contains(redacted, "xxxxx") {
		t.Fatalf("RedactURL = %q", redacted)
	}
}

func TestEnvAndRegistry(t *testing.T) {
	services := []Service{{Name: "reports", Database: "reports_abc", URL: "postgres://u:p@localhost/reports", DatabaseURLEnv: "REPORTS_DATABASE_URL", Source: SourceManaged}}
	envList := Env(services, true)
	env := strings.Join(envList, "\n")
	if !strings.Contains(env, "REPORTS_DATABASE_URL=postgres://u:p@localhost/reports") {
		t.Fatalf("Env missing service URL: %s", env)
	}
	registry := ""
	for _, item := range envList {
		if strings.HasPrefix(item, RegistryEnv+"=") {
			registry = strings.TrimPrefix(item, RegistryEnv+"=")
		}
	}
	if registry == "" {
		t.Fatalf("Env missing registry: %s", env)
	}
	decoded, err := DecodeRegistry(registry)
	if err != nil || len(decoded) != 1 || decoded[0].Name != "reports" {
		t.Fatalf("DecodeRegistry = %+v err=%v", decoded, err)
	}
}
