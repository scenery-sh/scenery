package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeEdgeLeaseRequiresLocalExplicitDomain(t *testing.T) {
	t.Parallel()
	request := RegisterRequest{
		BaseAppID: "demo", AppRoot: t.TempDir(), SessionID: "edge-lease",
		OwnerPID: os.Getpid(), Owner: testProcessOwner(), Status: "running",
		RouteManifest: RouteManifest{Mode: RouteModePath, DomainHost: "demo.example.test"},
		WorktreeProxy: &Backend{Network: "tcp", Addr: "127.0.0.1:4011"},
	}
	good, err := NewSession(request, "127.0.0.1:9440", "http", nil)
	if err != nil || good.StateRoot != "" || good.WorktreeProxy == request.WorktreeProxy {
		t.Fatalf("edge lease must own a copied backend and no runtime state: %+v, %v", good, err)
	}
	for _, backend := range []Backend{
		{Network: "tcp", Addr: "192.0.2.1:4011"},
		{Network: "tcp", Addr: "localhost:4011"},
		{Network: "tcp", Addr: "127.0.0.1:0"},
		{Network: "tcp", Addr: "127.0.0.1:65536"},
		{Network: "unix", Addr: "/tmp/app.sock"},
	} {
		request.WorktreeProxy = &backend
		if _, err := NewSession(request, "127.0.0.1:9440", "http", nil); err == nil {
			t.Fatalf("accepted nonlocal or invalid forwarding backend %+v", backend)
		}
	}
	request.WorktreeProxy = &Backend{Network: "tcp", Addr: "[::1]:4011"}
	request.RouteManifest.DomainHost = ""
	if _, err := NewSession(request, "127.0.0.1:9440", "http", nil); err == nil {
		t.Fatal("accepted edge lease without an explicit domain")
	}
}

func TestWorktreeEdgeLeaseDoesNotWriteRuntimeManifest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(root, "machine", "registry.json"), "127.0.0.1:9440", "http")
	if err != nil {
		t.Fatal(err)
	}
	registry.ownerVerifier = testOwnerVerifier
	appRoot := filepath.Join(root, "app")
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo", AppRoot: appRoot, SessionID: "edge-lease",
		OwnerPID: os.Getpid(), Owner: testProcessOwner(), Status: "running",
		RouteManifest: RouteManifest{Mode: RouteModePath, DomainHost: "demo.example.test"},
		WorktreeProxy: &Backend{Network: "tcp", Addr: "127.0.0.1:4011"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.StateRoot != "" {
		t.Fatalf("edge lease acquired runtime state %q", session.StateRoot)
	}
	if _, err := os.Stat(appRoot); !os.IsNotExist(err) {
		t.Fatalf("machine edge touched the runtime checkout: %v", err)
	}
	if got, route, ok := registry.RouteTargetForHost("demo.example.test"); !ok || route != RoutePathMode || got.SessionID != session.SessionID {
		t.Fatalf("explicit domain not published: %+v %q %v", got, route, ok)
	}
}
