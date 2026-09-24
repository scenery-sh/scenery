package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/storagefs"
)

func TestRuntimeRPCBoundsSlowWorkKeepsStatusResponsiveAndDisconnectCancels(t *testing.T) {
	t.Parallel()

	database := newRuntimeTestDatabase()
	server := newRuntimeRPCTestServer(database)
	limits := server.rpc.limits
	url, returned := serveRuntimeRPC(t, server)
	slow := func(app string) map[string]any { return map[string]any{"app_id": app, "query": "slow"} }

	// One connection starts only its work allowance; the calls beyond it are
	// refused at once instead of waiting for a slot.
	first := dialRuntimeRPC(t, url)
	for id := 1; id <= limits.connectionWork+2; id++ {
		first.send(id, "db/query", slow("session-a"))
	}
	database.awaitStarted(t, limits.connectionWork)
	for id := limits.connectionWork + 1; id <= limits.connectionWork+2; id++ {
		first.expectRefusal(id, "connection", limits.connectionWork)
	}

	// The control allowance is reserved: status answers while every work
	// slot of the connection holds a statement that never finishes on its own.
	var slowest time.Duration
	for id := 100; id < 110; id++ {
		started := time.Now()
		first.send(id, "status", map[string]any{})
		if response := first.read(); response.ID != id || response.Error != nil {
			t.Fatalf("status %d = %+v", id, response)
		}
		slowest = max(slowest, time.Since(started))
	}
	if slowest > 250*time.Millisecond {
		t.Fatalf("status took %s while work calls were saturated", slowest)
	}

	// The app allowance spans connections; another app has its own.
	second := dialRuntimeRPC(t, url)
	for id := 1; id <= limits.appWork-limits.connectionWork; id++ {
		second.send(id, "db/query", slow("session-a"))
	}
	database.awaitStarted(t, limits.appWork-limits.connectionWork)
	third := dialRuntimeRPC(t, url)
	third.send(1, "db/query", slow("session-a"))
	third.expectRefusal(1, "app", limits.appWork)
	third.send(2, "db/query", slow("session-b"))
	database.awaitStarted(t, 1)
	third.send(3, "status", map[string]any{})
	if response := third.read(); response.ID != 3 || response.Error != nil {
		t.Fatalf("status beside a saturated app = %+v", response)
	}
	if active, _ := database.counts(); active != limits.appWork+1 {
		t.Fatalf("active statements = %d, want %d", active, limits.appWork+1)
	}

	// Closing a connection cancels exactly its statements and frees their
	// slots before its handler returns.
	first.close()
	database.awaitCanceled(t, limits.connectionWork)
	awaitRuntimeHandlers(t, returned, 1)
	if work := runtimeActiveWork(server.rpc); work["session-a"] != limits.appWork-limits.connectionWork || work["session-b"] != 1 {
		t.Fatalf("app work after the first disconnect = %v", work)
	}
	second.close()
	third.close()
	awaitRuntimeHandlers(t, returned, 2)
	active, connections := database.counts()
	if active != 0 || connections != 0 || len(runtimeActiveWork(server.rpc)) != 0 {
		t.Fatalf("after disconnect: %d statements, %d open connections, app work %v", active, connections, runtimeActiveWork(server.rpc))
	}
	if canceled := database.canceledCount(); canceled != limits.appWork+1 {
		t.Fatalf("canceled statements = %d, want %d", canceled, limits.appWork+1)
	}
}

