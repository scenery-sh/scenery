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
	cfg := newGrafanaConfig(root, stack)
	if !cfg.Enabled || cfg.Required {
		t.Fatalf("cfg enabled/required = %v/%v", cfg.Enabled, cfg.Required)
	}
	if cfg.RootDir != filepath.Join(root, ".onlava", "grafana") {
		t.Fatalf("root dir = %q", cfg.RootDir)
	}
	if cfg.MetricsURL != "http://127.0.0.1:8428" || cfg.LogsURL != "http://127.0.0.1:9428" {
		t.Fatalf("victoria URLs = %q/%q", cfg.MetricsURL, cfg.LogsURL)
	}
	if cfg.TracesURL != "http://127.0.0.1:10428/select/jaeger" {
		t.Fatalf("traces URL = %q", cfg.TracesURL)
	}
}

func TestRenderGrafanaProvisioning(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack())
	ini, err := renderGrafanaINI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ini), "http_addr = 127.0.0.1") || !strings.Contains(string(ini), "preinstall_sync = victoriametrics-metrics-datasource,victoriametrics-logs-datasource") {
		t.Fatalf("unexpected grafana.ini:\n%s", ini)
	}

	datasources, err := renderGrafanaDatasources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{grafanaMetricsUID, grafanaLogsUID, grafanaTracesUID, "type: jaeger"} {
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
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack())
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

func TestStartGrafanaDisabled(t *testing.T) {
	t.Setenv("ONLAVA_DEV_GRAFANA", "0")
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), nil)
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
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), nil)
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
	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if component == nil || component.State().Status != "unavailable" {
		t.Fatalf("component = %+v, err = %v", component, err)
	}
}

func TestStartGrafanaReusesExternalGrafana(t *testing.T) {
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

	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !component.external || component.State().Status != "external" {
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

	component, err := startGrafanaForDev(context.Background(), t.TempDir(), fakeVictoriaStack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if component.external || component.State().Status != "degraded" {
		t.Fatalf("component = %+v state=%+v", component, component.State())
	}
}

func TestGrafanaStateIncludesStableLinks(t *testing.T) {
	cfg := newGrafanaConfig(t.TempDir(), fakeVictoriaStack())
	state := grafanaState(cfg, "ready", "")
	if state.Datasources["metrics"] != grafanaMetricsUID || state.Datasources["logs"] != grafanaLogsUID {
		t.Fatalf("datasources = %+v", state.Datasources)
	}
	if !strings.Contains(state.OverviewURL, "/d/"+grafanaOverviewUID) {
		t.Fatalf("overview URL = %q", state.OverviewURL)
	}
	if len(state.Dashboards) != 3 {
		t.Fatalf("dashboards = %+v", state.Dashboards)
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
