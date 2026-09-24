package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

// The documented development runtime RPC bounds (docs/local-contract.md).
const (
	runtimeBoundsConnectionWork = 6
	runtimeBoundsAppWork        = 12
)

// rpcBounds proves the development runtime RPC's execution bounds against the
// worktree's real PostgreSQL through the app origin's /runtime: admission
// beyond the connection and app limits is refused at once, status stays fast
// while every slot holds a sleeping statement, an oversized result fails
// without streaming it, and closing the connections cancels their statements
// on the server.
func (p *worktreeRuntimeProbe) rpcBounds(root string, runtime detachedDevResult) error {
	return p.scenario("A19", "development runtime RPC bounds slow SQL, keeps status responsive and cancels statements on disconnect", func(e map[string]any) error {
		record, err := p.record(root)
		if err != nil {
			return err
		}
		observer, err := postgresdb.Open(p.ctx, worktreePostgresURL(record.Postgres, postgresname.DatabaseNameFor(record.AppID, record.AppRoot)))
		if err != nil {
			return fmt.Errorf("open probe database for statement observation: %w", err)
		}
		defer func() { _ = observer.Close() }()
		tag := "scenery-a19-" + strings.ToLower(rand.Text())
		budgetTag, sleepTag := tag+"-budget", tag+"-sleep"
		url := "ws" + strings.TrimPrefix(strings.TrimRight(runtime.Session.RouteManifest.BaseURL, "/"), "http") + "/runtime"
		app := runtime.Session.SessionID
		query := func(sql string) map[string]any { return map[string]any{"app_id": app, "query": sql} }
		var connections []*runtimeBoundsConnection
		defer func() {
			for _, connection := range connections {
				connection.close()
			}
		}()
		dial := func() (*runtimeBoundsConnection, error) {
			connection, err := dialRuntimeBounds(p.ctx, url)
			if err == nil {
				connections = append(connections, connection)
			}
			return connection, err
		}

		// An oversized result fails fast and its statement stops on the server.
		budget, err := dial()
		if err != nil {
			return err
		}
		started := time.Now()
		response, err := budget.call(1, "db/query", query("select generate_series(1, 100000000) /* "+budgetTag+" */"), 10*time.Second)
		if err != nil {
			return err
		}
		if !response.failed("SCN8013", "result_too_large") || response.Error.Data.Details["rows_within_budget"] != float64(5000) {
			return fmt.Errorf("an oversized db/query answered %s", response.describe())
		}
		elapsed := time.Since(started)
		e["result_budget_ms"] = elapsed.Milliseconds()
		if elapsed > 5*time.Second {
			return fmt.Errorf("the oversized result took %s to fail", elapsed)
		}
		if err := waitStatements(p.ctx, observer, budgetTag, 0, 5*time.Second); err != nil {
			return fmt.Errorf("the statement stopped at its budget kept running: %w", err)
		}

		// One connection starts only its allowance and refuses the rest at once.
		sleep := query("select pg_sleep(20) /* " + sleepTag + " */")
		first, err := dial()
		if err != nil {
			return err
		}
		for id := 1; id <= runtimeBoundsConnectionWork+2; id++ {
			if err := first.send(id, "db/query", sleep); err != nil {
				return err
			}
		}
		for id := runtimeBoundsConnectionWork + 1; id <= runtimeBoundsConnectionWork+2; id++ {
			response, err := first.read(5 * time.Second)
			if err != nil {
				return err
			}
			if response.ID != id || !response.refused("connection", runtimeBoundsConnectionWork) {
				return fmt.Errorf("call %d beyond the connection allowance answered %s", id, response.describe())
			}
		}
		if err := waitStatements(p.ctx, observer, sleepTag, runtimeBoundsConnectionWork, 10*time.Second); err != nil {
			return err
		}

		// The app allowance spans connections.
		second, err := dial()
		if err != nil {
			return err
		}
		for id := 1; id <= runtimeBoundsAppWork-runtimeBoundsConnectionWork; id++ {
			if err := second.send(id, "db/query", sleep); err != nil {
				return err
			}
		}
		if err := waitStatements(p.ctx, observer, sleepTag, runtimeBoundsAppWork, 10*time.Second); err != nil {
			return err
		}
		third, err := dial()
		if err != nil {
			return err
		}
		response, err = third.call(1, "db/query", sleep, 5*time.Second)
		if err != nil {
			return err
		}
		if !response.refused("app", runtimeBoundsAppWork) {
			return fmt.Errorf("a work call beyond the app allowance answered %s", response.describe())
		}

		// Status keeps its own allowance while every work slot is asleep.
		var latencies []time.Duration
		for id := 2; id < 22; id++ {
			started := time.Now()
			response, err := third.call(id, "status", map[string]any{"app_id": app}, 5*time.Second)
			if err != nil {
				return err
			}
			if response.Error != nil {
				return fmt.Errorf("status during saturation answered %s", response.describe())
			}
			latencies = append(latencies, time.Since(started))
		}
		slices.Sort(latencies)
		e["status_calls"], e["status_ms_p50"], e["status_ms_max"] = len(latencies), latencies[len(latencies)/2].Milliseconds(), latencies[len(latencies)-1].Milliseconds()
		if latencies[len(latencies)-1] > 250*time.Millisecond {
			return fmt.Errorf("status took %s while the app's work calls were saturated", latencies[len(latencies)-1])
		}
		if count, err := countStatements(p.ctx, observer, sleepTag); err != nil || count != runtimeBoundsAppWork {
			return fmt.Errorf("sleeping statements after the status calls = %d (%v), want %d", count, err, runtimeBoundsAppWork)
		}
		e["connection_work_limit"], e["app_work_limit"], e["sleeping_statements"] = runtimeBoundsConnectionWork, runtimeBoundsAppWork, runtimeBoundsAppWork

		// Disconnecting cancels every statement on the server, long before its
		// 20 s sleep ends, and frees the app's allowance.
		disconnected := time.Now()
		for _, connection := range connections {
			connection.close()
		}
		if err := waitStatements(p.ctx, observer, sleepTag, 0, 5*time.Second); err != nil {
			return fmt.Errorf("closed connections left statements running: %w", err)
		}
		e["disconnect_cleanup_ms"] = time.Since(disconnected).Milliseconds()
		after, err := dial()
		if err != nil {
			return err
		}
		response, err = after.call(1, "db/query", query("select 1 /* "+tag+"-after */"), 10*time.Second)
		if err != nil {
			return err
		}
		if response.Error != nil {
			return fmt.Errorf("a work call after the disconnect answered %s", response.describe())
		}
		return nil
	})
}

