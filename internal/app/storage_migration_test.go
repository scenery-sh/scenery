package app

import (
	"errors"
	"testing"
)

func TestRetiredStorageSelectorsRequireExplicitMigration(t *testing.T) {
	for _, field := range []string{"cell_id", "share"} {
		var cfg Config
		err := decodeConfig(".scenery.json", []byte(`{"name":"files","storage":{"`+field+`":"old-cell"}}`), &cfg)
		var migration *StorageMigrationError
		if !errors.As(err, &migration) || migration.Field != "storage."+field {
			t.Fatalf("%s lost migration classification: %v", field, err)
		}
	}
	var cfg Config
	err := decodeConfig(".scenery.json", []byte(`{"name":"files","storage":{"unknown":true}}`), &cfg)
	var migration *StorageMigrationError
	if err == nil || errors.As(err, &migration) {
		t.Fatalf("ordinary typo was misclassified as migration: %v", err)
	}
}
