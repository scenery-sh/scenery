package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

var dashboardUpgrader = websocket.Upgrader{
	CheckOrigin: dashboardCheckOrigin,
}

func dashboardCheckOrigin(req *http.Request) bool {
	if req == nil {
		return false
	}
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.EqualFold(u.Host, req.Host)
}

type dashboardServer struct {
	controller   dashboardController
	supervisor   *devSupervisor
	http         *http.Server
	addr         string
	state        dashboardRunState
	logExporter  func(*devdash.LogEvent)
	openDatabase func(context.Context, string) (*sql.DB, error)
	rpc          *runtimeRPC
	connections  runtimeConnections

	traces *dashboardTraceEventBuffer
}

type dashboardServerHooks struct {
	exportLogEvent func(*devdash.LogEvent)
	// openDatabase opens an app root's development database.
	openDatabase func(context.Context, string) (*sql.DB, error)
}

type dashboardVictoria interface {
	QueryTraceSummaries(context.Context, devdash.TraceQuery) ([]*devdash.TraceSummary, error)
	ListDevEvents(context.Context, devdash.DevEventQuery) ([]devdash.DevEvent, error)
	MarkCleared(string, time.Time)
	URLs() map[string]string
	Endpoint(string) string
}

type dashboardController interface {
	dashboardActiveAppID() string
	dashboardCurrentSessionID() string
	dashboardStatusFor(context.Context, string) (devdash.AppStatus, error)
	dashboardStore() *devdash.Store
	dashboardAuthorizeReport(*http.Request, devdash.ReportEnvelope) dashboardReportAuth
	dashboardVictoria() dashboardVictoria
}

type dashboardReportAuth struct {
	Authorized bool
	Reason     string
}

func (s *dashboardServer) dashboardActiveAppID() string {
	if s == nil || s.controller == nil {
		return ""
	}
	return s.controller.dashboardActiveAppID()
}

func (s *dashboardServer) dashboardCurrentSessionID() string {
	if s == nil || s.controller == nil {
		return ""
	}
	return s.controller.dashboardCurrentSessionID()
}

func (s *dashboardServer) dashboardStatusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	if s == nil || s.controller == nil {
		return devdash.AppStatus{}, fmt.Errorf("dashboard controller unavailable")
	}
	return s.controller.dashboardStatusFor(ctx, appID)
}

func (s *dashboardServer) dashboardStore() *devdash.Store {
	if s == nil || s.controller == nil {
		return nil
	}
	return s.controller.dashboardStore()
}

func (s *dashboardServer) dashboardAuthorizeReport(req *http.Request, report devdash.ReportEnvelope) dashboardReportAuth {
	if s == nil || s.controller == nil {
		return dashboardReportAuth{Reason: "controller-unavailable"}
	}
	return s.controller.dashboardAuthorizeReport(req, report)
}

func (s *dashboardServer) dashboardVictoria() dashboardVictoria {
	if s == nil || s.controller == nil {
		return nil
	}
	return s.controller.dashboardVictoria()
}

func dashboardStoreAppID(status devdash.AppStatus) string {
	return firstNonEmpty(status.BaseAppID, status.AppID)
}

func newDashboardServer(supervisor *devSupervisor) *dashboardServer {
	return newDashboardServerWithController(supervisor, supervisor.root, devdash.ListenAddr(), supervisor)
}

func newDashboardServerWithController(controller dashboardController, root, addr string, supervisor *devSupervisor) *dashboardServer {
	return newDashboardServerWithControllerHooks(controller, root, addr, supervisor, dashboardServerHooks{})
}

// The dashboard listener is the worktree's runtime control backend: the
// development runtime RPC, storage transfers, report intake and the
// supervisor control plane. It serves no browser UI.
func newDashboardServerWithControllerHooks(controller dashboardController, root, addr string, supervisor *devSupervisor, hooks dashboardServerHooks) *dashboardServer {
	s := &dashboardServer{
		controller:   controller,
		supervisor:   supervisor,
		addr:         addr,
		state:        newDashboardRunState(root, addr),
		traces:       newDashboardTraceEventBuffer(),
		openDatabase: hooks.openDatabase,
		rpc:          newRuntimeRPC(defaultRuntimeRPCLimits),
	}
	s.logExporter = hooks.exportLogEvent
	if s.logExporter == nil {
		s.logExporter = s.exportVictoriaLogEvent
	}
	if s.openDatabase == nil {
		s.openDatabase = openPostgresDashboardDB
	}
	mux := http.NewServeMux()
	mux.HandleFunc(devdash.WebSocketPath, s.handleWebSocket)
	mux.HandleFunc(dashboardStoragePath, s.handleStorageTransfer)
	mux.HandleFunc(devdash.ReportPath, s.handleReport)
	mux.HandleFunc(dashboardControlPlanePath, s.handleControlPlane)
	s.http = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	// Shutdown, like Close, leaves hijacked RPC WebSockets open.
	s.http.RegisterOnShutdown(s.connections.closeAll)
	return s
}

