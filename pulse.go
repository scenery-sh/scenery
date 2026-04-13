package pulse

import (
	"pulse.dev/runtime"
	"pulse.dev/runtime/shared"
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

const (
	EnvProduction  = shared.EnvProduction
	EnvDevelopment = shared.EnvDevelopment
	EnvEphemeral   = shared.EnvEphemeral
	EnvLocal       = shared.EnvLocal
	EnvTest        = shared.EnvTest
	CloudAWS       = shared.CloudAWS
	CloudGCP       = shared.CloudGCP
	CloudAzure     = shared.CloudAzure
	CloudEncore    = shared.CloudEncore
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
