package runtimeapp

import (
	"context"
	"time"
)

type AuthInfo struct {
	UID  string
	Data any
}

type authContextKey struct{}
type authContextValue struct {
	info    AuthInfo
	started time.Time
}

func CurrentAuth() *AuthInfo {
	if provider := application.provider.Load(); provider != nil {
		return provider.CurrentAuth()
	}
	return nil
}

func WithAuthContext(ctx context.Context, info AuthInfo) context.Context {
	if provider := application.provider.Load(); provider != nil {
		return provider.WithAuthContext(ctx, info)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Library initialization can create an auth context before the runtime is
	// initialized. Preserve its native value for the eventual request owner.
	return context.WithValue(ctx, authContextKey{}, authContextValue{info: info, started: time.Now()})
}

func AuthContextValue(ctx context.Context) (AuthInfo, time.Time, bool) {
	if ctx == nil {
		return AuthInfo{}, time.Time{}, false
	}
	value, ok := ctx.Value(authContextKey{}).(authContextValue)
	return value.info, value.started, ok
}
