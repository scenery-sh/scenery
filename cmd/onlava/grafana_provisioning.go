package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const grafanaRequestDurationMetricName = "onlava_request_duration"

func renderGrafanaINI(cfg grafanaConfig) ([]byte, error) {
	var buf bytes.Buffer
	rootURL := strings.TrimRight(firstNonEmpty(cfg.PublicURL, cfg.URL), "/") + "/"
	domain := grafanaDefaultHost
	if parsed, err := url.Parse(rootURL); err == nil && parsed.Hostname() != "" {
		domain = parsed.Hostname()
	}
	fmt.Fprintf(&buf, "[server]\n")
	fmt.Fprintf(&buf, "http_addr = %s\n", grafanaDefaultHost)
	fmt.Fprintf(&buf, "http_port = %d\n", cfg.Port)
	fmt.Fprintf(&buf, "domain = %s\n", domain)
	fmt.Fprintf(&buf, "root_url = %s\n\n", rootURL)
	fmt.Fprintf(&buf, "[paths]\n")
	fmt.Fprintf(&buf, "data = %s\n", cfg.DataDir)
	fmt.Fprintf(&buf, "logs = %s\n", cfg.LogsDir)
	fmt.Fprintf(&buf, "plugins = %s\n", cfg.PluginsDir)
	fmt.Fprintf(&buf, "provisioning = %s\n\n", cfg.ProvisioningDir)
	fmt.Fprintf(&buf, "[auth]\n")
	fmt.Fprintf(&buf, "disable_login_form = true\n\n")
	fmt.Fprintf(&buf, "[auth.anonymous]\n")
	fmt.Fprintf(&buf, "enabled = true\n")
	fmt.Fprintf(&buf, "org_role = Viewer\n\n")
	fmt.Fprintf(&buf, "[analytics]\n")
	fmt.Fprintf(&buf, "reporting_enabled = false\n")
	fmt.Fprintf(&buf, "check_for_updates = false\n\n")
	if cfg.PluginPreinstall != "" {
		fmt.Fprintf(&buf, "[plugins]\n")
		fmt.Fprintf(&buf, "preinstall_sync = %s\n\n", cfg.PluginPreinstall)
	}
	return buf.Bytes(), nil
}

func renderGrafanaDatasources(cfg grafanaConfig) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "apiVersion: 1\n\n")
	fmt.Fprintf(&buf, "prune: true\n\n")
	fmt.Fprintf(&buf, "datasources:\n")
	if cfg.MetricsURL != "" {
		fmt.Fprintf(&buf, "  - name: onlava VictoriaMetrics\n")
		fmt.Fprintf(&buf, "    orgId: 1\n")
		fmt.Fprintf(&buf, "    uid: %s\n", cfg.MetricsDatasource)
		fmt.Fprintf(&buf, "    version: 1\n")
		fmt.Fprintf(&buf, "    type: victoriametrics-metrics-datasource\n")
		fmt.Fprintf(&buf, "    access: proxy\n")
		fmt.Fprintf(&buf, "    url: %s\n", cfg.MetricsURL)
		fmt.Fprintf(&buf, "    isDefault: true\n")
		fmt.Fprintf(&buf, "    editable: false\n\n")
	}
	if cfg.LogsURL != "" {
		fmt.Fprintf(&buf, "  - name: onlava VictoriaLogs\n")
		fmt.Fprintf(&buf, "    orgId: 1\n")
		fmt.Fprintf(&buf, "    uid: %s\n", cfg.LogsDatasource)
		fmt.Fprintf(&buf, "    version: 1\n")
		fmt.Fprintf(&buf, "    type: victoriametrics-logs-datasource\n")
		fmt.Fprintf(&buf, "    access: proxy\n")
		fmt.Fprintf(&buf, "    url: %s\n", cfg.LogsURL)
		fmt.Fprintf(&buf, "    editable: false\n\n")
	}
	if cfg.TracesURL != "" {
		fmt.Fprintf(&buf, "  - name: onlava VictoriaTraces\n")
		fmt.Fprintf(&buf, "    orgId: 1\n")
		fmt.Fprintf(&buf, "    uid: %s\n", cfg.TracesDatasource)
		fmt.Fprintf(&buf, "    version: 1\n")
		fmt.Fprintf(&buf, "    type: jaeger\n")
		fmt.Fprintf(&buf, "    access: proxy\n")
		fmt.Fprintf(&buf, "    url: %s\n", cfg.TracesURL)
		fmt.Fprintf(&buf, "    editable: false\n\n")
	}
	return buf.Bytes(), nil
}

