package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	durablestore "scenery.sh/internal/durable/store"
)

func TestWorkerDurableTokenCreate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"durabletoken","id":"durable-token-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/durabletoken\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")

	var out bytes.Buffer
	err := durableWorkerCommand([]string{"token", "create", "--app-root", root, "--service", "maps", "--name", "maps remote", "--id", "tok-test", "--json"}, &out)
	if err != nil {
		t.Fatalf("durableWorkerCommand token create: %v", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		Service       string `json:"service"`
		DBPath        string `json:"db_path"`
		Token         struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Secret    string `json:"secret"`
			TokenHash string `json:"token_hash"`
		} `json:"token"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode token response: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.durable.worker_token.create.v1" || payload.Service != "maps" || payload.Token.ID != "tok-test" || payload.Token.Name != "maps remote" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Token.Secret == "" || payload.Token.TokenHash == "" || payload.Token.Secret == payload.Token.TokenHash {
		t.Fatalf("token fields = %+v", payload.Token)
	}
	wantSuffix := filepath.ToSlash(filepath.Join(".scenery", "state", "db", "maps.durable.sqlite"))
	if !stringsHasSuffixSlash(payload.DBPath, wantSuffix) {
		t.Fatalf("db_path = %q, want suffix %q", payload.DBPath, wantSuffix)
	}

	db, err := durablestore.Open(context.Background(), "maps", filepath.FromSlash(payload.DBPath), durablestore.Options{Synchronous: "off"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, ok, err := db.AuthenticateWorkerToken(context.Background(), payload.Token.Secret)
	if err != nil || !ok {
		t.Fatalf("AuthenticateWorkerToken ok=%v err=%v", ok, err)
	}
	if token.ID != "tok-test" || token.TokenHash != payload.Token.TokenHash {
		t.Fatalf("stored token = %+v, payload hash %s", token, payload.Token.TokenHash)
	}
	if err := db.ReconcileTasks(context.Background(), []durablestore.TaskDeclaration{{Name: "maps.admin.v1", HandlerRef: "maps.admin.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Start(context.Background(), durablestore.StartRequest{ID: "job-admin", TaskName: "maps.admin.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}

	var listOut bytes.Buffer
	if err := durableWorkerCommand([]string{"jobs", "list", "--app-root", root, "--service", "maps", "--json"}, &listOut); err != nil {
		t.Fatalf("jobs list: %v", err)
	}
	var listPayload workerDurableJobsResponse
	if err := json.Unmarshal(listOut.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode jobs list: %v\n%s", err, listOut.String())
	}
	if len(listPayload.Jobs) != 1 || listPayload.Jobs[0].ID != "job-admin" || listPayload.Jobs[0].State != "queued" {
		t.Fatalf("jobs list payload = %+v", listPayload)
	}

	var inspectOut bytes.Buffer
	if err := durableWorkerCommand([]string{"jobs", "inspect", "job-admin", "--app-root", root, "--service", "maps", "--json"}, &inspectOut); err != nil {
		t.Fatalf("jobs inspect: %v", err)
	}
	var inspectPayload workerDurableJobsResponse
	if err := json.Unmarshal(inspectOut.Bytes(), &inspectPayload); err != nil {
		t.Fatalf("decode jobs inspect: %v\n%s", err, inspectOut.String())
	}
	if inspectPayload.Job == nil || inspectPayload.Job.ID != "job-admin" || len(inspectPayload.Events) == 0 {
		t.Fatalf("jobs inspect payload = %+v", inspectPayload)
	}
	if err := durableWorkerCommand([]string{"jobs", "cancel", "job-admin", "--app-root", root, "--service", "maps", "--json"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("jobs cancel: %v", err)
	}
	if err := durableWorkerCommand([]string{"jobs", "retry", "job-admin", "--app-root", root, "--service", "maps", "--json"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("jobs retry: %v", err)
	}
}

func TestParseWorkerDurableArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorkerDurableArgs([]string{"--endpoint", "https://api.example.test/", "--token", "secret", "--service", "maps", "--service", "jobs", "--log-format", "json"})
	if err != nil {
		t.Fatalf("parseWorkerDurableArgs: %v", err)
	}
	if opts.Endpoint != "https://api.example.test" || opts.Token != "secret" || opts.LogFormat != "json" || len(opts.Services) != 2 {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseWorkerDurableArgs([]string{"--endpoint", "https://api.example.test"}); err == nil {
		t.Fatal("expected missing token error")
	}
}

func stringsHasSuffixSlash(value, suffix string) bool {
	value = filepath.ToSlash(value)
	suffix = filepath.ToSlash(suffix)
	return len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix
}
