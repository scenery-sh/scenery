package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The development runtime RPC bounds the work a client can start
// (docs/local-contract.md documents every value here). Work calls open the
// app's database or storage; control calls read runtime state. Each
// connection runs a bounded number of each class, work calls are also bounded
// per app across connections, and a call beyond a limit is refused at once:
// admission never waits, so nothing queues behind a slow statement.
type runtimeRPCLimits struct {
	connectionWork    int
	connectionControl int
	appWork           int
	controlDeadline   time.Duration
	workDeadline      time.Duration
	queryRows         int
	resultBytes       int
}

var defaultRuntimeRPCLimits = runtimeRPCLimits{
	connectionWork:    6,
	connectionControl: 4,
	appWork:           12,
	controlDeadline:   5 * time.Second,
	workDeadline:      30 * time.Second,
	queryRows:         5000,
	resultBytes:       4 << 20,
}

type runtimeCallClass string

const (
	runtimeControlCall runtimeCallClass = "control"
	runtimeWorkCall    runtimeCallClass = "work"
)

func runtimeCallClassOf(method string) runtimeCallClass {
	switch method {
	case "postgres/tables", "postgres/schema", "postgres/rows", "db/query":
		return runtimeWorkCall
	}
	if strings.HasPrefix(method, "storage/") {
		return runtimeWorkCall
	}
	return runtimeControlCall
}

func (l runtimeRPCLimits) queryBudget() runtimeResultBudget {
	return runtimeResultBudget{maxRows: l.queryRows, maxBytes: l.resultBytes, remedy: "narrow the statement, for example with LIMIT"}
}

// A page is already capped by its SQL LIMIT; only its size can exceed.
func (l runtimeRPCLimits) pageBudget(limit int) runtimeResultBudget {
	return runtimeResultBudget{maxRows: limit, maxBytes: l.resultBytes, remedy: "request fewer rows"}
}

// runtimeRPC is one backend's admission state: its limits and the work
// calls it runs per app across every connection.
type runtimeRPC struct {
	limits runtimeRPCLimits
	mu     sync.Mutex
	work   map[string]int
}

func newRuntimeRPC(limits runtimeRPCLimits) *runtimeRPC {
	return &runtimeRPC{limits: limits, work: map[string]int{}}
}

// runtimeConnectionSlots are one connection's two allowances. Work calls
// never occupy the control allowance, so status stays answerable while every
// work slot holds a slow statement.
type runtimeConnectionSlots struct {
	work    chan struct{}
	control chan struct{}
}

func (r *runtimeRPC) connectionSlots() runtimeConnectionSlots {
	return runtimeConnectionSlots{
		work:    make(chan struct{}, r.limits.connectionWork),
		control: make(chan struct{}, r.limits.connectionControl),
	}
}

// runtimeCall is one admitted request. It holds its slots until release,
// which runs after its answer is written.
type runtimeCall struct {
	deadline time.Duration
	release  func()
}

// admit reserves a connection slot of the request's class and, for work,
// one of its app's slots. The app is the request's app_id, or the backend's
// default app when it names none.
func (r *runtimeRPC) admit(slots runtimeConnectionSlots, req rpcRequest, defaultApp func() string) (runtimeCall, *runtimeRPCFailure) {
	class := runtimeCallClassOf(req.Method)
	connection, limit, deadline := slots.control, r.limits.connectionControl, r.limits.controlDeadline
	if class == runtimeWorkCall {
		connection, limit, deadline = slots.work, r.limits.connectionWork, r.limits.workDeadline
	}
	select {
	case connection <- struct{}{}:
	default:
		return runtimeCall{}, capacityExhausted(class, "connection", limit)
	}
	release := func() { <-connection }
	if class == runtimeWorkCall {
		app := runtimeRequestAppID(req.Params)
		if app == "" {
			app = defaultApp()
		}
		if !r.acquireAppWork(app) {
			release()
			return runtimeCall{}, capacityExhausted(class, "app", r.limits.appWork)
		}
		releaseConnection := release
		release = func() {
			r.releaseAppWork(app)
			releaseConnection()
		}
	}
	return runtimeCall{deadline: deadline, release: release}, nil
}

