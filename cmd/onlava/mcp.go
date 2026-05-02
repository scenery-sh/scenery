package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"onlava.com/internal/devdash"
)

const mcpProtocolVersion = "2024-11-05"

type mcpSession struct {
	id       string
	appID    string
	outgoing chan []byte
}

type mcpToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(context.Context, *mcpSession, map[string]any) (any, bool, error)
}

func (s *dashboardServer) handleMCPSSE(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, err := randomToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session := &mcpSession{
		id:       sessionID,
		appID:    firstNonEmpty(req.URL.Query().Get("appID"), req.URL.Query().Get("app"), s.supervisor.activeAppID()),
		outgoing: make(chan []byte, 32),
	}
	s.addMCPSession(session)
	defer s.removeMCPSession(session.id)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	endpoint := "/message?session=" + url.QueryEscape(session.id)
	if session.appID != "" {
		endpoint += "&appID=" + url.QueryEscape(session.appID) + "&app=" + url.QueryEscape(session.appID)
	}
	if err := writeSSEEvent(w, "endpoint", endpoint); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case payload, ok := <-session.outgoing:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, "message", string(payload)); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *dashboardServer) handleMCPMessage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(req.URL.Query().Get("session"))
	if sessionID == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	session := s.getMCPSession(sessionID)
	if session == nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	defer req.Body.Close()
	var call rpcRequest
	if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if appID := firstNonEmpty(strings.TrimSpace(req.URL.Query().Get("appID")), strings.TrimSpace(req.URL.Query().Get("app"))); appID != "" {
		session.appID = appID
	}

	resp := s.handleMCPRPC(req.Context(), session, call)
	if resp != nil && call.ID != nil {
		if err := session.send(resp); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *dashboardServer) handleMCPRPC(ctx context.Context, session *mcpSession, req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "onlava-mcp",
					"version": onlavaDashboardCompatVersion,
				},
			},
		}
	case "notifications/initialized", "$/cancelRequest":
		return nil
	case "ping":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": s.mcpTools()},
		}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return mcpErrorResponse(req.ID, err)
		}
		tool, ok := s.lookupMCPTool(params.Name)
		if !ok {
			return &rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32601,
					Message: "tool not found: " + params.Name,
				},
			}
		}
		result, isErr, err := tool.Call(ctx, session, params.Arguments)
		if err != nil {
			return &rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  mcpToolResult(map[string]any{"error": err.Error()}, true),
			}
		}
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mcpToolResult(result, isErr),
		}
	default:
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32601,
				Message: "method not found: " + req.Method,
			},
		}
	}
}

func (s *dashboardServer) mcpTools() []map[string]any {
	tools := s.mcpToolDefinitions()
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		items = append(items, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": schema,
		})
	}
	return items
}

