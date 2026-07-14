package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

// writeEdgeHelperTestTarget writes target metadata pointing at this test
// process so the helper's live-process security checks (owner UID, start
// time) can pass without a real Caddy.
func writeEdgeHelperTestTarget(t *testing.T, extraJSON string) string {
	t.Helper()
	pid := os.Getpid()
	start, err := processStartTime(pid)
	if err != nil {
		t.Fatalf("processStartTime: %v", err)
	}
	path := filepath.Join(t.TempDir(), "edge-target.json")
	content := fmt.Sprintf(`{
  "kind": "scenery.edge.target",
  "schema_revision": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
  "spec_revision": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "producer": {"version": "v99.0.0", "toolchain": {"go_version": "go2.0"}},
  "edge_kind": "caddy",
  "target_addr": "127.0.0.1:19443",
  "http_target_addr": "127.0.0.1:19080",
  "pid": %d,
  "owner_uid": %d,
  "owner_gid": %d,
  "process_start": %q,
  "executable": ""%s
}
`, pid, os.Getuid(), os.Getgid(), start, extraJSON)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateEdgeTargetForPortToleratesNewerMetadataRevisions(t *testing.T) {
	// Regression guard for the public-edge outage where a running helper
	// rejected target metadata rewritten by an upgraded scenery under new
	// artifact schema/spec revisions and silently reset every connection.
	// The helper handoff must validate on the frozen payload fields alone,
	// tolerate unknown fields from future writers, and never rewrite the
	// user-owned file.
	path := writeEdgeHelperTestTarget(t, `,
  "future_field": {"nested": true}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	addr, err := validateEdgeTargetForPort(path, os.Getuid(), os.Getgid(), false)
	if err != nil {
		t.Fatalf("validateEdgeTargetForPort(https): %v", err)
	}
	if addr != "127.0.0.1:19443" {
		t.Fatalf("https target = %q", addr)
	}
	addr, err = validateEdgeTargetForPort(path, os.Getuid(), os.Getgid(), true)
	if err != nil {
		t.Fatalf("validateEdgeTargetForPort(http): %v", err)
	}
	if addr != "127.0.0.1:19080" {
		t.Fatalf("http target = %q", addr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("helper validation rewrote target metadata:\n%s", after)
	}
	if _, err := os.Stat(path + ".legacy.migrated"); err == nil {
		t.Fatalf("helper validation left a migration marker")
	}
}

func TestValidateEdgeTargetForPortKeepsSecurityChecks(t *testing.T) {
	uid := os.Getuid()
	gid := os.Getgid()

	// Non-loopback target.
	path := filepath.Join(t.TempDir(), "edge-target.json")
	if err := os.WriteFile(path, []byte(`{"edge_kind":"caddy","target_addr":"192.168.1.5:19443","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEdgeTargetForPort(path, uid, gid, false); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback err = %v", err)
	}

	// Port outside the managed high range.
	if err := os.WriteFile(path, []byte(`{"edge_kind":"caddy","target_addr":"127.0.0.1:8443","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEdgeTargetForPort(path, uid, gid, false); err == nil || !strings.Contains(err.Error(), "port must be in") {
		t.Fatalf("port range err = %v", err)
	}

	// Unexpected kind.
	if err := os.WriteFile(path, []byte(`{"edge_kind":"nginx","target_addr":"127.0.0.1:19443","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEdgeTargetForPort(path, uid, gid, false); err == nil || !strings.Contains(err.Error(), "unexpected kind") {
		t.Fatalf("kind err = %v", err)
	}

	// World-writable metadata.
	if err := os.WriteFile(path, []byte(`{"edge_kind":"caddy","target_addr":"127.0.0.1:19443","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o646); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEdgeTargetForPort(path, uid, gid, false); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("permissions err = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	// Start-time mismatch against a live process.
	mismatch := fmt.Sprintf(`{"edge_kind":"caddy","target_addr":"127.0.0.1:19443","pid":%d,"process_start":"1"}`, os.Getpid())
	if err := os.WriteFile(path, []byte(mismatch), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEdgeTargetForPort(path, uid, gid, false); err == nil || !strings.Contains(err.Error(), "start time mismatch") {
		t.Fatalf("start time err = %v", err)
	}
}

func TestEdgeHelperPlistStampsHandoffContract(t *testing.T) {
	t.Parallel()

	plist := edgeHelperPlist(edgeHelperOptions{
		OwnerUID:          501,
		OwnerGID:          20,
		OwnerHome:         "/Users/test",
		HelperTargetState: "/Users/test/run/edge-target.json",
		RouterAddr:        "127.0.0.1:9440",
		Public:            true,
		HelperVersion:     "1.2.3",
		HelperContract:    localagent.EdgeHelperContractRevision,
	})
	if !strings.Contains(plist, "<string>--helper-contract</string>") {
		t.Fatalf("plist missing --helper-contract:\n%s", plist)
	}
	opts, err := parseEdgeHelperPlistOptions([]byte(plist))
	if err != nil {
		t.Fatal(err)
	}
	if opts.HelperContract != localagent.EdgeHelperContractRevision || opts.HelperVersion != "1.2.3" || !opts.Public {
		t.Fatalf("parsed options = %+v", opts)
	}

	// Plists written before the contract stamp still parse; the missing
	// stamp is what drift detection flags.
	legacy := edgeHelperPlist(edgeHelperOptions{
		OwnerUID:          501,
		OwnerGID:          20,
		OwnerHome:         "/Users/test",
		HelperTargetState: "/Users/test/run/edge-target.json",
		HelperVersion:     "1.0.0",
	})
	if strings.Contains(legacy, "--helper-contract") {
		t.Fatalf("legacy plist unexpectedly stamped:\n%s", legacy)
	}
	opts, err = parseEdgeHelperPlistOptions([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if opts.HelperContract != "" {
		t.Fatalf("legacy parsed contract = %q", opts.HelperContract)
	}
}

func TestRetryEdgeHelperLaunchctlSurvivesAsyncBootout(t *testing.T) {
	t.Parallel()

	// launchctl bootout tears the old service down asynchronously, so the
	// first bootstrap attempts can fail with EIO (exit status 5). Install
	// must absorb that instead of making the operator rerun `scenery deploy
	// setup`.
	attempts := 0
	run := func(args ...string) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return []byte("Bootstrap failed: 5: Input/output error"), fmt.Errorf("exit status 5")
		}
		return nil, nil
	}
	var slept time.Duration
	sleep := func(d time.Duration) { slept += d }
	if err := retryEdgeHelperLaunchctl(10*time.Second, sleep, run, "bootstrap", "system", edgeHelperPlistPath); err != nil {
		t.Fatalf("retryEdgeHelperLaunchctl: %v", err)
	}
	if attempts != 3 || slept == 0 {
		t.Fatalf("attempts = %d, slept = %s", attempts, slept)
	}

	// A persistent failure still surfaces launchctl's error after the window.
	failing := func(args ...string) ([]byte, error) {
		return []byte("Bootstrap failed: 5: Input/output error"), fmt.Errorf("exit status 5")
	}
	err := retryEdgeHelperLaunchctl(0, func(time.Duration) {}, failing, "bootstrap", "system", edgeHelperPlistPath)
	if err == nil || !strings.Contains(err.Error(), "launchctl bootstrap") || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("persistent failure err = %v", err)
	}
}

func TestEdgeHelperFailureLogRateLimitsRepeatedDrops(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	log := newEdgeHelperFailureLog(30*time.Second, func() time.Time { return now })
	var out bytes.Buffer
	err := fmt.Errorf("edge target pid 42 is not running")
	log.report(&out, "[::]:443", "refusing connection", err)
	for range 25 {
		log.report(&out, "[::]:443", "refusing connection", err)
	}
	if lines := strings.Count(out.String(), "\n"); lines != 1 {
		t.Fatalf("suppressed reports still logged %d lines:\n%s", lines, out.String())
	}
	// A different failure logs immediately.
	log.report(&out, "[::]:80", "refusing connection", err)
	if lines := strings.Count(out.String(), "\n"); lines != 2 {
		t.Fatalf("distinct listener report suppressed:\n%s", out.String())
	}
	// After the interval, one line summarizes what was suppressed.
	now = now.Add(31 * time.Second)
	log.report(&out, "[::]:443", "refusing connection", err)
	if lines := strings.Count(out.String(), "\n"); lines != 3 {
		t.Fatalf("post-interval report missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "25 similar suppressed") {
		t.Fatalf("suppression summary missing:\n%s", out.String())
	}
}

func TestHandleEdgeHelperConnLogsValidationFailure(t *testing.T) {
	oldWriter := edgeHelperLogWriter
	oldLog := edgeHelperDropLog
	t.Cleanup(func() {
		edgeHelperLogWriter = oldWriter
		edgeHelperDropLog = oldLog
	})
	var out bytes.Buffer
	edgeHelperLogWriter = &out
	edgeHelperDropLog = newEdgeHelperFailureLog(30*time.Second, time.Now)

	// Target metadata whose PID is dead: validation fails, the connection is
	// closed, and the failure is explained in the helper log.
	path := filepath.Join(t.TempDir(), "edge-target.json")
	if err := os.WriteFile(path, []byte(`{"edge_kind":"caddy","target_addr":"127.0.0.1:19443","pid":2147483000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	opts := edgeHelperOptions{
		OwnerUID:          os.Getuid(),
		OwnerGID:          os.Getgid(),
		OwnerHome:         t.TempDir(),
		HelperTargetState: path,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleEdgeHelperConn(client, opts, edgeHelperListenSpec{Addr: "[::]:443"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleEdgeHelperConn did not close the connection")
	}
	if !strings.Contains(out.String(), "refusing connection (target metadata validation failed)") {
		t.Fatalf("helper log missing validation failure:\n%s", out.String())
	}
}
