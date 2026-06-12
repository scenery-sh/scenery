package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestGrafanaDevMode(t *testing.T) {
	t.Setenv("SCENERY_DEV_GRAFANA", "")
	if got := grafanaDevMode(); got != grafanaModeAuto {
		t.Fatalf("default mode = %q, want auto", got)
	}
	t.Setenv("SCENERY_DEV_GRAFANA", "0")
	if got := grafanaDevMode(); got != grafanaModeDisabled {
		t.Fatalf("disabled mode = %q", got)
	}
	t.Setenv("SCENERY_DEV_GRAFANA", "1")
	if got := grafanaDevMode(); got != grafanaModeRequired {
		t.Fatalf("required mode = %q", got)
	}
}

func TestGrafanaConfigDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stack := fakeVictoriaStack()
	cfg := newGrafanaConfig(root, stack, "")
	if !cfg.Enabled || cfg.Required {
		t.Fatalf("cfg enabled/required = %v/%v", cfg.Enabled, cfg.Required)
	}
	if cfg.RootDir != filepath.Join(root, ".scenery", "grafana") {
		t.Fatalf("root dir = %q", cfg.RootDir)
	}
	if cfg.Version != "13.0.2" {
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

func TestGrafanaConfigForRootUsesProvidedRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "agent", "grafana")
	cfg := newGrafanaConfigForRoot(root, fakeVictoriaStack(), "")
	if cfg.RootDir != root {
		t.Fatalf("root dir = %q, want %q", cfg.RootDir, root)
	}
	if cfg.ConfigPath != filepath.Join(root, "conf", "grafana.ini") {
		t.Fatalf("config path = %q", cfg.ConfigPath)
	}
}

func TestRenderGrafanaProvisioning(t *testing.T) {
	t.Parallel()

	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "https://grafana.acme.localhost")
	ini, err := renderGrafanaINI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ini), "http_addr = 127.0.0.1") ||
		!strings.Contains(string(ini), "domain = grafana.acme.localhost") ||
		!strings.Contains(string(ini), "root_url = https://grafana.acme.localhost/") ||
		!strings.Contains(string(ini), "org_role = Viewer") ||
		!strings.Contains(string(ini), "viewers_can_edit = true") ||
		!strings.Contains(string(ini), "preinstall_sync = victoriametrics-metrics-datasource@0.25.0,victoriametrics-logs-datasource@0.28.0") {
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
	if !strings.Contains(string(provider), "folder: scenery") || !strings.Contains(string(provider), cfg.DashboardsDir) {
		t.Fatalf("unexpected dashboard provider:\n%s", provider)
	}
}