func (s *dashboardServer) lookupMCPTool(name string) (mcpToolDefinition, bool) {
	for _, tool := range s.mcpToolDefinitions() {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcpToolDefinition{}, false
}

func (s *dashboardServer) mcpToolDefinitions() []mcpToolDefinition {
	unsupported := func(name string) func(context.Context, *mcpSession, map[string]any) (any, bool, error) {
		return func(context.Context, *mcpSession, map[string]any) (any, bool, error) {
			return map[string]any{
				"supported": false,
				"tool":      name,
				"reason":    "unsupported in Onlava",
			}, false, nil
		}
	}
	return []mcpToolDefinition{
		{
			Name:        "get_databases",
			Description: "Describe the Postgres database discovered from DATABASE_URL or DatabaseURL.",
			Call:        s.mcpGetDatabases,
		},
		{
			Name:        "query_database",
			Description: "Run a SQL query against the discovered Postgres database.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"params": map[string]any{
						"type":  "array",
						"items": map[string]any{},
					},
				},
				"required": []string{"query"},
			},
			Call: s.mcpQueryDatabase,
		},
		{
			Name:        "call_endpoint",
			Description: "Invoke an Onlava endpoint through the local dev runtime.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"service":    map[string]any{"type": "string"},
					"endpoint":   map[string]any{"type": "string"},
					"path":       map[string]any{"type": "string"},
					"method":     map[string]any{"type": "string"},
					"payload":    map[string]any{},
					"auth_token": map[string]any{"type": "string"},
				},
			},
			Call: s.mcpCallEndpoint,
		},
		{
			Name:        "get_services",
			Description: "List Onlava services and endpoints from the current app metadata.",
			Call:        s.mcpGetServices,
		},
		{
			Name:        "get_middleware",
			Description: "List registered Onlava middleware definitions.",
			Call:        s.mcpGetMiddleware,
		},
		{
			Name:        "get_auth_handlers",
			Description: "List registered Onlava auth handlers.",
			Call:        s.mcpGetAuthHandlers,
		},
		{
			Name:        "get_traces",
			Description: "List recent traces captured by onlava run.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit":      map[string]any{"type": "integer"},
					"message_id": map[string]any{"type": "string"},
				},
			},
			Call: s.mcpGetTraces,
		},
		{
			Name:        "get_trace_spans",
			Description: "Fetch spans and events for one or more captured traces.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"trace_id": map[string]any{"type": "string"},
					"trace_ids": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
			},
			Call: s.mcpGetTraceSpans,
		},
		{
			Name:        "get_metadata",
			Description: "Return the full Onlava metadata snapshot used by the dashboard.",
			Call:        s.mcpGetMetadata,
		},
		{
			Name:        "get_src_files",
			Description: "Read source files from the active Onlava app.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"files": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []string{"files"},
			},
			Call: s.mcpGetSourceFiles,
		},
		{
			Name:        "get_pubsub",
			Description: "Return Onlava Pub/Sub topics, subscriptions, queue depth, worker counts, and processing stats.",
			Call:        s.mcpGetPubSub,
		},
		{
			Name:        "get_storage_buckets",
			Description: "Unsupported object storage inspection stub.",
			Call:        unsupported("get_storage_buckets"),
		},
		{
			Name:        "get_objects",
			Description: "Unsupported object storage inspection stub.",
			Call:        unsupported("get_objects"),
		},
		{
			Name:        "get_cache_keyspaces",
			Description: "Unsupported cache inspection stub.",
			Call:        unsupported("get_cache_keyspaces"),
		},
		{
			Name:        "get_metrics",
			Description: "Unsupported metrics inspection stub.",
			Call:        unsupported("get_metrics"),
		},
		{
			Name:        "get_cronjobs",
			Description: "List registered cron jobs from the current app metadata.",
			Call:        s.mcpGetCronJobs,
		},
		{
			Name:        "get_secrets",
			Description: "List secrets referenced by the app and whether Onlava discovered a value for them.",
			Call:        s.mcpGetSecrets,
		},
		{
			Name:        "search_docs",
			Description: "Unsupported documentation search stub.",
			Call:        unsupported("search_docs"),
		},
		{
			Name:        "get_docs",
			Description: "Unsupported documentation retrieval stub.",
			Call:        unsupported("get_docs"),
		},
	}
}

func (s *dashboardServer) mcpGetMetadata(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	payload, err := decodeRawJSON(status.Meta)
	if err != nil {
		return nil, true, err
	}
	return payload, false, nil
}

func (s *dashboardServer) mcpGetServices(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	return s.mcpMetadataSection(ctx, session, args, "svcs")
}

func (s *dashboardServer) mcpGetMiddleware(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	return s.mcpMetadataSection(ctx, session, args, "middleware")
}

func (s *dashboardServer) mcpGetAuthHandlers(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	item, _, err := s.mcpMetadataSection(ctx, session, args, "auth_handler")
	if err != nil {
		return nil, true, err
	}
	if item == nil {
		return []any{}, false, nil
	}
	return []any{item}, false, nil
}

func (s *dashboardServer) mcpGetCronJobs(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	return s.mcpMetadataSection(ctx, session, args, "cron_jobs")
}

func (s *dashboardServer) mcpGetPubSub(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	snapshot, err := s.supervisor.store.GetPubSubSnapshot(ctx, status.AppID)
	if err != nil {
		return nil, true, err
	}
	var topics any = []any{}
	if len(snapshot.Topics) > 0 {
		if err := json.Unmarshal(snapshot.Topics, &topics); err != nil {
			return nil, true, err
		}
	}
	return map[string]any{
		"app_id":     status.AppID,
		"topics":     topics,
		"updated_at": snapshot.UpdatedAt,
	}, false, nil
}

