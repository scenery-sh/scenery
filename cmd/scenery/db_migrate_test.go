package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/app"
)

func TestDBMigrationFailedResultIsOneEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := executeCLIWith([]string{"db", "migrate", "-o", "json"}, &stdout, &stderr, time.Now(), func([]string, *cliTelemetryInvocation) error {
		return writeDBLifecycleJSON(&stdout, dbMigrationResult{cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.migrate")}, fmt.Errorf("schema is unversioned"))
	}, func(cliTelemetryRecord) {})
	if code != 3 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := decoder.Decode(&envelope); err != nil || envelope.OK || len(envelope.Data) == 0 {
		t.Fatalf("envelope=%+v err=%v", envelope, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("second output: %v", err)
	}
}

func TestMigrationDiscoveryBindsDeclaredServiceAndSource(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "reports/db/migrations")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "0001_initialize.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE notes(id int);"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{Database: app.DatabaseConfig{Migrations: []app.DatabaseMigrationConfig{{Service: "reports", Directory: "reports/db/migrations"}}}}
	requirements := testSQLRequirements(t, "reports")
	plans, err := discoverDBMigrationPlans(root, cfg, requirements)
	if err != nil || len(plans) != 1 || len(plans[0].Migrations) != 1 || !strings.HasPrefix(plans[0].Migrations[0].Checksum, "sha256:") {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "0003_gap.sql"), []byte("ALTER TABLE notes ADD COLUMN title text;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverDBMigrationPlans(root, cfg, requirements); err == nil {
		t.Fatal("revision gap accepted")
	}
	if err := os.Remove(filepath.Join(directory, "0003_gap.sql")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "external.sql"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverDBMigrationPlans(root, cfg, requirements); err == nil {
		t.Fatal("symlink migration accepted")
	}
	cfg.Database.Migrations[0].Service = "undeclared"
	if _, err := discoverDBMigrationPlans(root, cfg, requirements); err == nil {
		t.Fatal("undeclared SQL binding accepted")
	}
}