func (s *dashboardServer) Start(ctx context.Context) error {
	addr := s.addr
	if err := ensureDashboardPortAvailable(addr, s.state); err != nil {
		return fmt.Errorf("scenery dashboard failed to listen on %s: %w", addr, err)
	}
	ln, err := netListen("tcp", addr)
	if err != nil {
		return fmt.Errorf("scenery dashboard failed to listen on %s: %w", addr, err)
	}
	return s.startListener(ctx, ln)
}

// startListener consumes a listener already held by the worktree supervisor.
func (s *dashboardServer) startListener(ctx context.Context, ln net.Listener) error {
	s.addr = ln.Addr().String()
	s.state.DashboardAddr = s.addr
	if err := s.state.write(); err != nil {
		_ = ln.Close()
		return fmt.Errorf("scenery dashboard failed to persist run state: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = s.http.Shutdown(context.Background())
	}()
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("scenery dashboard server failed", "err", err)
		}
	}()
	return nil
}

type procInfo struct {
	pid  int
	ppid int
	stat string
	cmd  string
}

// stopRecordedOwner interrupts a recorded process and escalates only while
// the same recorded identity still verifies.
func stopRecordedOwner(owner localagent.Owner, grace time.Duration) error {
	proc, err := os.FindProcess(owner.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if localagent.VerifyOwner(owner) != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if localagent.VerifyOwner(owner) != nil {
		return nil
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
	// Closing the hijacked RPC WebSockets cancels their calls; Close returns
	// once every handler has.
	s.connections.closeAll()
	s.connections.wait()
	if stateErr := s.state.remove(); stateErr != nil {
		return errors.Join(err, stateErr)
	}
	return err
}

// runtimeRPCMaxRequestBytes bounds one development runtime RPC request
// message (docs/local-contract.md). db/query statements and their params are
// the largest legitimate requests. The generated client's
// DEV_RUNTIME_MAX_REQUEST_BYTES is the same limit; it refuses a larger call
// before sending it.
const runtimeRPCMaxRequestBytes = 1 << 20

func (s *dashboardServer) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := dashboardUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	// A connection upgraded after the backend closed never serves a call.
	if !s.connections.add(conn) {
		closeRuntimeConnection(conn)
		return
	}
	defer s.connections.remove(conn)
	// Closing the connection cancels every call it admitted; the handler
	// returns only after each of them has finished.
	ctx, cancel := context.WithCancel(req.Context())
	client := &dashboardClient{conn: conn}
	slots := s.rpc.connectionSlots()
	var calls sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.Close()
		calls.Wait()
	}()
	// A larger request is not read beyond the limit. Its id stays unread, so
	// no answer can reach its caller: the read fails and the connection closes
	// with 1009 (message too big), ending every call it still runs.
	conn.SetReadLimit(runtimeRPCMaxRequestBytes)

	for {
		var reqMsg rpcRequest
		if err := conn.ReadJSON(&reqMsg); err != nil {
			return
		}
		// A call beyond its connection's or app's allowance is refused before
		// the next request is read; nothing waits for a slot.
		call, refusal := s.rpc.admit(slots, reqMsg, s.dashboardActiveAppID)
		if refusal != nil {
			if reqMsg.ID == nil {
				continue
			}
			if err := client.writeJSON(rpcErrorResponse(reqMsg.ID, refusal)); err != nil {
				return
			}
			continue
		}
		// Admitted calls on one connection run concurrently, so a slow query
		// does not hold back a status poll; responses carry their request id.
		// A call keeps its slots until its answer is written.
		calls.Go(func() {
			defer call.release()
			resp := s.handleRPC(ctx, call, reqMsg)
			if reqMsg.ID == nil {
				return
			}
			if err := client.writeJSON(resp); err != nil {
				_ = conn.Close()
			}
		})
	}
}

