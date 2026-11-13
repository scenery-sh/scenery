package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	durablestore "scenery.sh/internal/durable/store"
	"scenery.sh/internal/postgresdb"
)

func TestWorkerDurableJobs(t *testing.T) {
	dsn := liveWorkerDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)
	root := persistentTestAppRoot(t, "worker-durable-jobs")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{"name":"durabletoken","id":"durable-token-id"}`,
		"go.mod":        "module example.com/durabletoken\n\ngo 1.27.0\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRootForTest(t) + "\n",
	})

	db, err := durablestore.Open(context.Background(), "maps", dsn, durablestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.ReconcileTasks(context.Background(), []durablestore.TaskDeclaration{{Name: "maps.admin.v1", HandlerRef: "maps.admin.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Start(context.Background(), durablestore.StartRequest{ID: "job-admin", TaskName: "maps.admin.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}

	var listOut bytes.Buffer
	if err := durableWorkerCommand([]string{"jobs", "list", "--app-root", root, "--service", "maps", "-o", "json"}, &listOut); err != nil {
		t.Fatalf("jobs list: %v", err)
	}
	var listPayload workerDurableJobsResponse
	if err := decodeCLIJSON(listOut.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode jobs list: %v\n%s", err, listOut.String())
	}
	if len(listPayload.Jobs) != 1 || listPayload.Jobs[0].ID != "job-admin" || listPayload.Jobs[0].State != "queued" {
		t.Fatalf("jobs list payload = %+v", listPayload)
	}

	var inspectOut bytes.Buffer
	if err := durableWorkerCommand([]string{"jobs", "inspect", "job-admin", "--app-root", root, "--service", "maps", "-o", "json"}, &inspectOut); err != nil {
		t.Fatalf("jobs inspect: %v", err)
	}
	var inspectPayload workerDurableJobsResponse
	if err := decodeCLIJSON(inspectOut.Bytes(), &inspectPayload); err != nil {
		t.Fatalf("decode jobs inspect: %v\n%s", err, inspectOut.String())
	}
	if inspectPayload.Job == nil || inspectPayload.Job.ID != "job-admin" || len(inspectPayload.Events) == 0 {
		t.Fatalf("jobs inspect payload = %+v", inspectPayload)
	}
	if err := durableWorkerCommand([]string{"jobs", "cancel", "job-admin", "--app-root", root, "--service", "maps", "-o", "json"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("jobs cancel: %v", err)
	}
	if err := durableWorkerCommand([]string{"jobs", "retry", "job-admin", "--app-root", root, "--service", "maps", "-o", "json"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("jobs retry: %v", err)
	}
}

func liveWorkerDatabaseURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres durable worker CLI test")
	}
	adminURL, err := workerAdminDatabaseURL(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	admin, err := postgresdb.Open(context.Background(), adminURL)
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres worker CLI tests: %v", err)
	}
	name := "scenery_worker_durable_test_" + randomWorkerHex(t, 8)
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	u, _ := url.Parse(raw)
	u.Path = "/" + name
	t.Cleanup(func() {
		db, _ := sql.Open(postgresdb.DriverName, adminURL)
		if db != nil {
			_, _ = db.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = db.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name)
			_ = db.Close()
		}
		_ = admin.Close()
	})
	return u.String()
}

func workerAdminDatabaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/postgres"
	return u.String(), nil
}

func randomWorkerHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}
