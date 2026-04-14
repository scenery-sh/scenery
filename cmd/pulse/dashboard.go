package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"

	"pulse.dev/internal/devdash"
)

//go:embed devdash_static/*
var dashboardAssets embed.FS

var dashboardUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type dashboardServer struct {
	supervisor *devSupervisor
	http       *http.Server

	mu          sync.Mutex
	clients     map[*dashboardClient]struct{}
	mcpSessions map[string]*mcpSession
}

func newDashboardServer(supervisor *devSupervisor) *dashboardServer {
	s := &dashboardServer{
		supervisor:  supervisor,
		clients:     make(map[*dashboardClient]struct{}),
		mcpSessions: make(map[string]*mcpSession),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/__graphql", s.handleGraphQL)
	mux.HandleFunc(devdash.WebSocketPath, s.handleWebSocket)
	mux.HandleFunc(devdash.EncoreWebSocketPath, s.handleWebSocket)
	mux.HandleFunc(devdash.ReportPath, s.handleReport)
	mux.HandleFunc("/sse", s.handleMCP)
	mux.HandleFunc("/message", s.handleMCP)
	s.http = &http.Server{
		Addr:    devdash.ListenAddr(),
		Handler: mux,
	}
	return s
}

func (s *dashboardServer) Start(ctx context.Context) error {
	addr := devdash.ListenAddr()
	if err := portAvailable(addr); err != nil {
		return fmt.Errorf("pulse dashboard failed to listen on %s: %w", addr, err)
	}
	ln, err := netListen("tcp", addr)
	if err != nil {
		return fmt.Errorf("pulse dashboard failed to listen on %s: %w", addr, err)
	}
	go func() {
		<-ctx.Done()
		_ = s.http.Shutdown(context.Background())
	}()
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("pulse dashboard server failed", "err", err)
		}
	}()
	return nil
}

func (s *dashboardServer) Close() error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.Close()
}

func (s *dashboardServer) handleRoot(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/":
		http.Redirect(w, req, "/"+s.supervisor.activeAppID(), http.StatusFound)
		return
	case "/site.webmanifest":
		s.serveAsset(w, req, "devdash_static/site.webmanifest", "application/manifest+json")
		return
	default:
		if strings.HasPrefix(req.URL.Path, "/assets/") {
			s.serveAsset(w, req, "devdash_static"+req.URL.Path, detectAssetContentType(req.URL.Path))
			return
		}
		if req.URL.Path == "/favicon.ico" {
			s.serveAsset(w, req, "devdash_static/assets/branding/icons/favicon.ico", "image/x-icon")
			return
		}
	}

	if req.Method != http.MethodGet || req.URL.Path == devdash.WebSocketPath || req.URL.Path == devdash.EncoreWebSocketPath {
		http.NotFound(w, req)
		return
	}
	index := s.indexHTML(strings.TrimPrefix(req.URL.Path, "/"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, index)
}

func (s *dashboardServer) serveAsset(w http.ResponseWriter, req *http.Request, name, contentType string) {
	data, err := dashboardAssets.ReadFile(name)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	data = rewriteDashboardAsset(name, data)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, req, filepath.Base(name), time.Time{}, bytes.NewReader(data))
}

