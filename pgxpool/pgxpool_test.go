package pgxpool

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestParseConfigInjectsPulseTracer(t *testing.T) {
	cfg, err := ParseConfig("postgres://pulse:pulse@localhost/pulse?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if _, ok := cfg.ConnConfig.Tracer.(*queryTracer); !ok {
		t.Fatalf("expected pulse query tracer, got %T", cfg.ConnConfig.Tracer)
	}
}

func TestInstrumentConfigIsIdempotent(t *testing.T) {
	cfg, err := ParseConfig("postgres://pulse:pulse@localhost/pulse?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	first := cfg.ConnConfig.Tracer
	instrumentConfig(cfg)
	if cfg.ConnConfig.Tracer != first {
		t.Fatalf("instrumentConfig wrapped tracer twice: first=%T second=%T", first, cfg.ConnConfig.Tracer)
	}
}

func TestQueryTracerDelegatesToBaseTracer(t *testing.T) {
	base := &fakeQueryTracer{}
	tracer := &queryTracer{base: base}

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT 1",
		Args: []any{1},
	})
	if got := ctx.Value(fakeTracerKey("start")); got != "ok" {
		t.Fatalf("start context value = %#v, want %q", got, "ok")
	}

	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if base.starts != 1 {
		t.Fatalf("base starts = %d, want 1", base.starts)
	}
	if base.ends != 1 {
		t.Fatalf("base ends = %d, want 1", base.ends)
	}
}

type fakeTracerKey string

type fakeQueryTracer struct {
	starts int
	ends   int
}

func (f *fakeQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	f.starts++
	return context.WithValue(ctx, fakeTracerKey("start"), "ok")
}

func (f *fakeQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	f.ends++
}