func countStatements(ctx context.Context, observer *sql.DB, tag string) (int, error) {
	var count int
	err := observer.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND pid <> pg_backend_pid() AND position($1 in query) > 0`, tag).Scan(&count)
	return count, err
}

func waitStatements(ctx context.Context, observer *sql.DB, tag string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		count, err := countStatements(ctx, observer, tag)
		if err != nil {
			return err
		}
		if count == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d active statements tagged %s after %s, want %d", count, tag, timeout, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type runtimeBoundsConnection struct {
	conn *websocket.Conn
}

type runtimeBoundsResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    *struct {
			Code       string         `json:"code"`
			Diagnostic string         `json:"diagnostic"`
			Details    map[string]any `json:"details"`
		} `json:"data"`
	} `json:"error"`
}

func dialRuntimeBounds(ctx context.Context, url string) (*runtimeBoundsConnection, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, response, err := websocket.DefaultDialer.DialContext(dialCtx, url, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", url, err)
	}
	return &runtimeBoundsConnection{conn: conn}, nil
}

func (c *runtimeBoundsConnection) send(id int, method string, params any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return c.conn.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: encoded})
}

func (c *runtimeBoundsConnection) read(timeout time.Duration) (runtimeBoundsResponse, error) {
	var response runtimeBoundsResponse
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return response, err
	}
	err := c.conn.ReadJSON(&response)
	return response, err
}

// call sends one request and reads until its answer; other answers on the
// connection are ignored.
func (c *runtimeBoundsConnection) call(id int, method string, params any, timeout time.Duration) (runtimeBoundsResponse, error) {
	if err := c.send(id, method, params); err != nil {
		return runtimeBoundsResponse{}, err
	}
	for {
		response, err := c.read(timeout)
		if err != nil || response.ID == id {
			return response, err
		}
	}
}

func (c *runtimeBoundsConnection) close() { _ = c.conn.Close() }

func (r runtimeBoundsResponse) failed(diagnostic, code string) bool {
	return r.Error != nil && r.Error.Code == -32000 && r.Error.Data != nil && r.Error.Data.Diagnostic == diagnostic && r.Error.Data.Code == code
}

func (r runtimeBoundsResponse) refused(scope string, limit int) bool {
	if !r.failed("SCN8011", "capacity_exhausted") {
		return false
	}
	details := r.Error.Data.Details
	return details["class"] == "work" && details["scope"] == scope && details["limit"] == float64(limit)
}

func (r runtimeBoundsResponse) describe() string {
	if r.Error == nil {
		return fmt.Sprintf("result %s", r.Result)
	}
	return fmt.Sprintf("error %d %q %+v", r.Error.Code, r.Error.Message, r.Error.Data)
}