func TestRuntimeRPCDeadlineCancelsTheCallAndAnswersDeadlineExceeded(t *testing.T) {
	t.Parallel()

	database := newRuntimeTestDatabase()
	server := newRuntimeRPCTestServer(database)
	server.rpc.limits.workDeadline = 20 * time.Millisecond
	request := runtimeTestRequest(1, "db/query", map[string]any{"app_id": "session-a", "query": "slow"})
	call, refusal := server.rpc.admit(server.rpc.connectionSlots(), request, server.dashboardActiveAppID)
	if refusal != nil {
		t.Fatal(refusal)
	}
	defer call.release()

	response := server.handleRPC(context.Background(), call, request)
	failure := runtimeResponseFailure(t, response)
	if failure.Diagnostic != "SCN8012" || failure.Code != "deadline_exceeded" || failure.Details.(runtimeDeadlineDetails).DeadlineMS != 20 {
		t.Fatalf("deadline failure = %+v", failure)
	}
	if response.Error.Code != -32000 || response.Error.Message != "SCN8012: db/query did not complete within its 20ms deadline" {
		t.Fatalf("deadline error = %+v", response.Error)
	}
	if database.canceledCount() != 1 {
		t.Fatal("the statement did not observe the deadline")
	}
	// A storage failure that already describes the interrupted outcome keeps
	// its identity; plain cancellation becomes the deadline.
	partial := &dashboardStorageFailure{storagefs.Failure{Code: "storage_partial_completion", Diagnostic: "SCN8010"}}
	canceled := &dashboardStorageFailure{storagefs.Failure{Code: "canceled", Diagnostic: "SCN8003"}}
	if !runtimeFailureDescribesOutcome(partial) || runtimeFailureDescribesOutcome(canceled) || runtimeFailureDescribesOutcome(context.DeadlineExceeded) {
		t.Fatal("deadline classification does not keep partial storage outcomes")
	}
}

func TestRuntimeRPCResultBudgetsFailAndCancelTheStatement(t *testing.T) {
	t.Parallel()

	database := newRuntimeTestDatabase()
	server := newRuntimeRPCTestServer(database)
	server.rpc.limits.queryRows = 3
	server.rpc.limits.resultBytes = 51 // two rows of `["` + 20 bytes + `"]` and the array
	ctx := context.Background()

	result, err := server.queryDB(ctx, runtimeQueryRequest{AppID: "session-a", Query: "rows 2 20"})
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(result.Rows); len(encoded) != 51 || len(result.Rows) != 2 {
		t.Fatalf("a result at the byte budget = %s (%d bytes)", encoded, len(encoded))
	}

	for _, tc := range []struct {
		name, query string
		read, fit   int
	}{
		{name: "rows", query: "rows 6 1", read: 4, fit: 3},
		{name: "bytes", query: "rows 6 20", read: 3, fit: 2},
	} {
		_, err := server.queryDB(ctx, runtimeQueryRequest{AppID: "session-a", Query: tc.query})
		failure, ok := errors.AsType[*runtimeRPCFailure](err)
		if !ok || failure.Diagnostic != "SCN8013" || failure.Code != "result_too_large" {
			t.Fatalf("%s: error = %v", tc.name, err)
		}
		if details := failure.Details.(runtimeResultDetails); details != (runtimeResultDetails{MaxRows: 3, MaxBytes: 51, RowsWithinBudget: tc.fit}) {
			t.Fatalf("%s: details = %+v", tc.name, details)
		}
		// The statement is canceled before its rows close, so the driver
		// stops instead of draining the rest of the result.
		if read, canceledFirst := database.lastRows(); read != tc.read || !canceledFirst {
			t.Fatalf("%s: read %d rows, canceled before close %v", tc.name, read, canceledFirst)
		}
	}

	// A table page keeps its row limit and fails on size, reporting how many
	// rows fit so a browser can request a smaller page.
	_, err = server.postgresRows(ctx, dashboardPostgresRowsRequest{AppID: "session-a", Table: "wide", Limit: 5})
	failure, ok := errors.AsType[*runtimeRPCFailure](err)
	if !ok || failure.Details.(runtimeResultDetails) != (runtimeResultDetails{MaxRows: 5, MaxBytes: 51, RowsWithinBudget: 2}) {
		t.Fatalf("postgres/rows over budget = %v", err)
	}
	if message := failure.Error(); message != "SCN8013: the result exceeds 5 rows or 51 bytes; request fewer rows" {
		t.Fatalf("postgres/rows message = %q", message)
	}
	if _, connections := database.counts(); connections != 0 {
		t.Fatalf("open connections after budget failures = %d", connections)
	}
}

