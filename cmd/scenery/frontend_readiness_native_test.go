//go:build scenery_frontend_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This named probe owns real subprocess, listener and production-build proof.
// Ordinary readiness tests keep their service-free HTTP transport fixtures.
func TestFrontendReadinessIntegration(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	address, err := freeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	script := `const flag = process.argv[1];
const port = Number(process.argv[2]);
Bun.serve({hostname:"127.0.0.1",port,fetch:async request => {
  if (new URL(request.url).pathname === "/client.js") {
    if (!await Bun.file(flag).exists()) return new Response("not built",{status:503});
    return new Response("export const generation = 1",{headers:{"Content-Type":"text/javascript"}});
  }
  return new Response('<script type="module" src="/client.js"></script>',{headers:{"Content-Type":"text/html"}});
}});`
	_, port, _ := strings.Cut(address, ":")
	flag := filepath.Join(root, "module-ready")
	child, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name: "proof", Kind: "frontend", Command: bun, Args: []string{"-e", script, flag, port}, Dir: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	process := &managedFrontendProcess{Name: "proof", Root: root, Addr: address, Process: child}
	t.Cleanup(func() { _ = process.Stop() })
	ready := make(chan error, 1)
	go func() { ready <- waitForManagedFrontend(ctx, process) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for len(process.readinessObservation()) == 0 {
		select {
		case err := <-ready:
			t.Fatalf("frontend returned before module publication: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if _, err := probeFrontendHTTP(ctx, address, &http.Client{Timeout: time.Second}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("listening frontend with missing client module was accepted: %v", err)
	}
	select {
	case err := <-ready:
		t.Fatalf("frontend returned while client module was unavailable: %v", err)
	default:
	}
	if err := os.WriteFile(flag, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	observations := process.readinessObservation()
	if len(observations) != 4 || observations[0].Milestone != "listening" || observations[1].Milestone != "representative_route" || observations[2].Milestone != "client_modules" || observations[2].Outcome != "ready" || observations[3].Outcome != "not_measured" {
		t.Fatalf("readiness milestones: %+v", observations)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.Done:
	default:
		t.Fatal("frontend child was not reaped")
	}
	if tcpAddrAcceptsConnections(address) {
		t.Fatal("frontend listener survived cleanup")
	}

	buildScript := `if (await Bun.file("fail-build").exists()) process.exit(7);
await Bun.write("dist/index.html", "generation-one");`
	static := &staticFrontendServer{Name: "proof", Root: root, Dir: filepath.Join(root, "dist"), buildBin: bun, buildArgs: []string{"-e", buildScript}, buildEnv: os.Environ()}
	var events, errors bytes.Buffer
	supervisor := &devSupervisor{console: newRunConsole(&events, &errors, false, true, "frontend-proof", root)}
	supervisor.setProductionFrontend("proof", static)
	supervisor.RebuildProductionFrontends(ctx, []string{"proof"})
	if !static.ready.Load() {
		t.Fatal("successful production build did not publish readiness")
	}
	if err := os.WriteFile(filepath.Join(root, "fail-build"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor.RebuildProductionFrontends(ctx, []string{"proof"})
	content, err := os.ReadFile(filepath.Join(static.Dir, "index.html"))
	if err != nil || string(content) != "generation-one" || !static.ready.Load() {
		t.Fatalf("failed build lost previous bundle: %q, %v", content, err)
	}
	decoder := json.NewDecoder(&events)
	starts := map[string]bool{}
	var terminals []runEvent
	for {
		var envelope struct {
			Data runEvent `json:"data"`
		}
		if err := decoder.Decode(&envelope); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		event := envelope.Data
		operation, _ := event.Data["operation_id"].(string)
		if event.Type == "frontend.rebuild" {
			starts[operation] = true
		}
		if event.Type == "frontend.rebuild.finish" {
			if operation == "" || !starts[operation] || event.Data["duration_ms"].(float64) <= 0 {
				t.Fatalf("unmatched production terminal: %+v", event)
			}
			terminals = append(terminals, event)
		}
	}
	if len(terminals) != 2 || terminals[0].Data["ok"] != true || terminals[1].Data["ok"] != false {
		t.Fatalf("production outcomes: %+v", terminals)
	}
	proof, err := json.Marshal(map[string]any{"readiness": observations, "missing_module_rejected": true, "production_terminals": terminals, "failed_build_retained_previous_bundle": true, "child_reaped": true, "listener_closed": true})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(proof))
}
