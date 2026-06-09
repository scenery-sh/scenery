package neonselfhost

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDeleteRemovesBackendBranchAndComputeContainer(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-test"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"delete",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-test",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("delete: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete: %v\n%s", err, stdout.String())
	}
	if payload.Status != "deleted" {
		t.Fatalf("payload = %+v", payload)
	}
	state, _, err = ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil {
		t.Fatalf("read backend: %v", err)
	}
	if _, ok := state.Branches["br-local-test"]; ok {
		t.Fatalf("branch still present: %+v", state.Branches)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if !strings.Contains(string(logBytes), "rm -f -v onlava-neon-compute-feature-x") {
		t.Fatalf("docker log = %q", string(logBytes))
	}
}

func TestRunDiffUsesPgDumpForReadyBackendBranches(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	state.Branches["br-local-main"] = BackendBranch{
		Project:          "onlv",
		Branch:           "main",
		TimelineID:       "timeline-main",
		EndpointID:       "main",
		ComputeContainer: "onlava-neon-compute-main",
		Host:             "127.0.0.1",
		Port:             55442,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "pg_dump.log")
	pgDump := filepath.Join(bin, "pg_dump")
	if err := os.WriteFile(pgDump, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$PG_DUMP_LOG"
case "$*" in
  *" -p 55441 "*)
    printf 'CREATE TABLE current_table(id integer);\n'
    ;;
  *" -p 55442 "*)
    printf 'CREATE TABLE main_table(id integer);\n'
    ;;
  *)
    echo "unexpected pg_dump args $*" >&2
    exit 1
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PG_DUMP_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("diff: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode diff: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(payload.Diff, "--- feature/x") || !strings.Contains(payload.Diff, "+CREATE TABLE main_table") {
		t.Fatalf("diff payload = %+v", payload)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read pg_dump log: %v", err)
	}
	if !strings.Contains(string(logBytes), "--schema-only --no-owner --no-privileges") {
		t.Fatalf("pg_dump log = %q", string(logBytes))
	}
}

func TestRunDiffFallsBackToComputeContainerPgDumpOnVersionMismatch(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	state.Branches["br-local-main"] = BackendBranch{
		Project:          "onlv",
		Branch:           "main",
		TimelineID:       "timeline-main",
		EndpointID:       "main",
		ComputeContainer: "onlava-neon-compute-main",
		Host:             "127.0.0.1",
		Port:             55442,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "ready",
	}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	bin := t.TempDir()
	logPath := filepath.Join(bin, "docker.log")
	pgDump := filepath.Join(bin, "pg_dump")
	if err := os.WriteFile(pgDump, []byte(`#!/bin/sh
echo "pg_dump: error: server version: 16.9; pg_dump version: 14.20" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$*" in
  *"onlava-neon-compute-feature-x"*)
    printf 'CREATE TABLE current_table(id integer);\n'
    ;;
  *"onlava-neon-compute-main"*)
    printf 'CREATE TABLE main_table(id integer);\n'
    ;;
  *)
    echo "unexpected docker $*" >&2
    exit 1
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)

	var stdout, stderr bytes.Buffer
	err := Run(&stdout, &stderr, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err != nil {
		t.Fatalf("diff: %v stderr=%q", err, stderr.String())
	}
	var payload BranchActionResult
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode diff: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(payload.Diff, "+CREATE TABLE main_table") {
		t.Fatalf("diff payload = %+v", payload)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "exec -e PGPASSWORD=cloud_admin onlava-neon-compute-feature-x pg_dump") || !strings.Contains(log, "-p 55433") {
		t.Fatalf("docker log = %q", log)
	}
}

func TestRunDiffRequiresReadyBackendBranches(t *testing.T) {
	root := t.TempDir()
	state := NewBackendState("tenant-test", 16)
	state.Branches["br-local-current"] = BackendBranch{
		Project:          "onlv",
		Branch:           "feature/x",
		TimelineID:       "timeline-current",
		EndpointID:       "feature-x",
		ComputeContainer: "onlava-neon-compute-feature-x",
		Host:             "127.0.0.1",
		Port:             55441,
		Database:         "onlv",
		Role:             "cloud_admin",
		Status:           "pending",
	}
	state.Branches["br-local-main"] = BackendBranch{Branch: "main", Status: "ready"}
	if err := WriteBackendState(filepath.Join(root, "backend.json"), state); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	err := Run(&bytes.Buffer{}, &bytes.Buffer{}, []string{
		"diff",
		"--project", "onlv",
		"--parent-branch", "main",
		"--branch", "feature/x",
		"--branch-id", "br-local-current",
		"--database", "onlv",
		"--role", "cloud_admin",
		"--target", "main",
		"--root", root,
		"--json",
	})
	if err == nil || !strings.Contains(err.Error(), "requires current branch") {
		t.Fatalf("diff error = %v", err)
	}
}