func (s *dashboardServer) indexHTML(appID string) string {
	if appID == "" {
		appID = s.supervisor.activeAppID()
	}
	data, err := dashboardAssets.ReadFile("devdash_static/index.html")
	if err == nil {
		html := rewriteDashboardBranding(string(data))
		html = strings.ReplaceAll(html, `"/__encore"`, `"/__pulse"`)
		html = strings.ReplaceAll(html, "__APP_ID__", appID)
		return html
	}
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Pulse Dev Dashboard</title>
    <style>
      body { font-family: ui-sans-serif, system-ui, sans-serif; margin: 0; background: #0e1411; color: #ebf1ea; }
      main { max-width: 900px; margin: 0 auto; padding: 48px 24px; }
      h1 { margin: 0 0 12px; font-size: 32px; }
      p { color: #b3c0b5; line-height: 1.6; }
      code { background: #1b241d; padding: 2px 6px; border-radius: 6px; }
    </style>
  </head>
	<body>
    <main>
      <h1>Pulse Dev Dashboard</h1>
      <p>The dashboard server is running for <code>` + appID + `</code>, but vendored UI assets are not present yet.</p>
      <p>WebSocket endpoint: <code>ws://` + devdash.ListenAddr() + devdash.WebSocketPath + `</code></p>
    </main>
  </body>
</html>`
}

func (s *dashboardServer) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := dashboardUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	client := s.addClient(conn)
	defer func() {
		s.removeClient(client)
		_ = conn.Close()
	}()

	for {
		var reqMsg rpcRequest
		if err := conn.ReadJSON(&reqMsg); err != nil {
			return
		}
		resp := s.handleRPC(req.Context(), reqMsg)
		if reqMsg.ID == nil {
			continue
		}
		if err := client.writeJSON(resp); err != nil {
			return
		}
	}
}

func (s *dashboardServer) handleRPC(ctx context.Context, req rpcRequest) rpcResponse {
	result, err := s.dispatchRPC(ctx, req.Method, req.Params)
	if err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32000,
				Message: err.Error(),
			},
		}
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

func (s *dashboardServer) dispatchRPC(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "version":
		return map[string]any{"version": pulseDashboardCompatVersion, "channel": pulseDashboardCompatChannel}, nil
	case "list-apps":
		return s.supervisor.listApps(ctx)
	case "status":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		return s.supervisor.statusFor(ctx, firstNonEmpty(params.AppID, s.supervisor.activeAppID()))
	case "traces/clear":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return "ok", s.supervisor.store.ClearTraces(ctx, params.AppID)
	case "traces/list":
		var params struct {
			AppID     string `json:"app_id"`
			MessageID string `json:"message_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.supervisor.store.ListTraceSummaries(ctx, params.AppID, 100, params.MessageID)
	case "traces/get":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.traceEventsFor(ctx, params.AppID, params.TraceID)
	case "traces/spans/summaries/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.supervisor.store.GetTraceSummaries(ctx, params.AppID, params.TraceID)
	case "traces/spans/events/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.traceEventsForSpan(ctx, params.AppID, params.TraceID, params.SpanID)
	case "api-call":
		var params devdash.APICallRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.apiCall(ctx, params)
	case "db/query":
		var params devdash.QueryRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.queryDB(ctx, params)
	case "db/transaction":
		var params devdash.TransactionRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.transactionDB(ctx, params)
	case "db-migration-status":
		return []any{}, nil
	case "editors/list":
		return map[string]any{"editors": listEditors()}, nil
	case "editors/open":
		var params struct {
			AppID     string `json:"app_id"`
			Editor    string `json:"editor"`
			File      string `json:"file"`
			StartLine int    `json:"start_line"`
			StartCol  int    `json:"start_col"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return map[string]any{}, openEditor(s.supervisor.root, params.Editor, params.File, params.StartLine, params.StartCol)
	case "onboarding/get":
		return s.supervisor.store.GetOnboarding(ctx)
	case "onboarding/set":
		var params struct {
			Properties []string `json:"properties"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return nil, s.supervisor.store.SetOnboarding(ctx, params.Properties)
	case "telemetry":
		return "ok", nil
	default:
		if strings.HasPrefix(method, "ai/") {
			return nil, fmt.Errorf("%s is unsupported in Pulse", method)
		}
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

func (s *dashboardServer) handleReport(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+s.supervisor.reportToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	defer req.Body.Close()
	var report devdash.ReportEnvelope
	if err := json.NewDecoder(req.Body).Decode(&report); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if report.AppID == "" {
		report.AppID = s.supervisor.activeAppID()
	}
	switch report.Type {
	case "trace-summary":
		if report.TraceSummary != nil {
			report.TraceSummary.AppID = report.AppID
			_ = s.supervisor.store.AppendTraceSummary(req.Context(), report.TraceSummary)
			s.notify(&devdash.Notification{
				Method: "trace/new",
				Params: map[string]any{
					"app_id":     report.AppID,
					"test_trace": false,
					"span":       report.TraceSummary,
				},
			})
		}
	case "trace-event":
		if report.TraceEvent != nil {
			report.TraceEvent.AppID = report.AppID
			_ = s.supervisor.store.AppendTraceEvent(req.Context(), report.TraceEvent)
		}
	case "log":
		if report.LogEvent != nil {
			report.LogEvent.AppID = report.AppID
			_ = s.supervisor.store.WriteLogEvent(req.Context(), report.LogEvent)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *dashboardServer) handleMCP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/sse":
		s.handleMCPSSE(w, req)
	case "/message":
		s.handleMCPMessage(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (s *dashboardServer) notify(notification *devdash.Notification) {
	if notification == nil {
		return
	}
	message := map[string]any{
		"jsonrpc": "2.0",
		"method":  notification.Method,
		"params":  notification.Params,
	}
	s.mu.Lock()
	clients := make([]*dashboardClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	for _, client := range clients {
		if err := client.writeJSON(message); err != nil {
			s.removeClient(client)
			_ = client.conn.Close()
		}
	}
}

func (s *dashboardServer) addClient(conn *websocket.Conn) *dashboardClient {
	client := &dashboardClient{conn: conn}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[client] = struct{}{}
	return client
}

func (s *dashboardServer) removeClient(client *dashboardClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, client)
}

func (s *dashboardServer) apiCall(ctx context.Context, params devdash.APICallRequest) (map[string]any, error) {
	status, err := s.supervisor.statusFor(ctx, firstNonEmpty(params.AppID, s.supervisor.activeAppID()))
	if err != nil {
		return nil, err
	}
	if !status.Running {
		return nil, fmt.Errorf("app not running")
	}
	path, method, err := s.resolveEndpointRequest(status.Meta, params)
	if err != nil {
		return nil, err
	}
	body := io.Reader(nil)
	if len(params.Payload) > 0 {
		body = strings.NewReader(string(params.Payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+status.Addr+path, body)
	if err != nil {
		return nil, err
	}
	if len(params.Payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if params.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+params.AuthToken)
	}
	if params.CorrelationID != "" {
		req.Header.Set("X-Correlation-ID", params.CorrelationID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return map[string]any{
		"status":      resp.Status,
		"status_code": resp.StatusCode,
		"body":        bodyBytes,
		"trace_id":    resp.Header.Get("X-Pulse-Trace-Id"),
	}, nil
}

func (s *dashboardServer) resolveEndpointRequest(meta json.RawMessage, params devdash.APICallRequest) (path, method string, err error) {
	path = strings.TrimSpace(params.Path)
	method = strings.ToUpper(strings.TrimSpace(params.Method))
	if path != "" && method != "" {
		return path, method, nil
	}
	var payload struct {
		Svcs []struct {
			Name string `json:"name"`
			Rpcs []struct {
				Name        string         `json:"name"`
				Path        struct{}       `json:"-"`
				Methods     []string       `json:"http_methods"`
				AccessType  string         `json:"access_type"`
				ServiceName string         `json:"service_name"`
				RawPath     map[string]any `json:"path"`
			} `json:"rpcs"`
		} `json:"svcs"`
	}
	if err := json.Unmarshal(meta, &payload); err != nil {
		return "", "", err
	}
	for _, svc := range payload.Svcs {
		if svc.Name != params.Service {
			continue
		}
		for _, rpc := range svc.Rpcs {
			if rpc.Name != params.Endpoint {
				continue
			}
			if path == "" {
				path = renderMetadataPath(rpc.RawPath)
			}
			if method == "" {
				if len(rpc.Methods) > 0 {
					method = rpc.Methods[0]
				} else {
					method = http.MethodGet
				}
			}
			return path, method, nil
		}
	}
	if path == "" {
		return "", "", fmt.Errorf("unknown endpoint %s.%s", params.Service, params.Endpoint)
	}
	if method == "" {
		method = http.MethodGet
	}
	return path, method, nil
}

func renderMetadataPath(raw map[string]any) string {
	segments, _ := raw["segments"].([]any)
	if len(segments) == 0 {
		return "/"
	}
	var parts []string
	for _, item := range segments {
		segment, _ := item.(map[string]any)
		value, _ := segment["value"].(string)
		segmentType, _ := segment["type"].(string)
		switch segmentType {
		case "PARAM":
			parts = append(parts, ":"+value)
		default:
			parts = append(parts, value)
		}
	}
	return "/" + strings.Join(parts, "/")
}

func (s *dashboardServer) queryDB(ctx context.Context, req devdash.QueryRequest) ([]any, error) {
	appID := firstNonEmpty(req.AppID, s.supervisor.activeAppID())
	status, err := s.supervisor.statusFor(ctx, appID)
	if err != nil {
		return nil, err
	}
	db, err := openPostgres(ctx, status.AppRoot)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, req.Query, req.Params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows, req.ArrayMode)
}

func (s *dashboardServer) transactionDB(ctx context.Context, req devdash.TransactionRequest) ([]any, error) {
	appID := firstNonEmpty(req.AppID, s.supervisor.activeAppID())
	status, err := s.supervisor.statusFor(ctx, appID)
	if err != nil {
		return nil, err
	}
	db, err := openPostgres(ctx, status.AppRoot)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var results []any
	for _, query := range req.Queries {
		rows, err := tx.QueryContext(ctx, query.SQL, query.Params...)
		if err != nil {
			return nil, err
		}
		items, err := scanRows(rows, false)
		rows.Close()
		if err != nil {
			return nil, err
		}
		results = append(results, items)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func openPostgres(ctx context.Context, root string) (*sql.DB, error) {
	dsn, _, err := discoverDatabaseURL(root)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func discoverDatabaseURL(root string) (string, string, error) {
	for _, key := range []string{"DATABASE_URL", "DatabaseURL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, key, nil
		}
	}
	env, err := parseDotEnvFile(filepath.Join(root, ".env"))
	if err != nil {
		return "", "", err
	}
	for _, key := range []string{"DATABASE_URL", "DatabaseURL"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value, ".env:" + key, nil
		}
	}
	return "", "", fmt.Errorf("DATABASE_URL not found in environment or .env")
}

func parseDotEnvFile(path string) (map[string]string, error) {
	data := make(map[string]string)
	file, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(file), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		data[key] = value
	}
	return data, nil
}

func scanRows(rows *sql.Rows, arrayMode bool) ([]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var results []any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		if arrayMode {
			results = append(results, values)
			continue
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func listEditors() []string {
	candidates := []string{"cursor", "code", "code-insiders", "windsurf", "zed"}
	var found []string
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate); err == nil {
			found = append(found, candidate)
		}
	}
	if len(found) == 0 {
		return []string{}
	}
	return found
}

func openEditor(root, editor, file string, line, col int) error {
	target, err := localPath(root, file)
	if err != nil {
		return err
	}
	if editor == "" {
		editors := listEditors()
		if len(editors) == 0 {
			return fmt.Errorf("no supported editor found in PATH")
		}
		editor = editors[0]
	}
	var args []string
	switch editor {
	case "cursor", "code", "code-insiders", "windsurf":
		location := target
		if line > 0 {
			location = fmt.Sprintf("%s:%d", target, line)
			if col > 0 {
				location = fmt.Sprintf("%s:%d", location, col)
			}
		}
		args = []string{"--goto", location}
	case "zed":
		args = []string{target}
	default:
		return fmt.Errorf("unsupported editor %q", editor)
	}
	cmd := exec.Command(editor, args...)
	return cmd.Start()
}

func detectAssetContentType(path string) string {
	switch filepath.Ext(path) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".m4a":
		return "audio/mp4"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".ico":
		return "image/x-icon"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var netListen = func(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

type dashboardClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *dashboardClient) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}
