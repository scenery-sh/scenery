package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGrafanaDevMode(t *testing.T) {
	t.Setenv("ONLAVA_DEV_GRAFANA", "")
	if got := grafanaDevMode(); got != grafanaModeAuto {
		t.Fatalf("default mode = %q, want auto", got)
	}
	t.Setenv("ONLAVA_DEV_GRAFANA", "0")
	if got := grafanaDevMode(); got != grafanaModeDisabled {
		t.Fatalf("disabled mode = %q", got)
	}
	t.Setenv("ONLAVA_DEV_GRAFANA", "1")
	if got := grafanaDevMode(); got != grafanaModeRequired {
		t.Fatalf("required mode = %q", got)
	}
}

func TestGrafanaConfigDefaults(t *testing.T) {
	root := t.TempDir()
	stack := fakeVictoriaStack()
	cfg := newGrafanaConfig(root, stack, "")
	if !cfg.Enabled || cfg.Required {
		t.Fatalf("cfg enabled/required = %v/%v", cfg.Enabled, cfg.Required)
	}
	if cfg.RootDir != filepath.Join(root, ".onlava", "grafana") {
		t.Fatalf("root dir = %q", cfg.RootDir)
	}
	if cfg.Version != "13.0.1+security-01" {
		t.Fatalf("version = %q", cfg.Version)
	}
	if cfg.Port != grafanaDefaultPort || cfg.URL != "http://127.0.0.1:10429" {
		t.Fatalf("grafana addr = %d/%q", cfg.Port, cfg.URL)
	}
	if cfg.MetricsURL != "http://127.0.0.1:8428" || cfg.LogsURL != "http://127.0.0.1:9428" {
		t.Fatalf("victoria URLs = %q/%q", cfg.MetricsURL, cfg.LogsURL)
	}
	if cfg.TracesURL != "http://127.0.0.1:10428/select/jaeger" {
		t.Fatalf("traces URL = %q", cfg.TracesURL)
	}
}

func TestRenderGrafanaProvisioning(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "https://grafana.acme.localhost")
	ini, err := renderGrafanaINI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ini), "http_addr = 127.0.0.1") ||
		!strings.Contains(string(ini), "domain = grafana.acme.localhost") ||
		!strings.Contains(string(ini), "root_url = https://grafana.acme.localhost/") ||
		!strings.Contains(string(ini), "preinstall_sync = victoriametrics-metrics-datasource@0.24.0,victoriametrics-logs-datasource@0.27.1") {
		t.Fatalf("unexpected grafana.ini:\n%s", ini)
	}

	datasources, err := renderGrafanaDatasources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prune: true", "orgId: 1", "version: 1", grafanaMetricsUID, grafanaLogsUID, grafanaTracesUID, "type: jaeger"} {
		if !strings.Contains(string(datasources), want) {
			t.Fatalf("datasources missing %q:\n%s", want, datasources)
		}
	}

	provider, err := renderGrafanaDashboardProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provider), "folder: onlava") || !strings.Contains(string(provider), cfg.DashboardsDir) {
		t.Fatalf("unexpected dashboard provider:\n%s", provider)
	}
}

