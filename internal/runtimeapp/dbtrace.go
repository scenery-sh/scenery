package runtimeapp

import (
	"context"
	"sync/atomic"
)

type DBTraceProvider struct {
	Start func(context.Context, string, int) context.Context
	End   func(context.Context, string, int64, error)
}

var dbTraceOwner atomic.Pointer[DBTraceProvider]

func BindDBTrace(provider DBTraceProvider) {
	if provider.Start == nil || provider.End == nil {
		panic("scenery: incomplete database tracing owner")
	}
	if !dbTraceOwner.CompareAndSwap(nil, &provider) {
		panic("scenery: database tracing owner already bound")
	}
}

func TraceDBQueryStart(ctx context.Context, query string, argsCount int) context.Context {
	if owner := dbTraceOwner.Load(); owner != nil {
		return owner.Start(ctx, query, argsCount)
	}
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func TraceDBQueryEnd(ctx context.Context, commandTag string, rowsAffected int64, err error) {
	if owner := dbTraceOwner.Load(); owner != nil {
		owner.End(ctx, commandTag, rowsAffected, err)
	}
}