func (s *dashboardServer) handleReport(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer func() { _ = req.Body.Close() }()
	var report devdash.ReportEnvelope
	if err := json.NewDecoder(req.Body).Decode(&report); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	auth := s.dashboardAuthorizeReport(req, report)
	if !auth.Authorized {
		s.recordRejectedReport(req.Context(), report, auth.Reason)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if report.AppID == "" {
		report.AppID = s.dashboardActiveAppID()
	}
	if report.SessionID == "" {
		report.SessionID = s.dashboardCurrentSessionID()
	}
	switch report.Type {
	case "trace-summary":
		if report.TraceSummary != nil {
			report.TraceSummary.AppID = report.AppID
			if report.TraceSummary.SessionID == "" {
				report.TraceSummary.SessionID = report.SessionID
			}
			fillTraceSummaryIdentity(report.TraceSummary, report)
			events := s.drainBufferedTraceEvents(report.TraceSummary)
			go s.exportVictoriaTraceSummaryWithEvents(context.Background(), report.TraceSummary, events)
		}
	case "trace-event":
		if report.TraceEvent != nil {
			report.TraceEvent.AppID = report.AppID
			if report.TraceEvent.SessionID == "" {
				report.TraceEvent.SessionID = report.SessionID
			}
			fillTraceEventIdentity(report.TraceEvent, report)
			s.bufferTraceEvent(report.TraceEvent)
		}
	case "log":
		if report.LogEvent != nil {
			report.LogEvent.AppID = report.AppID
			if report.LogEvent.SessionID == "" {
				report.LogEvent.SessionID = report.SessionID
			}
			fillLogEventIdentity(report.LogEvent, report)
			go s.logExporter(report.LogEvent)
		}
	case "internal-failure":
		// An application process minted a report token; keep its cause where
		// `scenery inspect report` reads it.
		recordRuntimeFailureReport(report)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *dashboardServer) recordRejectedReport(ctx context.Context, report devdash.ReportEnvelope, reason string) {
	_ = ctx
	appID := firstNonEmpty(report.AppID, s.dashboardActiveAppID())
	sessionID := firstNonEmpty(report.SessionID, s.dashboardCurrentSessionID())
	event := &devdash.LogEvent{
		AppID:       appID,
		SessionID:   sessionID,
		AppRootHash: report.AppRootHash,
		Branch:      report.Branch,
		Worktree:    report.Worktree,
		Level:       "warn",
		Message:     "stale or unauthorized dev report rejected",
		Attrs: map[string]any{
			"kind":         "dev-report-rejected",
			"reason":       firstNonEmpty(reason, "unauthorized"),
			"report_type":  report.Type,
			"reporter_pid": report.ReporterPID,
		},
		Timestamp: time.Now().UTC(),
	}
	go s.logExporter(event)
}

func fillTraceSummaryIdentity(summary *devdash.TraceSummary, report devdash.ReportEnvelope) {
	if summary == nil {
		return
	}
	if summary.AppRootHash == "" {
		summary.AppRootHash = report.AppRootHash
	}
	if summary.Branch == "" {
		summary.Branch = report.Branch
	}
	if summary.Worktree == "" {
		summary.Worktree = report.Worktree
	}
}

func fillTraceEventIdentity(event *devdash.TraceEvent, report devdash.ReportEnvelope) {
	if event == nil {
		return
	}
	if event.AppRootHash == "" {
		event.AppRootHash = report.AppRootHash
	}
	if event.Branch == "" {
		event.Branch = report.Branch
	}
	if event.Worktree == "" {
		event.Worktree = report.Worktree
	}
}

func fillLogEventIdentity(event *devdash.LogEvent, report devdash.ReportEnvelope) {
	if event == nil {
		return
	}
	if event.AppRootHash == "" {
		event.AppRootHash = report.AppRootHash
	}
	if event.Branch == "" {
		event.Branch = report.Branch
	}
	if event.Worktree == "" {
		event.Worktree = report.Worktree
	}
}

// queryDB runs one statement and answers its columns in select order with
// every row as a value array, like postgres/rows, within the query budget.
func (s *dashboardServer) queryDB(ctx context.Context, req runtimeQueryRequest) (runtimeQueryResult, error) {
	db, err := s.openDashboardPostgres(ctx, req.AppID)
	if err != nil {
		return runtimeQueryResult{}, err
	}
	defer func() { _ = db.Close() }()
	columns, rows, err := queryRuntimeRows(ctx, db, s.rpc.limits.queryBudget(), req.Query, req.Params...)
	return runtimeQueryResult{Columns: columns, Rows: rows}, err
}

// A call whose context ends while pgx waits for its statement closes the
// connection, and pgx then sends PostgreSQL a cancel request: a disconnect or
// deadline also stops the statement on the server.
func openPostgresDashboardDB(ctx context.Context, root string) (*sql.DB, error) {
	appRoot, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		return nil, err
	}
	database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(database.URL) == "" {
		return nil, fmt.Errorf("no postgres database discovered")
	}
	return openPostgresDatabase(ctx, database.URL)
}

// queryRuntimeRows runs one statement and scans it within budget. The
// statement is canceled before its rows close (deferred calls run in reverse),
// so an exceeded budget stops the result on the server; closing alone would
// make the driver drain the rest of it.
func queryRuntimeRows(ctx context.Context, db *sql.DB, budget runtimeResultBudget, query string, args ...any) ([]string, []json.RawMessage, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	defer cancel()
	return scanRuntimeRows(rows, budget)
}

// scanRuntimeRows reads rows as JSON value arrays in column order; text-like
// bytes become strings. It counts the exact encoded size of the rows array
// and fails at the first row beyond the budget instead of accumulating it.
// It never answers a nil row list.
func scanRuntimeRows(rows *sql.Rows, budget runtimeResultBudget) ([]string, []json.RawMessage, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	out := []json.RawMessage{}
	size := len("[]")
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if len(out) == budget.maxRows {
			return nil, nil, resultTooLarge(budget, len(out))
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, nil, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, nil, err
		}
		size += len(encoded)
		if len(out) > 0 {
			size++
		}
		if size > budget.maxBytes {
			return nil, nil, resultTooLarge(budget, len(out))
		}
		out = append(out, encoded)
	}
	return columns, out, rows.Err()
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
	Data    any    `json:"data,omitempty"`
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