func (s *dashboardServer) mcpGetSecrets(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	meta, err := decodeRawJSON(status.Meta)
	if err != nil {
		return nil, true, err
	}
	payload, _ := meta.(map[string]any)
	pkgs, _ := payload["pkgs"].([]any)
	env, err := parseDotEnvFile(filepath.Join(status.AppRoot, ".env"))
	if err != nil {
		return nil, true, err
	}

	var secrets []map[string]any
	seen := make(map[string]bool)
	for _, pkgAny := range pkgs {
		pkg, _ := pkgAny.(map[string]any)
		pkgPath, _ := pkg["rel_path"].(string)
		names, _ := pkg["secrets"].([]any)
		for _, nameAny := range names {
			name, _ := nameAny.(string)
			if name == "" || seen[pkgPath+":"+name] {
				continue
			}
			seen[pkgPath+":"+name] = true
			valueSource := ""
			keys := secretLookupKeys(name)
			for _, key := range keys {
				if _, ok := os.LookupEnv(key); ok {
					valueSource = key
					break
				}
				if _, ok := env[key]; ok {
					valueSource = ".env:" + key
					break
				}
			}
			secrets = append(secrets, map[string]any{
				"name":      name,
				"package":   pkgPath,
				"env_keys":  keys,
				"available": valueSource != "",
				"source":    valueSource,
			})
		}
	}
	return map[string]any{"secrets": secrets}, false, nil
}

func (s *dashboardServer) mcpGetSourceFiles(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	files := stringSliceArg(args, "files")
	if len(files) == 0 {
		if single := stringArg(args, "file"); single != "" {
			files = []string{single}
		}
	}
	if len(files) == 0 {
		return nil, true, fmt.Errorf("files must not be empty")
	}
	items := make([]map[string]any, 0, len(files))
	for _, name := range files {
		target, err := localPath(status.AppRoot, name)
		if err != nil {
			return nil, true, err
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, true, err
		}
		rel, err := filepath.Rel(status.AppRoot, target)
		if err != nil {
			return nil, true, err
		}
		items = append(items, map[string]any{
			"path":    filepath.ToSlash(rel),
			"content": string(data),
		})
	}
	return map[string]any{"files": items}, false, nil
}

func (s *dashboardServer) mcpGetTraces(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	limit := intArg(args, "limit", 100)
	messageID := stringArg(args, "message_id")
	items, err := s.supervisor.store.ListTraceSummaries(ctx, status.AppID, limit, messageID)
	if err != nil {
		return nil, true, err
	}
	return map[string]any{"traces": items}, false, nil
}

func (s *dashboardServer) mcpGetTraceSpans(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	traceIDs := stringSliceArg(args, "trace_ids")
	if single := stringArg(args, "trace_id"); single != "" {
		traceIDs = append([]string{single}, traceIDs...)
	}
	if len(traceIDs) == 0 {
		return nil, true, fmt.Errorf("trace_id or trace_ids is required")
	}
	var traces []map[string]any
	for _, traceID := range traceIDs {
		summaries, err := s.supervisor.store.GetTraceSummaries(ctx, status.AppID, traceID)
		if err != nil {
			return nil, true, err
		}
		events, err := s.traceEventsFor(ctx, status.AppID, traceID)
		if err != nil {
			return nil, true, err
		}
		traces = append(traces, map[string]any{
			"trace_id":  traceID,
			"summaries": summaries,
			"events":    events,
		})
	}
	return map[string]any{"traces": traces}, false, nil
}

func (s *dashboardServer) mcpCallEndpoint(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, true, err
	}
	var req devdash.APICallRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, true, err
	}
	req.AppID = status.AppID
	result, err := s.apiCall(ctx, req)
	if err != nil {
		return nil, true, err
	}
	return result, false, nil
}