func TestRuntimeRPCDocumentedLimitsAndCallClasses(t *testing.T) {
	t.Parallel()

	want := runtimeRPCLimits{
		connectionWork:    6,
		connectionControl: 4,
		appWork:           12,
		controlDeadline:   5 * time.Second,
		workDeadline:      30 * time.Second,
		queryRows:         5000,
		resultBytes:       4 * 1024 * 1024,
	}
	if defaultRuntimeRPCLimits != want {
		t.Fatalf("limits = %+v; change docs/local-contract.md with them", defaultRuntimeRPCLimits)
	}
	for method, class := range map[string]runtimeCallClass{
		"status": runtimeControlCall, "traces/clear": runtimeControlCall, "list-apps": runtimeControlCall,
		"postgres/tables": runtimeWorkCall, "postgres/schema": runtimeWorkCall, "postgres/rows": runtimeWorkCall,
		"db/query": runtimeWorkCall, "storage/inspect": runtimeWorkCall, "storage/delete-selection": runtimeWorkCall,
	} {
		if got := runtimeCallClassOf(method); got != class {
			t.Errorf("%s class = %s, want %s", method, got, class)
		}
	}
	if message := resultTooLarge(defaultRuntimeRPCLimits.queryBudget(), 5000).Error(); message != "SCN8013: the result exceeds 5000 rows or 4 MiB; narrow the statement, for example with LIMIT" {
		t.Fatalf("query budget message = %q", message)
	}
}

func newRuntimeRPCTestServer(database *runtimeTestDatabase) *dashboardServer {
	controller := runtimeRPCTestController{status: devdash.AppStatus{Running: true, AppID: "session-a", AppRoot: "/tmp/demo"}}
	return newDashboardServerWithControllerHooks(controller, "", "127.0.0.1:0", nil, dashboardServerHooks{
		openDatabase: func(context.Context, string) (*sql.DB, error) {
			return sql.OpenDB(runtimeTestConnector{database: database}), nil
		},
	})
}

// serveRuntimeRPC serves the WebSocket handler over loopback and reports
// every handler return.
func serveRuntimeRPC(t *testing.T, server *dashboardServer) (string, <-chan struct{}) {
	t.Helper()
	returned := make(chan struct{}, 8)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() { returned <- struct{}{} }()
		server.handleWebSocket(w, req)
	}))
	t.Cleanup(httpServer.Close)
	return "ws" + strings.TrimPrefix(httpServer.URL, "http"), returned
}

func runtimeActiveWork(rpc *runtimeRPC) map[string]int {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	work := make(map[string]int, len(rpc.work))
	for app, calls := range rpc.work {
		work[app] = calls
	}
	return work
}

func awaitRuntimeHandlers(t *testing.T, returned <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-returned:
		case <-time.After(2 * time.Second):
			t.Fatal("a closed connection's handler did not return")
		}
	}
}

func runtimeTestRequest(id int, method string, params any) rpcRequest {
	raw, _ := json.Marshal(params)
	return rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: raw}
}

func runtimeResponseFailure(t *testing.T, response rpcResponse) *runtimeRPCFailure {
	t.Helper()
	if response.Error == nil {
		t.Fatalf("response = %+v, want an error", response)
	}
	failure, ok := response.Error.Data.(*runtimeRPCFailure)
	if !ok {
		t.Fatalf("error data = %#v, want a runtime failure", response.Error.Data)
	}
	return failure
}

type runtimeTestClient struct {
	t    *testing.T
	conn *websocket.Conn
}

