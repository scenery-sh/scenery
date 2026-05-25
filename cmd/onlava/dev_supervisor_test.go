package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

func TestAppChildEnvForcesColorWhenRequested(t *testing.T) {
	env := appChildEnv([]string{"A=1"}, true, "B=2")
	if !containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) missing CLICOLOR_FORCE=1", env)
	}
}

func TestAppChildEnvLeavesColorUnsetWhenDisabled(t *testing.T) {
	env := appChildEnv([]string{"A=1"}, false, "B=2")
	if containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) unexpectedly added CLICOLOR_FORCE=1", env)
	}
}

func TestAppEnvWithDotEnvAddsMissingValuesWithoutOverridingProcessEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-file\nB=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root)
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if containsString(env, "A=from-file") {
		t.Fatalf("env should not override process value: %v", env)
	}
	if !containsString(env, "B=2") {
		t.Fatalf("env missing .env value: %v", env)
	}
}

func TestAppEnvWithDotEnvCanLoadLocalOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-env\nB=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("B=from-local\nC=from-local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root, ".env", ".env.local")
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if !containsString(env, "B=from-local") {
		t.Fatalf("env missing .env.local override: %v", env)
	}
	if !containsString(env, "C=from-local") {
		t.Fatalf("env missing .env.local value: %v", env)
	}
}

func TestTemporalDevHelpers(t *testing.T) {
	host, port, err := splitTemporalAddress("127.0.0.1:7233")
	if err != nil {
		t.Fatalf("splitTemporalAddress: %v", err)
	}
	if host != "127.0.0.1" || port != 7233 {
		t.Fatalf("host/port = %s/%d", host, port)
	}
	if _, _, err := splitTemporalAddress("not-a-host-port"); err == nil {
		t.Fatal("expected invalid address error")
	}

	root := t.TempDir()
	if got, want := temporalLocalDBPath(root, ".onlava/temporal/dev.sqlite"), filepath.Join(root, ".onlava/temporal/dev.sqlite"); got != want {
		t.Fatalf("temporalLocalDBPath = %q, want %q", got, want)
	}

	cfg := app.TemporalConfig{
		Enabled:    true,
		AddressEnv: "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:  "orders",
		Local: app.TemporalLocalConfig{
			AutoStart:  true,
			DBFilename: ".onlava/temporal/dev.sqlite",
		},
	}
	rtCfg := temporalRuntimeConfigFromApp(cfg)
	if !rtCfg.Enabled || rtCfg.AddressEnv != "CUSTOM_TEMPORAL_ADDRESS" || rtCfg.Namespace != "orders" || !rtCfg.Local.AutoStart {
		t.Fatalf("runtime temporal config = %+v", rtCfg)
	}

	server := &temporalDevServer{info: onlavaRuntimeInfoForTest()}
	env := server.Env()
	if !containsString(env, "CUSTOM_TEMPORAL_ADDRESS=127.0.0.1:7233") || !containsString(env, "TEMPORAL_NAMESPACE=orders") {
		t.Fatalf("temporal env = %+v", env)
	}
}

func onlavaRuntimeInfoForTest() onlavaruntime.TemporalRuntimeInfo {
	return onlavaruntime.TemporalRuntimeInfo{
		Enabled:         true,
		Address:         "127.0.0.1:7233",
		AddressEnv:      "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:       "orders",
		TaskQueuePrefix: "onlava.orders",
	}
}

func TestStripANSI(t *testing.T) {
	input := []byte("\x1b[34mTRC\x1b[0m request completed code=ok\n")
	got := stripANSI(input)
	want := []byte("TRC request completed code=ok\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestIsExpectedOutputReadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "eof", err: io.EOF, want: true},
		{name: "os err closed", err: os.ErrClosed, want: true},
		{name: "net err closed", err: net.ErrClosed, want: true},
		{name: "wrapped path error", err: &fs.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}, want: true},
		{name: "other", err: io.ErrUnexpectedEOF, want: false},
	}
	for _, tt := range tests {
		if got := isExpectedOutputReadError(tt.err); got != tt.want {
			t.Fatalf("%s: isExpectedOutputReadError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestListAppsReturnsOnlyActiveSupervisorApp(t *testing.T) {
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	for _, rec := range []devdash.AppRecord{
		{ID: "basicapp", Name: "basicapp", Root: "/tmp/basicapp", UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "cronapp", Name: "cronapp", Root: "/tmp/cronapp", UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "demoapp-dev", Name: "demoapp", Root: "/tmp/demoapp", Running: true, UpdatedAt: now},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	s := &devSupervisor{
		cfg:   app.Config{Name: "demoapp", ID: "demoapp-dev"},
		store: store,
		status: devdash.AppRecord{
			ID:      "demoapp-dev",
			Name:    "demoapp",
			Root:    "/tmp/demoapp",
			Running: true,
		},
	}

	items, err := s.listApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("listApps() returned %d items, want 1: %#v", len(items), items)
	}
	if got := items[0]["id"]; got != "demoapp-dev" {
		t.Fatalf("listApps()[0].id = %v, want demoapp-dev", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestLooksLikeOnlavaDashboardProcess(t *testing.T) {
	tests := []struct {
		name string
		info procInfo
		want bool
	}{
		{
			name: "onlava dev process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/local/bin/onlava dev"},
			want: true,
		},
		{
			name: "non orphaned onlava dev process",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/onlava dev"},
			want: true,
		},
		{
			name: "onlava run is headless",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/onlava run"},
			want: false,
		},
		{
			name: "onlava app binary is not dashboard",
			info: procInfo{pid: 100, ppid: 42, cmd: "/tmp/onlava-app"},
			want: false,
		},
		{
			name: "non onlava process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/bin/python3 -m http.server"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeOnlavaDashboardProcess(tt.info); got != tt.want {
				t.Fatalf("looksLikeOnlavaDashboardProcess(%+v) = %v, want %v", tt.info, got, tt.want)
			}
		})
	}
}