func (s *dashboardServer) mcpGetDatabases(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	dsn, source, err := discoverDatabaseURL(status.AppRoot)
	if err != nil {
		return nil, true, err
	}
	return map[string]any{
		"databases": []map[string]any{
			{
				"id":      "primary",
				"driver":  "postgres",
				"source":  source,
				"url":     redactDatabaseURL(dsn),
				"running": true,
			},
		},
	}, false, nil
}

func (s *dashboardServer) mcpQueryDatabase(ctx context.Context, session *mcpSession, args map[string]any) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, true, err
	}
	var req devdash.QueryRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, true, err
	}
	req.AppID = status.AppID
	rows, err := s.queryDB(ctx, req)
	if err != nil {
		return nil, true, err
	}
	return map[string]any{"rows": rows}, false, nil
}

func (s *dashboardServer) mcpMetadataSection(ctx context.Context, session *mcpSession, args map[string]any, key string) (any, bool, error) {
	status, err := s.mcpStatus(ctx, session, args)
	if err != nil {
		return nil, true, err
	}
	meta, err := decodeRawJSON(status.Meta)
	if err != nil {
		return nil, true, err
	}
	payload, _ := meta.(map[string]any)
	return payload[key], false, nil
}

func (s *dashboardServer) mcpStatus(ctx context.Context, session *mcpSession, args map[string]any) (devdash.AppStatus, error) {
	appID := firstNonEmpty(stringArg(args, "app_id"), stringArg(args, "app"), session.appID, s.supervisor.activeAppID())
	return s.supervisor.statusFor(ctx, appID)
}

func (s *dashboardServer) addMCPSession(session *mcpSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpSessions[session.id] = session
}

func (s *dashboardServer) getMCPSession(id string) *mcpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpSessions[id]
}

func (s *dashboardServer) removeMCPSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mcpSessions, id)
}

func (s *mcpSession) send(resp *rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	select {
	case s.outgoing <- data:
		return nil
	default:
		return fmt.Errorf("mcp session is backlogged")
	}
}

func mcpErrorResponse(id any, err error) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    -32602,
			Message: err.Error(),
		},
	}
}

func mcpToolResult(payload any, isError bool) map[string]any {
	text := ""
	if data, err := json.MarshalIndent(payload, "", "  "); err == nil {
		text = string(data)
	} else {
		text = fmt.Sprint(payload)
	}
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": text,
			},
		},
		"structuredContent": payload,
		"isError":           isError,
	}
}

func writeSSEEvent(w http.ResponseWriter, event, data string) error {
	if _, err := io.WriteString(w, "event: "+event+"\n"); err != nil {
		return err
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := io.WriteString(w, "data: "+line+"\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func decodeRawJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceArg(args map[string]any, key string) []string {
	raw, _ := args[key].([]any)
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		value, _ := item.(string)
		value = strings.TrimSpace(value)
		if value != "" {
			items = append(items, value)
		}
	}
	return items
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return fallback
	}
}

func redactDatabaseURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username != "" {
			parsed.User = url.UserPassword(username, "xxxxx")
		}
	}
	return parsed.String()
}

func secretLookupKeys(fieldName string) []string {
	keys := []string{fieldName}
	alt := toEnvKey(fieldName)
	if alt != "" && alt != fieldName {
		keys = append(keys, alt)
	}
	return keys
}

func toEnvKey(name string) string {
	if name == "" {
		return ""
	}
	var out strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if i > 0 && shouldInsertUnderscore(runes[i-1], r, nextRune(runes, i)) {
			out.WriteByte('_')
		}
		out.WriteRune(r)
	}
	return strings.ToUpper(out.String())
}

func nextRune(runes []rune, index int) rune {
	if index+1 >= len(runes) {
		return 0
	}
	return runes[index+1]
}

func shouldInsertUnderscore(prev, current, next rune) bool {
	if current < 'A' || current > 'Z' {
		return false
	}
	prevLower := prev >= 'a' && prev <= 'z'
	prevDigit := prev >= '0' && prev <= '9'
	nextLower := next >= 'a' && next <= 'z'
	if prevLower || prevDigit {
		return true
	}
	return prev >= 'A' && prev <= 'Z' && nextLower
}