type runtimeTestResponse struct {
	ID    int `json:"id"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    *struct {
			Code       string         `json:"code"`
			Diagnostic string         `json:"diagnostic"`
			Details    map[string]any `json:"details"`
		} `json:"data"`
	} `json:"error"`
}

func dialRuntimeRPC(t *testing.T, url string) *runtimeTestClient {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &runtimeTestClient{t: t, conn: conn}
}

func (c *runtimeTestClient) send(id int, method string, params any) {
	c.t.Helper()
	if err := c.conn.WriteJSON(runtimeTestRequest(id, method, params)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *runtimeTestClient) read() runtimeTestResponse {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var response runtimeTestResponse
	if err := c.conn.ReadJSON(&response); err != nil {
		c.t.Fatal(err)
	}
	return response
}

func (c *runtimeTestClient) expectRefusal(id int, scope string, limit int) {
	c.t.Helper()
	response := c.read()
	if response.ID != id || response.Error == nil || response.Error.Code != -32000 || response.Error.Data == nil {
		c.t.Fatalf("response %d = %+v, want a capacity refusal", id, response)
	}
	data := response.Error.Data
	if data.Code != "capacity_exhausted" || data.Diagnostic != "SCN8011" || data.Details["scope"] != scope || data.Details["class"] != "work" || data.Details["limit"] != float64(limit) {
		c.t.Fatalf("refusal %d = %s %+v", id, response.Error.Message, *data)
	}
}

func (c *runtimeTestClient) close() { _ = c.conn.Close() }

// runtimeTestDatabase is a database/sql driver whose statements the tests
// control: "slow" blocks until its context ends; "rows N SIZE" answers N rows
// of one SIZE-byte text column; a statement naming the "wide" table answers
// its LIMIT argument's rows of 20 bytes.
type runtimeTestDatabase struct {
	mu           sync.Mutex
	active       int
	connections  int
	canceled     int
	started      chan struct{}
	canceledSlow chan struct{}
	rowsRead     int
	closedCancel bool
}

func newRuntimeTestDatabase() *runtimeTestDatabase {
	return &runtimeTestDatabase{started: make(chan struct{}, 64), canceledSlow: make(chan struct{}, 64)}
}

func (d *runtimeTestDatabase) counts() (active, connections int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active, d.connections
}

func (d *runtimeTestDatabase) canceledCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.canceled
}

func (d *runtimeTestDatabase) lastRows() (read int, canceledBeforeClose bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rowsRead, d.closedCancel
}

func (d *runtimeTestDatabase) awaitStarted(t *testing.T, count int) {
	t.Helper()
	awaitRuntimeTestSignals(t, d.started, count, "statements to start")
}

func (d *runtimeTestDatabase) awaitCanceled(t *testing.T, count int) {
	t.Helper()
	awaitRuntimeTestSignals(t, d.canceledSlow, count, "statements to be canceled")
}

func awaitRuntimeTestSignals(t *testing.T, signals <-chan struct{}, count int, what string) {
	t.Helper()
	for received := range count {
		select {
		case <-signals:
		case <-time.After(2 * time.Second):
			t.Fatalf("waited for %d %s, saw %d", count, what, received)
		}
	}
}

func (d *runtimeTestDatabase) slow(ctx context.Context) error {
	d.mu.Lock()
	d.active++
	d.mu.Unlock()
	d.started <- struct{}{}
	<-ctx.Done()
	d.mu.Lock()
	d.active--
	d.canceled++
	d.mu.Unlock()
	d.canceledSlow <- struct{}{}
	return ctx.Err()
}

type runtimeTestConnector struct{ database *runtimeTestDatabase }

func (c runtimeTestConnector) Connect(context.Context) (driver.Conn, error) {
	c.database.mu.Lock()
	c.database.connections++
	c.database.mu.Unlock()
	return &runtimeTestConn{database: c.database}, nil
}

func (runtimeTestConnector) Driver() driver.Driver { return runtimeTestDriver{} }

type runtimeTestDriver struct{}

func (runtimeTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use the connector")
}

type runtimeTestConn struct{ database *runtimeTestDatabase }

func (c *runtimeTestConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not needed") }
func (c *runtimeTestConn) Begin() (driver.Tx, error)           { return nil, errors.New("not needed") }

func (c *runtimeTestConn) Close() error {
	c.database.mu.Lock()
	defer c.database.mu.Unlock()
	c.database.connections--
	return nil
}

func (c *runtimeTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if query == "slow" {
		return nil, c.database.slow(ctx)
	}
	count, size := 0, 20
	if strings.Contains(query, `"wide"`) {
		count = int(args[0].Value.(int64))
	} else {
		fields := strings.Fields(query)
		count, _ = strconv.Atoi(fields[1])
		size, _ = strconv.Atoi(fields[2])
	}
	return &runtimeTestRows{ctx: ctx, database: c.database, remaining: count, value: strings.Repeat("x", size)}, nil
}

type runtimeTestRows struct {
	ctx       context.Context
	database  *runtimeTestDatabase
	remaining int
	read      int
	value     string
}

func (r *runtimeTestRows) Columns() []string { return []string{"value"} }

func (r *runtimeTestRows) Close() error {
	r.database.mu.Lock()
	defer r.database.mu.Unlock()
	r.database.rowsRead, r.database.closedCancel = r.read, r.ctx.Err() != nil
	return nil
}

func (r *runtimeTestRows) Next(dest []driver.Value) error {
	if r.read == r.remaining {
		return io.EOF
	}
	r.read++
	dest[0] = []byte(r.value)
	return nil
}
