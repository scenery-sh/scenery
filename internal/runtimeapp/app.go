// Package runtimeapp is the application-facing boundary to process-local runtime
// state. It contains no server, scheduler, database driver or process lifecycle.
package runtimeapp

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime/shared"
)

// Provider is installed once by the native runtime during package initialization.
// Calls retain their original Go context and execute in the application process.
type Provider struct {
	Meta            func() *shared.AppMetadata
	CurrentRequest  func() *shared.Request
	StartSpan       func(context.Context, string) (context.Context, *Span)
	CurrentAuth     func() *AuthInfo
	WithAuthContext func(context.Context, AuthInfo) context.Context
}

type binding struct {
	provider atomic.Pointer[Provider]
}

var application binding
var initialMetadata = shared.AppMetadata{Environment: DefaultEnvironment()}

func Bind(provider Provider) {
	application.bind(provider)
}

func (binding *binding) bind(provider Provider) {
	if provider.Meta == nil || provider.CurrentRequest == nil || provider.StartSpan == nil || provider.CurrentAuth == nil || provider.WithAuthContext == nil {
		panic("scenery: incomplete application runtime provider")
	}
	if !binding.provider.CompareAndSwap(nil, &provider) {
		panic("scenery: application runtime provider is already bound")
	}
}

func Meta() *shared.AppMetadata {
	if provider := application.provider.Load(); provider != nil {
		return provider.Meta()
	}
	meta := initialMetadata
	return &meta
}

func CurrentRequest() *shared.Request {
	if provider := application.provider.Load(); provider != nil {
		return provider.CurrentRequest()
	}
	return &shared.Request{Type: shared.None}
}

func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	if provider := application.provider.Load(); provider != nil {
		return provider.StartSpan(ctx, name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx, &Span{}
}

func DefaultEnvironment() shared.Environment {
	if strings.EqualFold(strings.TrimSpace(envpolicy.Get("SCENERY_RUNTIME_ENV")), "test") {
		return shared.Environment{Name: "test", Type: shared.EnvTest, Cloud: shared.CloudLocal}
	}
	return shared.Environment{Name: "local", Type: shared.EnvDevelopment, Cloud: shared.CloudLocal}
}

// Span owns one native completion callback. Its zero value and nil pointer are
// safe to end; a completed span cannot report a second, conflicting outcome.
type Span struct {
	once sync.Once
	end  func(error)
}

func NewSpan(end func(error)) *Span {
	return &Span{end: end}
}

func (span *Span) End(err error) {
	if span != nil {
		span.once.Do(func() {
			if span.end != nil {
				span.end(err)
			}
		})
	}
}
