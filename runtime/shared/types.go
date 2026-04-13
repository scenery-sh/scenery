package shared

import (
	"net/http"
	"reflect"
	"time"
)

type Environment struct {
	Name  string
	Type  EnvironmentType
	Cloud CloudProvider
}

type AppMetadata struct {
	AppID       string
	APIBaseURL  string
	Environment Environment
}

type EnvironmentType string

const (
	EnvProduction  EnvironmentType = "production"
	EnvDevelopment EnvironmentType = "development"
	EnvEphemeral   EnvironmentType = "ephemeral"
	EnvLocal       EnvironmentType = "local"
	EnvTest        EnvironmentType = "test"
)

type CloudProvider string

const (
	CloudAWS    CloudProvider = "aws"
	CloudGCP    CloudProvider = "gcp"
	CloudAzure  CloudProvider = "azure"
	CloudEncore CloudProvider = "encore"
	CloudLocal  CloudProvider = "local"
)

type RequestType string

const (
	None         RequestType = "none"
	APICall      RequestType = "api-call"
	InternalCall RequestType = "internal-call"
	RawAPICall   RequestType = "raw-api-call"
)

type APIDesc struct {
	RequestType  reflect.Type
	ResponseType reflect.Type
	Raw          bool
	Exposed      bool
	AuthRequired bool
}

type PathParam struct {
	Name  string
	Value string
}

type PathParams []PathParam

func (p PathParams) Get(name string) string {
	for _, item := range p {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}

type Request struct {
	Type       RequestType
	Started    time.Time
	API        *APIDesc
	Service    string
	Endpoint   string
	Path       string
	PathParams PathParams
	Method     string
	Headers    http.Header
	Payload    any
}
