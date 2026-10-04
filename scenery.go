package scenery

import (
	"context"
	"net/http"

	"scenery.sh/internal/appsdk"
	"scenery.sh/runtime/shared"
)

type AppMetadata = shared.AppMetadata
type Environment = shared.Environment
type EnvironmentType = shared.EnvironmentType
type CloudProvider = shared.CloudProvider
type Request = shared.Request
type RequestType = shared.RequestType
type APIDesc = shared.APIDesc
type PathParam = shared.PathParam
type PathParams = shared.PathParams
type Span = appsdk.Span

const (
	EnvProduction  = shared.EnvProduction
	EnvDevelopment = shared.EnvDevelopment
	EnvEphemeral   = shared.EnvEphemeral
	EnvLocal       = shared.EnvLocal
	EnvTest        = shared.EnvTest
	CloudAWS       = shared.CloudAWS
	CloudGCP       = shared.CloudGCP
	CloudAzure     = shared.CloudAzure
	CloudLocal     = shared.CloudLocal
	None           = shared.None
	APICall        = shared.APICall
	InternalCall   = shared.InternalCall
	RawAPICall     = shared.RawAPICall
)

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
