package postgresdb

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"scenery.sh/internal/appsdk"
)

type queryTraceHost struct {
	appsdk.Host
	query          string
	args           int
	started, ended context.Context
	tag            string
	rows           int64
	err            error
}

func (h *queryTraceHost) TraceDBQueryStart(ctx context.Context, query string, args int) context.Context {
	h.query, h.args = query, args
	h.started = context.WithValue(ctx, h, true)
	return h.started
}

func (h *queryTraceHost) TraceDBQueryEnd(ctx context.Context, tag string, rows int64, err error) {
	h.ended, h.tag, h.rows, h.err = ctx, tag, rows, err
}

func TestPostgresConnectorTracesThroughRuntimeHost(t *testing.T) {
	previous := appsdk.CurrentHost()
	t.Cleanup(func() { appsdk.RegisterHost(previous) })
	config, err := tracedConnectionConfig("postgres://localhost/app?search_path=reports,scenery")
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeParams["search_path"] != "reports,scenery" {
		t.Fatal("lost schema binding")
	}
	if config.Tracer == nil {
		t.Fatal("connector has no query tracer")
	}
	host := &queryTraceHost{}
	appsdk.RegisterHost(host)
	ctx := config.Tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "-- name: List :many\nSELECT $1", Args: []any{"private-value"}})
	config.Tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 3")})
	if host.query != "-- name: List :many\nSELECT $1" || host.args != 1 || host.ended != host.started || host.tag != "SELECT 3" || host.rows != 3 || host.err != nil {
		t.Fatalf("query trace = %+v", host)
	}
	failure := errors.New("query failed")
	config.Tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: failure})
	if host.rows != -1 || !errors.Is(host.err, failure) {
		t.Fatalf("error trace = %+v", host)
	}
	appsdk.RegisterHost(nil)
	parent := context.Background()
	if got := config.Tracer.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{}); got != parent {
		t.Fatal("no-host tracing changed context")
	}
	config.Tracer.TraceQueryEnd(parent, nil, pgx.TraceQueryEndData{})
}
