package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"

	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/envfile"
)

var dashboardUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type dashboardServer struct {
	supervisor *devSupervisor
	http       *http.Server
	state      dashboardRunState

	mu          sync.Mutex
	clients     map[*dashboardClient]struct{}
	mcpSessions map[string]*mcpSession
	assets      fs.FS
}

func newDashboardServer(supervisor *devSupervisor, assetsDir string) *dashboardServer {
	assets, _ := dashboardAssetFS(assetsDir)
	s := &dashboardServer{
		supervisor:  supervisor,
		state:       newDashboardRunState(supervisor.root, devdash.ListenAddr()),
		clients:     make(map[*dashboardClient]struct{}),
		mcpSessions: make(map[string]*mcpSession),
		assets:      assets,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/__graphql", s.handleGraphQL)
	mux.HandleFunc(devdash.WebSocketPath, s.handleWebSocket)
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
	if err := ensureDashboardPortAvailable(addr, s.state); err != nil {
		return fmt.Errorf("onlava dashboard failed to listen on %s: %w", addr, err)
	}
	ln, err := netListen("tcp", addr)
	if err != nil {
		return fmt.Errorf("onlava dashboard failed to listen on %s: %w", addr, err)
	}
	if err := s.state.write(); err != nil {
		_ = ln.Close()
		return fmt.Errorf("onlava dashboard failed to persist run state: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = s.http.Shutdown(context.Background())
	}()
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("onlava dashboard server failed", "err", err)
		}
	}()
	return nil
}

type procInfo struct {
	pid  int
	ppid int
	cmd  string
}

func looksLikeOnlavaDashboardProcess(info procInfo) bool {
	lower := strings.ToLower(info.cmd)
	return strings.Contains(lower, "onlava") && strings.Contains(lower, " dev")
}

func findListeningPID(addr string) (int, bool) {
	cmd := exec.Command("lsof", "-nP", "-iTCP@"+addr, "-sTCP:LISTEN", "-t")
	output, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	lines := strings.Fields(string(output))
	if len(lines) == 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return 0, false
	}
	return pid, true
}

func inspectProcess(pid int) (procInfo, bool) {
	cmd := exec.Command("ps", "-o", "pid=,ppid=,command=", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		return procInfo{}, false
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return procInfo{}, false
	}
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return procInfo{}, false
	}
	gotPID, err := strconv.Atoi(parts[0])
	if err != nil {
		return procInfo{}, false
	}
	ppid, err := strconv.Atoi(parts[1])
	if err != nil {
		return procInfo{}, false
	}
	return procInfo{
		pid:  gotPID,
		ppid: ppid,
		cmd:  strings.Join(parts[2:], " "),
	}, true
}

func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := inspectProcess(pid); !ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (s *dashboardServer) Close() error {
	if s == nil || s.http == nil {
		return nil
	}
	err := s.http.Close()
	if stateErr := s.state.remove(); stateErr != nil {
		return errors.Join(err, stateErr)
	}
	return err
}

func (s *dashboardServer) handleRoot(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/":
		http.Redirect(w, req, "/"+s.supervisor.activeAppID(), http.StatusFound)
		return
	default:
		if isDashboardStaticPath(req.URL.Path) {
			s.serveAsset(w, req, strings.TrimPrefix(req.URL.Path, "/"), detectAssetContentType(req.URL.Path))
			return
		}
	}

	if req.Method != http.MethodGet || req.URL.Path == devdash.WebSocketPath {
		http.NotFound(w, req)
		return
	}
	index := s.indexHTML(strings.TrimPrefix(req.URL.Path, "/"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, index)
}

func (s *dashboardServer) serveAsset(w http.ResponseWriter, req *http.Request, name, contentType string) {
	data, err := s.readAsset(name)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, req, filepath.Base(name), time.Time{}, bytes.NewReader(data))
}

func (s *dashboardServer) indexHTML(appID string) string {
	if appID == "" {
		appID = s.supervisor.activeAppID()
	}
	if data, err := s.readAsset("index.html"); err == nil {
		return strings.ReplaceAll(string(data), "__APP_ID__", appID)
	}
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>onlava Dev Dashboard</title>
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
      <h1>onlava Dev Dashboard</h1>
      <p>The dashboard server is running for <code>` + appID + `</code>, but the dashboard UI build is not available.</p>
      <p>Build it from the onlava repo with <code>bun run build</code> inside <code>ui/</code>.</p>
      <p>WebSocket endpoint: <code>ws://` + devdash.ListenAddr() + devdash.WebSocketPath + `</code></p>
    </main>
  </body>
</html>`
}

func dashboardAssetFS(assetsDir string) (fs.FS, error) {
	if dir := strings.TrimSpace(assetsDir); dir != "" {
		return os.DirFS(dir), nil
	}
	if dir := strings.TrimSpace(os.Getenv("ONLAVA_DEV_DASHBOARD_UI_DIR")); dir != "" {
		return os.DirFS(dir), nil
	}
	return nil, fs.ErrNotExist
}

func isDashboardStaticPath(path string) bool {
	switch path {
	case "/favicon.ico", "/manifest.webmanifest", "/site.webmanifest":
		return true
	}
	return strings.HasPrefix(path, "/assets/")
}

func (s *dashboardServer) readAsset(name string) ([]byte, error) {
	if s == nil || s.assets == nil {
		return nil, fs.ErrNotExist
	}
	switch name {
	case "favicon.ico":
		if data, err := fs.ReadFile(s.assets, "favicon.ico"); err == nil {
			return data, nil
		}
		return fs.ReadFile(s.assets, "assets/favicon.ico")
	case "site.webmanifest", "manifest.webmanifest":
		if data, err := fs.ReadFile(s.assets, "site.webmanifest"); err == nil {
			return data, nil
		}
		return fs.ReadFile(s.assets, "manifest.webmanifest")
	default:
		return fs.ReadFile(s.assets, strings.TrimPrefix(name, "/"))
	}
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
			go s.exportVictoriaTraceSummary(context.Background(), report.TraceSummary)
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
			go s.exportVictoriaLogEvent(report.LogEvent)
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
	go s.broadcastNotification(message, clients)
}

func (s *dashboardServer) broadcastNotification(message map[string]any, clients []*dashboardClient) {
	for _, client := range clients {
		if err := client.writeJSON(message); err != nil {
			s.removeClient(client)
			_ = client.conn.Close()
		}
	}
}

func (s *dashboardServer) addClient(conn dashboardWebSocket) *dashboardClient {
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
		"trace_id":    resp.Header.Get("X-Onlava-Trace-Id"),
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
	return envfile.ParseFile(path)
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

func pubSubHistoryPeriod(value string) time.Duration {
	switch strings.TrimSpace(value) {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	default:
		return 15 * time.Minute
	}
}

var netListen = func(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

type dashboardClient struct {
	conn    dashboardWebSocket
	writeMu sync.Mutex
}

type dashboardWebSocket interface {
	WriteJSON(any) error
	Close() error
	SetWriteDeadline(time.Time) error
}

const dashboardClientWriteTimeout = time.Second

func (c *dashboardClient) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(dashboardClientWriteTimeout)); err != nil {
		return err
	}
	defer func() {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}()
	return c.conn.WriteJSON(v)
}
