package runtime

import (
	"context"
	"net/http"
	"time"

	"scenery.sh/internal/appsdk"
)

// The runtime serves the application-facing packages through appsdk.Host, so
// those packages depend on the small request-context layer instead of on this
// package's implementation closure.
func init() {
	appsdk.RegisterHost(sdkHost{})
}

type sdkHost struct{}

func (sdkHost) TraceDBQueryStart(ctx context.Context, query string, argsCount int) context.Context {
	return TraceDBQueryStart(ctx, query, argsCount)
}

func (sdkHost) TraceDBQueryEnd(ctx context.Context, commandTag string, rowsAffected int64, err error) {
	TraceDBQueryEnd(ctx, commandTag, rowsAffected, err)
}

func (sdkHost) CurrentAuth() (appsdk.Auth, bool) {
	info := CurrentAuth()
	if info == nil {
		return appsdk.Auth{}, false
	}
	return appsdk.Auth{UID: info.UID, Data: info.Data}, true
}

func (sdkHost) WithAuth(ctx context.Context, auth appsdk.Auth) context.Context {
	return WithAuthContext(ctx, AuthInfo{UID: auth.UID, Data: auth.Data})
}

func (sdkHost) MarkServiceInitialized(service string, shutdown func(context.Context)) {
	MarkServiceInitialized(service, shutdown)
}

func (sdkHost) FrameworkConfigSecret(key string) ([]byte, bool, error) {
	return FrameworkConfigSecret(key)
}

func (sdkHost) FrameworkConfigString(key string) (string, bool, error) {
	return FrameworkConfigString(key)
}

func (sdkHost) TraceStorageOperation(ctx context.Context, store, operation string) (context.Context, func(int64, error)) {
	ctx, end := beginOperationTrace(ctx, "STORAGE", store, operation, map[string]any{"store": store})
	return ctx, func(size int64, err error) {
		if state := stateFromContext(ctx); state != nil && state.trace != nil && state.traceEnabled {
			if reporter := activeReporter(); reporter != nil {
				emitOperationEvent(reporter, state.trace, time.Now().UTC(), map[string]any{"storage_result": map[string]any{"bytes": size}})
			}
		}
		end(err)
	}
}

func (sdkHost) TraceHTTPTransport(base http.RoundTripper) http.RoundTripper {
	return traceHTTPTransport(base)
}
