package scenery

import (
	"context"

	"scenery.sh/runtime"
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
type Span = runtime.Span

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
	return runtime.Meta()
}

func CurrentRequest() *Request {
	return runtime.CurrentRequest()
}

// StartSpan starts an application-owned child span beneath the current request.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	return runtime.StartSpan(ctx, name)
}
