package scenery

import (
	"context"
	"net/http"

	"scenery.sh/internal/appsdk"
	"scenery.sh/runtime/shared"
)

type AppMetadata = shared.AppMetadata
type Environment = shared.Environment
type Request = shared.Request
type Span = appsdk.Span

const CloudLocal = shared.CloudLocal

func Meta() *AppMetadata {
	return appsdk.Metadata()
}

func CurrentRequest() *Request {
	return appsdk.CurrentRequest()
}

// StartSpan starts an application-owned child span beneath the current request.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	return appsdk.StartSpan(ctx, name)
}

// TraceHTTPTransport gives a custom HTTP transport the same automatic tracing
// as the default client. Without a linked runtime it returns the transport unchanged.
func TraceHTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if host := appsdk.CurrentHost(); host != nil {
		return host.TraceHTTPTransport(base)
	}
	return base
}