func (r *runtimeRPC) acquireAppWork(app string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.work[app] >= r.limits.appWork {
		return false
	}
	r.work[app]++
	return true
}

func (r *runtimeRPC) releaseAppWork(app string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.work[app] <= 1 {
		delete(r.work, app)
		return
	}
	r.work[app]--
}

// Admission reads only app_id; the method still validates its params.
func runtimeRequestAppID(raw json.RawMessage) string {
	var params struct {
		AppID string `json:"app_id"`
	}
	_ = json.Unmarshal(raw, &params)
	return strings.TrimSpace(params.AppID)
}

var errRuntimeCallDeadline = errors.New("development runtime call deadline")

// runtimeRPCFailure is a documented runtime failure. It is the failure object
// error.data carries, the same shape storage failures use.
type runtimeRPCFailure struct {
	Code       string `json:"code"`
	Diagnostic string `json:"diagnostic"`
	Message    string `json:"message"`
	Details    any    `json:"details,omitempty"`
}

func (f *runtimeRPCFailure) Error() string { return f.Diagnostic + ": " + f.Message }

type runtimeCapacityDetails struct {
	Class string `json:"class"`
	Scope string `json:"scope"`
	Limit int    `json:"limit"`
}

func capacityExhausted(class runtimeCallClass, scope string, limit int) *runtimeRPCFailure {
	calls, where := "control calls", "on this connection"
	if class == runtimeWorkCall {
		calls = "database or storage calls"
	}
	if scope == "app" {
		where = "for this app"
	}
	return &runtimeRPCFailure{
		Code:       "capacity_exhausted",
		Diagnostic: "SCN8011",
		Message:    fmt.Sprintf("the development runtime is already running %d %s %s; retry after one completes", limit, calls, where),
		Details:    runtimeCapacityDetails{Class: string(class), Scope: scope, Limit: limit},
	}
}

type runtimeDeadlineDetails struct {
	DeadlineMS int64 `json:"deadline_ms"`
}

func deadlineExceeded(method string, deadline time.Duration) *runtimeRPCFailure {
	return &runtimeRPCFailure{
		Code:       "deadline_exceeded",
		Diagnostic: "SCN8012",
		Message:    fmt.Sprintf("%s did not complete within its %s deadline", method, deadline),
		Details:    runtimeDeadlineDetails{DeadlineMS: deadline.Milliseconds()},
	}
}

// runtimeResultBudget bounds what one result may accumulate.
type runtimeResultBudget struct {
	maxRows  int
	maxBytes int
	remedy   string
}

type runtimeResultDetails struct {
	MaxRows          int `json:"max_rows"`
	MaxBytes         int `json:"max_bytes"`
	RowsWithinBudget int `json:"rows_within_budget"`
}

func resultTooLarge(budget runtimeResultBudget, rowsWithinBudget int) *runtimeRPCFailure {
	size := fmt.Sprintf("%d bytes", budget.maxBytes)
	if budget.maxBytes%(1<<20) == 0 {
		size = fmt.Sprintf("%d MiB", budget.maxBytes>>20)
	}
	return &runtimeRPCFailure{
		Code:       "result_too_large",
		Diagnostic: "SCN8013",
		Message:    fmt.Sprintf("the result exceeds %d rows or %s; %s", budget.maxRows, size, budget.remedy),
		Details:    runtimeResultDetails{MaxRows: budget.maxRows, MaxBytes: budget.maxBytes, RowsWithinBudget: rowsWithinBudget},
	}
}

// A failure that already classifies an interrupted call keeps its identity
// when the deadline caused it: a partially completed storage deletion keeps
// its progress instead of becoming a bare deadline.
func runtimeFailureDescribesOutcome(err error) bool {
	if _, ok := errors.AsType[*runtimeRPCFailure](err); ok {
		return true
	}
	if failure, ok := errors.AsType[*dashboardStorageFailure](err); ok {
		return failure.Code != "canceled"
	}
	return false
}
