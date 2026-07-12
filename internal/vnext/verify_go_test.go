package vnext

import "testing"

func TestMigrationHasNoLegacyOwner(t *testing.T) {
	t.Parallel()
	if migrationHasNoLegacyOwner(nil) {
		t.Fatal("nil migration reported native-only")
	}
	if migrationHasNoLegacyOwner(&Migration{Services: []MigrationService{{Name: "drive", Active: "legacy"}}}) {
		t.Fatal("legacy owner reported native-only")
	}
	if !migrationHasNoLegacyOwner(&Migration{Services: []MigrationService{
		{Name: "drive", Active: "native"},
		{Name: "maps", Active: "native"},
	}}) {
		t.Fatal("all-native migration was not recognized")
	}
}