func renderGrafanaDashboardProvider(cfg grafanaConfig) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "apiVersion: 1\n\n")
	fmt.Fprintf(&buf, "providers:\n")
	fmt.Fprintf(&buf, "  - name: onlava\n")
	fmt.Fprintf(&buf, "    orgId: 1\n")
	fmt.Fprintf(&buf, "    folder: onlava\n")
	fmt.Fprintf(&buf, "    type: file\n")
	fmt.Fprintf(&buf, "    disableDeletion: false\n")
	fmt.Fprintf(&buf, "    allowUiUpdates: false\n")
	fmt.Fprintf(&buf, "    updateIntervalSeconds: 30\n")
	fmt.Fprintf(&buf, "    options:\n")
	fmt.Fprintf(&buf, "      path: %s\n", cfg.DashboardsDir)
	return buf.Bytes(), nil
}

func writeGrafanaProvisioning(ctx context.Context, cfg grafanaConfig) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	for _, dir := range []string{
		filepath.Dir(cfg.ConfigPath),
		cfg.DataDir,
		cfg.LogsDir,
		cfg.PluginsDir,
		filepath.Join(cfg.ProvisioningDir, "datasources"),
		filepath.Join(cfg.ProvisioningDir, "dashboards"),
		cfg.DashboardsDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if data, err := renderGrafanaINI(cfg); err != nil {
		return err
	} else if err := atomicWriteFile(cfg.ConfigPath, data, 0o644); err != nil {
		return err
	}
	if data, err := renderGrafanaDatasources(cfg); err != nil {
		return err
	} else if err := atomicWriteFile(filepath.Join(cfg.ProvisioningDir, "datasources", "onlava.yaml"), data, 0o644); err != nil {
		return err
	}
	if data, err := renderGrafanaDashboardProvider(cfg); err != nil {
		return err
	} else if err := atomicWriteFile(filepath.Join(cfg.ProvisioningDir, "dashboards", "onlava.yaml"), data, 0o644); err != nil {
		return err
	}
	for name, data := range grafanaDashboardFiles(cfg) {
		if err := atomicWriteFile(filepath.Join(cfg.DashboardsDir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func grafanaDashboardFiles(cfg grafanaConfig) map[string][]byte {
	out := map[string][]byte{}
	for _, dashboard := range []struct {
		name  string
		model map[string]any
	}{
		{name: "onlava-overview.json", model: grafanaOverviewDashboard(cfg)},
		{name: "onlava-logs.json", model: grafanaLogsDashboard(cfg)},
		{name: "onlava-endpoint.json", model: grafanaEndpointDashboard(cfg)},
	} {
		data, err := json.MarshalIndent(dashboard.model, "", "  ")
		if err != nil {
			continue
		}
		out[dashboard.name] = append(data, '\n')
	}
	return out
}

func grafanaOverviewDashboard(cfg grafanaConfig) map[string]any {
	requestDuration := grafanaRequestDurationMetricName
	return baseGrafanaDashboard(grafanaOverviewUID, "onlava dev overview", []any{
		statPanel(1, "Requests observed", metricTarget(fmt.Sprintf("count_over_time(%s[15m])", requestDuration)), 0, 0, 6, 4),
		statPanel(2, "Latest latency", metricTarget(requestDuration), 6, 0, 6, 4),
		timeSeriesPanel(3, "Request duration", []any{metricTarget(requestDuration)}, 0, 4, 12, 8),
		timeSeriesPanel(4, "Errors", []any{metricTarget(fmt.Sprintf(`count_over_time(%s{onlava_is_error="true"}[5m])`, requestDuration))}, 12, 0, 12, 6),
		logsPanel(5, "Recent warnings and errors", `{level=~"warn|warning|error|fatal"}`, 12, 6, 12, 6),
	})
}

func grafanaLogsDashboard(cfg grafanaConfig) map[string]any {
	return baseGrafanaDashboard(grafanaLogsDashboardUID, "onlava dev logs", []any{
		logsPanel(1, "Log stream", "*", 0, 0, 24, 10),
		logsPanel(2, "Errors", `{level=~"error|fatal"}`, 0, 10, 24, 8),
	})
}

func grafanaEndpointDashboard(cfg grafanaConfig) map[string]any {
	requestDuration := grafanaRequestDurationMetricName
	dashboard := baseGrafanaDashboard(grafanaEndpointUID, "onlava dev endpoint", []any{
		timeSeriesPanel(1, "Endpoint latency", []any{metricTarget(fmt.Sprintf(`%s{onlava_service="$service",onlava_endpoint="$endpoint"}`, requestDuration))}, 0, 0, 12, 8),
		timeSeriesPanel(2, "Endpoint errors", []any{metricTarget(fmt.Sprintf(`count_over_time(%s{onlava_service="$service",onlava_endpoint="$endpoint",onlava_is_error="true"}[5m])`, requestDuration))}, 12, 0, 12, 8),
		logsPanel(3, "Endpoint logs", `{onlava_log_service="$service",onlava_log_endpoint="$endpoint"}`, 0, 8, 24, 8),
	})
	dashboard["templating"] = map[string]any{
		"list": []any{
			queryVariable("service", fmt.Sprintf(`label_values(%s, onlava_service)`, requestDuration)),
			queryVariable("endpoint", fmt.Sprintf(`label_values(%s{onlava_service="$service"}, onlava_endpoint)`, requestDuration)),
		},
	}
	return dashboard
}

func baseGrafanaDashboard(uid, title string, panels []any) map[string]any {
	return map[string]any{
		"uid":           uid,
		"title":         title,
		"tags":          []string{"onlava", "dev"},
		"timezone":      "browser",
		"schemaVersion": 39,
		"version":       1,
		"refresh":       "5s",
		"time": map[string]any{
			"from": "now-30m",
			"to":   "now",
		},
		"panels": panels,
	}
}

func statPanel(id int, title string, target map[string]any, x, y, w, h int) map[string]any {
	return map[string]any{
		"id":          id,
		"type":        "stat",
		"title":       title,
		"datasource":  metricDatasourceRef(),
		"targets":     []any{target},
		"gridPos":     gridPos(x, y, w, h),
		"fieldConfig": map[string]any{"defaults": map[string]any{}, "overrides": []any{}},
		"options":     map[string]any{"reduceOptions": map[string]any{"calcs": []string{"lastNotNull"}}},
	}
}

func timeSeriesPanel(id int, title string, targets []any, x, y, w, h int) map[string]any {
	return map[string]any{
		"id":          id,
		"type":        "timeseries",
		"title":       title,
		"datasource":  metricDatasourceRef(),
		"targets":     targets,
		"gridPos":     gridPos(x, y, w, h),
		"fieldConfig": map[string]any{"defaults": map[string]any{}, "overrides": []any{}},
		"options":     map[string]any{"legend": map[string]any{"displayMode": "list", "placement": "bottom"}},
	}
}

func logsPanel(id int, title, query string, x, y, w, h int) map[string]any {
	return map[string]any{
		"id":         id,
		"type":       "logs",
		"title":      title,
		"datasource": logsDatasourceRef(),
		"targets": []any{
			map[string]any{
				"refId": "A",
				"expr":  query,
				"query": query,
			},
		},
		"gridPos": gridPos(x, y, w, h),
		"options": map[string]any{"showTime": true, "showLabels": true, "wrapLogMessage": true},
	}
}

func metricTarget(expr string) map[string]any {
	return map[string]any{
		"refId":        "A",
		"expr":         expr,
		"legendFormat": strings.TrimSpace(expr),
	}
}

func queryVariable(name, query string) map[string]any {
	return map[string]any{
		"name":       name,
		"type":       "query",
		"datasource": metricDatasourceRef(),
		"query":      query,
		"refresh":    1,
		"sort":       1,
	}
}

func metricDatasourceRef() map[string]any {
	return map[string]any{
		"type": "victoriametrics-metrics-datasource",
		"uid":  grafanaMetricsUID,
	}
}

func logsDatasourceRef() map[string]any {
	return map[string]any{
		"type": "victoriametrics-logs-datasource",
		"uid":  grafanaLogsUID,
	}
}

func gridPos(x, y, w, h int) map[string]any {
	return map[string]any{"x": x, "y": y, "w": w, "h": h}
}
