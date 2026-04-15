package pgxpool

import (
	"context"

	"github.com/jackc/pgx/v5"
	stdpgxpool "github.com/jackc/pgx/v5/pgxpool"

	pulseruntime "pulse.dev/runtime"
)

type (
	Pool   = stdpgxpool.Pool
	Config = stdpgxpool.Config
	Conn   = stdpgxpool.Conn
	Stat   = stdpgxpool.Stat
)

func ParseConfig(connString string) (*Config, error) {
	cfg, err := stdpgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	instrumentConfig(cfg)
	return cfg, nil
}

func New(ctx context.Context, connString string) (*Pool, error) {
	cfg, err := ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	return stdpgxpool.NewWithConfig(ctx, cfg)
}

func NewWithConfig(ctx context.Context, cfg *Config) (*Pool, error) {
	instrumentConfig(cfg)
	return stdpgxpool.NewWithConfig(ctx, cfg)
}

func instrumentConfig(cfg *Config) {
	if cfg == nil || cfg.ConnConfig == nil {
		return
	}
	if _, ok := cfg.ConnConfig.Tracer.(*queryTracer); ok {
		return
	}
	cfg.ConnConfig.Tracer = &queryTracer{base: cfg.ConnConfig.Tracer}
}

type queryTracer struct {
	base pgx.QueryTracer
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.base != nil {
		ctx = t.base.TraceQueryStart(ctx, conn, data)
	}
	return pulseruntime.TraceDBQueryStart(ctx, data.SQL, len(data.Args))
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if t.base != nil {
		t.base.TraceQueryEnd(ctx, conn, data)
	}
	pulseruntime.TraceDBQueryEnd(ctx, data.CommandTag.String(), data.CommandTag.RowsAffected(), data.Err)
}

func (t *queryTracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if base, ok := t.base.(pgx.BatchTracer); ok {
		return base.TraceBatchStart(ctx, conn, data)
	}
	return ctx
}

func (t *queryTracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	if base, ok := t.base.(pgx.BatchTracer); ok {
		base.TraceBatchQuery(ctx, conn, data)
	}
}

func (t *queryTracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	if base, ok := t.base.(pgx.BatchTracer); ok {
		base.TraceBatchEnd(ctx, conn, data)
	}
}

func (t *queryTracer) TraceCopyFromStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if base, ok := t.base.(pgx.CopyFromTracer); ok {
		return base.TraceCopyFromStart(ctx, conn, data)
	}
	return ctx
}

func (t *queryTracer) TraceCopyFromEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if base, ok := t.base.(pgx.CopyFromTracer); ok {
		base.TraceCopyFromEnd(ctx, conn, data)
	}
}

func (t *queryTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if base, ok := t.base.(pgx.PrepareTracer); ok {
		return base.TracePrepareStart(ctx, conn, data)
	}
	return ctx
}

func (t *queryTracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	if base, ok := t.base.(pgx.PrepareTracer); ok {
		base.TracePrepareEnd(ctx, conn, data)
	}
}

func (t *queryTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if base, ok := t.base.(pgx.ConnectTracer); ok {
		return base.TraceConnectStart(ctx, data)
	}
	return ctx
}

func (t *queryTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if base, ok := t.base.(pgx.ConnectTracer); ok {
		base.TraceConnectEnd(ctx, data)
	}
}
