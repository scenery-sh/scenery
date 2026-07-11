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
	AppID        string
	BaseAppID    string
	RuntimeAppID string
	SessionID    string
	APIBaseURL   string
	Environment  Environment
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
	CloudAWS   CloudProvider = "aws"
	CloudGCP   CloudProvider = "gcp"
	CloudAzure CloudProvider = "azure"
	CloudLocal CloudProvider = "local"
)

type RequestType string

const (
	None         RequestType = "none"
	APICall      RequestType = "api-call"
	InternalCall RequestType = "internal-call"
	RawAPICall   RequestType = "raw-api-call"
	EventCall    RequestType = "event-call"
	DurableCall  RequestType = "durable-call"
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
	Type          RequestType
	Started       time.Time
	InvocationID  string
	TraceID       string
	CallerBinding string
	ExecutionID   string
	Deployment    string
	Locale        string
	Deadline      time.Time
	API           *APIDesc
	Service       string
	Endpoint      string
	Path          string
	PathParams    PathParams
	Method        string
	Headers       http.Header
	Payload       any

	// CronIdempotencyKey is set when the current request was triggered by a cron job.
	CronIdempotencyKey string
}