func TestWriteGrafanaProvisioning(t *testing.T) {
	t.Parallel()

	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	if err := writeGrafanaProvisioning(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		cfg.ConfigPath,
		filepath.Join(cfg.ProvisioningDir, "datasources", "scenery.yaml"),
		filepath.Join(cfg.ProvisioningDir, "dashboards", "scenery.yaml"),
		filepath.Join(cfg.DashboardsDir, "scenery-overview.json"),
		filepath.Join(cfg.DashboardsDir, "scenery-logs.json"),
		filepath.Join(cfg.DashboardsDir, "scenery-endpoint.json"),
		filepath.Join(cfg.DashboardsDir, "scenery-temporal.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
}

func TestGrafanaDashboardsUseExportedRequestMetric(t *testing.T) {
	t.Parallel()

	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	files := grafanaDashboardFiles(cfg)
	overview := string(files["scenery-overview.json"])
	if !strings.Contains(overview, grafanaRequestDurationMetricName) {
		t.Fatalf("overview dashboard does not query %s:\n%s", grafanaRequestDurationMetricName, overview)
	}
	if !strings.Contains(overview, "scenery_session_id") || !strings.Contains(overview, "label_values(scenery_request_duration_seconds, scenery_session_id)") {
		t.Fatalf("overview dashboard missing session variable/filter:\n%s", overview)
	}
	for _, want := range []string{"Request traces", grafanaTracesUID, `"queryType": "search"`, `"tags": "scenery.trace.type=REQUEST"`, "label_values(scenery_request_duration_seconds, scenery_app)"} {
		if !strings.Contains(overview, want) {
			t.Fatalf("overview dashboard missing %q:\n%s", want, overview)
		}
	}
	endpoint := string(files["scenery-endpoint.json"])
	if !strings.Contains(endpoint, `scenery_session_id=~\"$session\"`) {
		t.Fatalf("endpoint dashboard missing session filter:\n%s", endpoint)
	}
	temporal := string(files["scenery-temporal.json"])
	for _, want := range []string{grafanaTemporalUID, `scenery_temporal=\"true\"`, grafanaTracesUID, `"queryType": "search"`, `"tags": "scenery.temporal=true"`} {
		if !strings.Contains(temporal, want) {
			t.Fatalf("temporal dashboard missing %q:\n%s", want, temporal)
		}
	}
	if sceneryRequestDurationMetricName != "scenery_request_duration_seconds" || grafanaRequestDurationMetricName != "scenery_request_duration_seconds" {
		t.Fatalf("unexpected request duration metric names: otlp=%q grafana=%q", sceneryRequestDurationMetricName, grafanaRequestDurationMetricName)
	}
}

func TestDownloadedGrafanaBinaryRequiresConfiguredVersion(t *testing.T) {
	t.Parallel()

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

	wantPath := managedGrafanaTestPath(cfg)
	if err := os.MkdirAll(filepath.Dir(wantPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotPath, gotHome := downloadedGrafanaBinary(cfg)
	if gotPath != wantPath || gotHome != filepath.Dir(filepath.Dir(wantPath)) {
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
	managedBinary := managedGrafanaTestPath(cfg)
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

func TestResolveGrafanaBinaryIgnoresPathBinary(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	cfg.Download = false
	pathDir := t.TempDir()
	pathBinary := filepath.Join(pathDir, "grafana")
	if err := os.WriteFile(pathBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", pathDir)
	_, _, err := resolveGrafanaBinary(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "system PATH binaries are not used") {
		t.Fatalf("resolveGrafanaBinary err = %v", err)
	}
}

func managedGrafanaTestPath(cfg grafanaConfig) string {
	return filepath.Join(toolchainStoreDirForStateRoot(cfg.RootDir), "artifacts", "grafana", cfg.Version, currentPlatformDirForTest(), "home", "bin", "grafana")
}

func currentPlatformDirForTest() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func TestGrafanaChildEnvFiltersGFOverrides(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	body := "fb4729934fd8e3a348312c4cec5ff2143f4c4c22670be63c272d3277c6d774ea  dist/grafana_13.0.2_25720641773_darwin_arm64.tar.gz\n"
	got := grafanaChecksumFromResponse(body, "grafana-13.0.2.darwin-arm64.tar.gz")
	if got != "fb4729934fd8e3a348312c4cec5ff2143f4c4c22670be63c272d3277c6d774ea" {
		t.Fatalf("checksum = %q", got)
	}
}

func TestVerifyGrafanaArchiveChecksumRequiresCustomSHA(t *testing.T) {
	t.Setenv("SCENERY_GRAFANA_DOWNLOAD_URL", "https://example.test/grafana.tar.gz")
	err := verifyGrafanaArchiveChecksum(context.Background(), "https://example.test/grafana.tar.gz", []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "SCENERY_GRAFANA_DOWNLOAD_SHA256") {
		t.Fatalf("verifyGrafanaArchiveChecksum err = %v", err)
	}
}

func TestStartGrafanaDisabled(t *testing.T) {
	t.Setenv("SCENERY_DEV_GRAFANA", "0")
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
	t.Setenv("SCENERY_DEV_GRAFANA", "auto")
	t.Setenv("SCENERY_DEV_GRAFANA_DOWNLOAD", "0")
	t.Setenv("SCENERY_GRAFANA_PORT", strconv.Itoa(freePortForTest(t)))
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
	t.Setenv("SCENERY_DEV_GRAFANA", "1")
	t.Setenv("SCENERY_DEV_GRAFANA_DOWNLOAD", "0")
	t.Setenv("SCENERY_GRAFANA_PORT", strconv.Itoa(freePortForTest(t)))
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
	t.Setenv("SCENERY_GRAFANA_PORT", portText)
	t.Setenv("SCENERY_DEV_GRAFANA_DOWNLOAD", "0")

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
			"/api/dashboards/uid/" + grafanaEndpointUID,
			"/api/dashboards/uid/" + grafanaTemporalUID:
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
	t.Setenv("SCENERY_GRAFANA_PORT", portText)
	t.Setenv("SCENERY_GRAFANA_REUSE_EXTERNAL", "1")
	t.Setenv("SCENERY_DEV_GRAFANA_DOWNLOAD", "0")

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
	t.Setenv("SCENERY_GRAFANA_PORT", portText)
	t.Setenv("SCENERY_DEV_GRAFANA", "auto")
	t.Setenv("SCENERY_DEV_GRAFANA_DOWNLOAD", "0")
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
	t.Parallel()

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
	if !strings.Contains(state.TemporalURL, "/d/"+grafanaTemporalUID) {
		t.Fatalf("temporal URL = %q", state.TemporalURL)
	}
	if len(state.Dashboards) != 4 {
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

func TestGrafanaSubstrateRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack(), "")
	component := &grafanaComponent{cfg: cfg, state: grafanaState(cfg, "ready", "")}
	req := component.SubstrateRequest(123)
	if req.Kind != localagent.SubstrateGrafana || req.OwnerPID != 123 {
		t.Fatalf("substrate request = %+v", req)
	}
	if req.URLs["web"] != cfg.URL || req.Endpoints["health"] != cfg.URL+grafanaHealthPath {
		t.Fatalf("substrate endpoints = urls:%+v endpoints:%+v", req.URLs, req.Endpoints)
	}

	restored := grafanaComponentFromSubstrate(localagent.Substrate{
		Kind:      localagent.SubstrateGrafana,
		URLs:      req.URLs,
		Endpoints: req.Endpoints,
	}, fakeVictoriaStack(), "https://grafana.acme.localhost")
	if restored == nil {
		t.Fatal("restored grafana component is nil")
		return
	}
	if !restored.external || restored.URL() != cfg.URL {
		t.Fatalf("restored component = %+v", restored)
	}
	state := restored.State()
	if state.Status != "external" || state.URL != "https://grafana.acme.localhost" {
		t.Fatalf("restored state = %+v", state)
	}
	if state.Datasources["metrics"] != grafanaMetricsUID {
		t.Fatalf("restored datasources = %+v", state.Datasources)
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