func TestWriteGrafanaProvisioning(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	if err := writeGrafanaProvisioning(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		cfg.ConfigPath,
		filepath.Join(cfg.ProvisioningDir, "datasources", "onlava.yaml"),
		filepath.Join(cfg.ProvisioningDir, "dashboards", "onlava.yaml"),
		filepath.Join(cfg.DashboardsDir, "onlava-overview.json"),
		filepath.Join(cfg.DashboardsDir, "onlava-logs.json"),
		filepath.Join(cfg.DashboardsDir, "onlava-endpoint.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
}

func TestGrafanaDashboardsUseExportedRequestMetric(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	files := grafanaDashboardFiles(cfg)
	overview := string(files["onlava-overview.json"])
	if !strings.Contains(overview, grafanaRequestDurationMetricName) {
		t.Fatalf("overview dashboard does not query %s:\n%s", grafanaRequestDurationMetricName, overview)
	}
	if onlavaRequestDurationMetricName != "onlava_request_duration_seconds" || grafanaRequestDurationMetricName != "onlava_request_duration_seconds" {
		t.Fatalf("unexpected request duration metric names: otlp=%q grafana=%q", onlavaRequestDurationMetricName, grafanaRequestDurationMetricName)
	}
}

func TestDownloadedGrafanaBinaryRequiresConfiguredVersion(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	oldPath := filepath.Join(cfg.RootDir, "home", "grafana-12.2.1", "bin", "grafana")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, home := downloadedGrafanaBinary(cfg); path != "" || home != "" {
		t.Fatalf("unexpected wrong-version binary %q/%q", path, home)
	}

	wantPath := filepath.Join(cfg.RootDir, "home", "grafana-"+cfg.Version, "bin", "grafana")
	if err := os.MkdirAll(filepath.Dir(wantPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotPath, gotHome := downloadedGrafanaBinary(cfg)
	if gotPath != wantPath || gotHome != filepath.Join(cfg.RootDir, "home", "grafana-"+cfg.Version) {
		t.Fatalf("downloadedGrafanaBinary = %q/%q, want %q", gotPath, gotHome, wantPath)
	}
}

func TestResolveGrafanaBinaryPrefersManagedVersionOverPath(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	cfg.Download = false
	pathDir := t.TempDir()
	pathBinary := filepath.Join(pathDir, "grafana")
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	managedBinary := filepath.Join(cfg.RootDir, "home", "grafana-"+cfg.Version, "bin", "grafana")
	if err := os.MkdirAll(filepath.Dir(managedBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	got, _, err := resolveGrafanaBinary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != managedBinary {
		t.Fatalf("resolveGrafanaBinary = %q, want managed %q", got, managedBinary)
	}
}

func TestResolveGrafanaBinaryRejectsWrongPathVersion(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	cfg.Download = false
	pathDir := t.TempDir()
	pathBinary := filepath.Join(pathDir, "grafana")
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\necho 'Version 0.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	_, _, err := resolveGrafanaBinary(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "does not match pinned version") {
		t.Fatalf("resolveGrafanaBinary err = %v", err)
	}
}

func TestGrafanaChildEnvFiltersGFOverrides(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "https://grafana.acme.localhost")
	env := grafanaChildEnv([]string{
		"PATH=/bin",
		"GF_SERVER_ROOT_URL=https://wrong.example/",
		"GF_SECURITY_ADMIN_PASSWORD=secret",
	}, cfg)
	if !containsString(env, "PATH=/bin") {
		t.Fatalf("PATH missing from env: %#v", env)
	}
	for _, value := range env {
		if value == "GF_SECURITY_ADMIN_PASSWORD=secret" || value == "GF_SERVER_ROOT_URL=https://wrong.example/" {
			t.Fatalf("ambient GF_* override preserved: %#v", env)
		}
	}
	if !containsString(env, "GF_SERVER_ROOT_URL=https://grafana.acme.localhost/") {
		t.Fatalf("root URL missing from env: %#v", env)
	}
	if !containsString(env, "GF_SERVER_HTTP_PORT=10429") {
		t.Fatalf("port missing from env: %#v", env)
	}
}

func TestGrafanaChecksumFromResponseAcceptsGrafanaDistNames(t *testing.T) {
	body := "16ab83288e2a95f661d1234d0ecac0e2cfc2fa5a7209b0977bbe8a5b4940c67e  dist/grafana_13.0.1+security-01_25720641773_darwin_arm64.tar.gz\n"
	got := grafanaChecksumFromResponse(body, "grafana-13.0.1+security-01.darwin-arm64.tar.gz")
	if got != "16ab83288e2a95f661d1234d0ecac0e2cfc2fa5a7209b0977bbe8a5b4940c67e" {
		t.Fatalf("checksum = %q", got)
	}
}

func TestVerifyGrafanaArchiveChecksumRequiresCustomSHA(t *testing.T) {
	t.Setenv("ONLAVA_GRAFANA_DOWNLOAD_URL", "https://example.test/grafana.tar.gz")
	err := verifyGrafanaArchiveChecksum(context.Background(), "https://example.test/grafana.tar.gz", []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "ONLAVA_GRAFANA_DOWNLOAD_SHA256") {
		t.Fatalf("verifyGrafanaArchiveChecksum err = %v", err)
	}
}

func TestStartGrafanaDisabled(t *testing.T) {
	t.Setenv("ONLAVA_DEV_GRAFANA", "0")
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := component.State()
	if state.Enabled || state.Status != "disabled" {
		t.Fatalf("state = %+v", state)
	}
}

func TestStartGrafanaAutoMissingBinaryDoesNotFail(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ONLAVA_DEV_GRAFANA", "auto")
	t.Setenv("ONLAVA_DEV_GRAFANA_DOWNLOAD", "0")
	t.Setenv("ONLAVA_GRAFANA_PORT", strconv.Itoa(freePortForTest(t)))
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if component.State().Status != "unavailable" {
		t.Fatalf("state = %+v", component.State())
	}
}

func TestStartGrafanaRequiredMissingBinaryFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ONLAVA_DEV_GRAFANA", "1")
	t.Setenv("ONLAVA_DEV_GRAFANA_DOWNLOAD", "0")
	t.Setenv("ONLAVA_GRAFANA_PORT", strconv.Itoa(freePortForTest(t)))
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if component == nil || component.State().Status != "unavailable" {
		t.Fatalf("component = %+v, err = %v", component, err)
	}
}

func TestStartGrafanaDoesNotReuseExternalGrafanaByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != grafanaHealthPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := strings.Cut(parsed.Host, ":")
	t.Setenv("ONLAVA_GRAFANA_PORT", portText)
	t.Setenv("ONLAVA_DEV_GRAFANA_DOWNLOAD", "0")

	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !component.external || component.State().Status != "degraded" || component.State().Available {
		t.Fatalf("component = %+v state=%+v", component, component.State())
	}
}

func TestStartGrafanaReusesVerifiedExternalGrafana(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case grafanaHealthPath,
			"/api/datasources/uid/" + grafanaMetricsUID,
			"/api/datasources/uid/" + grafanaLogsUID,
			"/api/datasources/uid/" + grafanaTracesUID,
			"/api/dashboards/uid/" + grafanaOverviewUID,
			"/api/dashboards/uid/" + grafanaLogsDashboardUID,
			"/api/dashboards/uid/" + grafanaEndpointUID:
			_, _ = w.Write([]byte(`{"database":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := strings.Cut(parsed.Host, ":")
	t.Setenv("ONLAVA_GRAFANA_PORT", portText)
	t.Setenv("ONLAVA_GRAFANA_REUSE_EXTERNAL", "1")
	t.Setenv("ONLAVA_DEV_GRAFANA_DOWNLOAD", "0")

	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !component.external || component.State().Status != "external" || !component.State().Available {
		t.Fatalf("component = %+v state=%+v", component, component.State())
	}
}

func TestStartGrafanaTreatsNonGrafanaHealthAsOccupied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == grafanaHealthPath {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		_, _ = w.Write([]byte("not grafana"))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := strings.Cut(parsed.Host, ":")
	t.Setenv("ONLAVA_GRAFANA_PORT", portText)
	t.Setenv("ONLAVA_DEV_GRAFANA", "auto")
	t.Setenv("ONLAVA_DEV_GRAFANA_DOWNLOAD", "0")
	t.Setenv("PATH", t.TempDir())

	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if component.external || component.State().Status != "degraded" {
		t.Fatalf("component = %+v state=%+v", component, component.State())
	}
}

func TestGrafanaStateIncludesStableLinks(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	state := grafanaState(cfg, "ready", "")
	if !state.Available || !state.ServerReady || !state.DatasourcesReady || !state.DashboardsReady {
		t.Fatalf("ready state = %+v", state)
	}
	if state.Datasources["metrics"] != grafanaMetricsUID || state.Datasources["logs"] != grafanaLogsUID {
		t.Fatalf("datasources = %+v", state.Datasources)
	}
	if !strings.Contains(state.OverviewURL, "/d/"+grafanaOverviewUID) {
		t.Fatalf("overview URL = %q", state.OverviewURL)
	}
	if len(state.Dashboards) != 3 {
		t.Fatalf("dashboards = %+v", state.Dashboards)
	}
	proxied := grafanaStateWithBaseURL(state, "https://grafana.acme.localhost")
	if proxied.URL != "https://grafana.acme.localhost" || !strings.Contains(proxied.OverviewURL, "https://grafana.acme.localhost/d/"+grafanaOverviewUID) {
		t.Fatalf("proxied state URLs = %+v", proxied)
	}
	degraded := grafanaState(cfg, "degraded", "not ready")
	if degraded.Available || degraded.URL != "" || degraded.OverviewURL != "" {
		t.Fatalf("degraded state exposes links: %+v", degraded)
	}
}

func fakeVictoriaStack() *victoriaStack {
	return &victoriaStack{components: []*victoriaComponent{
		{spec: victoriaComponentSpec{Name: "metrics"}, baseURL: "http://127.0.0.1:8428"},
		{spec: victoriaComponentSpec{Name: "logs"}, baseURL: "http://127.0.0.1:9428"},
		{spec: victoriaComponentSpec{Name: "traces"}, baseURL: "http://127.0.0.1:10428"},
	}}
}

func freePortForTest(t *testing.T) int {
	t.Helper()
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	return port
}
