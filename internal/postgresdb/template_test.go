package postgresdb

import (
	"strings"
	"testing"
)

func TestTemplateDatabaseNameDeterministicAndBounded(t *testing.T) {
	first := TemplateDatabaseName("House Platform", "fp-123")
	second := TemplateDatabaseName("House Platform", "fp-123")
	different := TemplateDatabaseName("House Platform", "fp-456")

	if first != second {
		t.Fatalf("expected deterministic template name, got %q != %q", first, second)
	}
	if first == different {
		t.Fatalf("expected distinct template name when fingerprint changes, got %q", first)
	}
	if !strings.HasPrefix(first, "scenery_tpl_house_platform_") {
		t.Fatalf("unexpected template prefix: %q", first)
	}
	if len(first) > 63 {
		t.Fatalf("template name exceeds 63 bytes: %d (%q)", len(first), first)
	}

	longApp := strings.Repeat("enterprise_application_", 5)
	bounded := TemplateDatabaseName(longApp, "fp-123")
	if len(bounded) > 63 {
		t.Fatalf("long app template name exceeds 63 bytes: %d (%q)", len(bounded), bounded)
	}
	if !strings.HasPrefix(bounded, TemplateScopePrefix(longApp)) {
		t.Fatalf("bounded template %q does not match scope prefix %q", bounded, TemplateScopePrefix(longApp))
	}
}

func TestDatabaseURLWithNameReplacesDatabaseAndStripsSearchPath(t *testing.T) {
	got, err := DatabaseURLWithName("postgres://scenery:secret@127.0.0.1:54321/old_db?sslmode=disable&search_path=orders%2Cscenery", "scenery_tpl_app_1234")
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://scenery:secret@127.0.0.1:54321/scenery_tpl_app_1234?sslmode=disable"
	if got != want {
		t.Fatalf("DatabaseURLWithName = %q, want %q", got, want)
	}
}
