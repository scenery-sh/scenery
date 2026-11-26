// Package workerplan is the host plan of a worker bundle: the bindings,
// policies, codecs, durable executions and schedules the CLI projects from
// the compiled graph into the manifest the Rust host serves.
package workerplan

import (
	"encoding/json"
	"net/http"
	"time"

	"scenery.sh/internal/connectionapi"
	"scenery.sh/internal/runtimeapi"
)

type Access = runtimeapi.Access

const (
	ContractSourcePath     = "path"
	ContractSourcePathTail = "path_tail"
	ContractSourceQuery    = "query"
	ContractSourceHeader   = "header"
	ContractSourceCookie   = "cookie"
)

// WorkerServicePlan maps one canonical service to a supervised process.
// The Host, rather than that process, registers its binding contracts.
type WorkerServicePlan struct {
	Address    string
	Module     string
	Runtime    string
	Name       string
	Process    string
	Operations map[string]WorkerOperationPlan
	Clients    map[string]WorkerClientPlan
	Bindings   []WorkerBindingPlan
	Durable    map[string]WorkerDurablePlan
	Schedules  []WorkerSchedulePlan
	Stores     map[string]string
}

type WorkerClientPlan struct {
	Binding string
	Enqueue bool
}

type WorkerOperationPlan struct {
	Input    string
	Outcomes map[string]string
}

type WorkerBindingPlan struct {
	Address    string
	Operation  string
	Policy     ContractHTTPPolicy
	HTTP       *WorkerHTTPPlan
	MCP        bool `json:",omitempty"`
	Visibility string
	Delivery   string
	Execution  string
}

type WorkerDurablePlan struct {
	Operation    string
	Registration ContractDurableRegistration
	Dedupe       []WorkerKeyField
	Concurrency  []WorkerKeyField
}

type WorkerSchedulePlan struct {
	Address           string
	Operation         string
	Execution         string
	Authorization     string
	Pipeline          string
	Policy            ContractHTTPPolicy
	Input             json.RawMessage
	Timeout           time.Duration
	Every             time.Duration
	Overlap           string
	CatchupMaximumAge time.Duration
}

type WorkerKeyField struct {
	Name string
	Type string
}

type WorkerHTTPPlan struct {
	Method    string
	Path      string
	Access    Access
	PathTail  *ContractPathTail
	Request   ContractRequestSchema
	Responses map[string]WorkerResponsePlan
}

type WorkerResponsePlan struct {
	Status  int
	Codec   string
	Media   []string
	Body    WorkerResponseValue
	Options ContractResponseOptions
	Stream  bool
	Headers []WorkerResponseHeader
	Cookies []WorkerResponseCookie
}

type WorkerResponseValue struct {
	Path         []string
	Optional     bool
	DefaultEmpty bool
}

type WorkerResponseHeader struct {
	Name     string
	Encoding string
	Value    WorkerResponseValue
}

type WorkerResponseCookie struct {
	Cookie ContractResponseCookie
	Value  WorkerResponseValue
}

type ContractHTTPPolicy struct {
	// Generated semantic identities are shared by every transport adapter.
	ServiceName                 string
	OperationAddress            string
	BindingAddress              string
	GatewayAddress              string
	CORS                        string
	AllowedOrigins              []string
	Forwarded                   string
	TrustedProxyPrefixes        []string
	MaxRequestHeaderBytes       int64
	MaxResponseBytes            int64
	CompressionAlgorithms       []string
	CompressionThreshold        int64
	TotalInvocationTimeoutNanos int64
	ReadTimeoutNanos            int64
	WriteTimeoutNanos           int64
	IdleTimeoutNanos            int64
	AuthorizationStrategy       string
	AuthorizationRuleCount      int
	AuthorizationRules          []ContractAuthorizationRule
	PipelineSteps               []string
	FrameworkGuarantee          string
	TransportStatuses           map[string]int
}

type ContractAuthorizationRule struct {
	Name       string
	Effect     string
	Expression string
}

type ContractContextMapping struct {
	Source string
	Target string
}

type ContractMultipartPart struct {
	Name           string
	Target         string
	Type           string
	Kind           string
	MediaTypes     []string
	MaxBytes       int64
	Multiple       bool
	Optional       bool
	RetainFilename bool
}

type ContractInputMapping struct {
	Source     string
	Name       string
	Target     string
	Type       string
	Encoding   string
	Optional   bool
	EnumValues []string
}

type ContractBodyMapping struct {
	Codec                    string
	Target                   string
	Type                     string
	Include                  []string
	Except                   []string
	AcceptedMediaTypes       []string
	SupportedContentEncoding []string
	MaxCompressedBytes       int64
	MaxDecompressedBytes     int64
	Fields                   []ContractInputMapping
	MultipartParts           []ContractMultipartPart
	MaxMultipartParts        int
}

type ContractRequestSchema struct {
	Mappings          []ContractInputMapping
	ContextMappings   []ContractContextMapping
	Body              *ContractBodyMapping
	TransportStatuses map[string]int
}

type ContractResponseOptions struct {
	MaxBytes              int64
	CompressionAlgorithms []string
	CompressionThreshold  int64
	TypeExpression        string
}

type ContractResponseCookie struct {
	Name     string
	Path     string
	Domain   string
	MaxAge   int
	Expires  string
	Secure   bool
	HTTPOnly bool
	SameSite http.SameSite
}

type ContractPathTail struct {
	CanonicalTemplate string
	Name              string
	Target            string
	Type              string
	EmptyCapture      string
	MinimumSegments   int
	Decoding          string
	Guarantee         string
	Precedence        []string
}

type ContractDurableRegistration struct {
	Address                string
	ExternalName           string
	EngineAddress          string
	Service                string
	Revision               uint32
	DefaultTimeout         time.Duration
	DefaultLease           time.Duration
	MaxAttempts            int
	RetryInitial           time.Duration
	RetryMax               time.Duration
	RetryBackoff           float64
	RetryJitter            float64
	SuccessRetention       time.Duration
	FailureRetention       time.Duration
	MaxConcurrency         int
	DeduplicationRetention time.Duration
	DeduplicationConflict  string
}

// ObservabilityConfig selects the endpoints whose logs and traces the host
// records.
type ObservabilityConfig struct {
	Logs    EndpointFilterConfig
	Tracing EndpointFilterConfig
}

type EndpointFilterConfig struct {
	IncludeEndpoints []string
	ExcludeEndpoints []string
}

const (
	Public  = runtimeapi.Public
	Auth    = runtimeapi.Auth
	Private = runtimeapi.Private
)

// StandardAuth configures the host's standard authentication. BootstrapSQL,
// when present, is the schema the host applies before it serves.
type StandardAuth struct {
	Enabled      bool
	GoogleOAuth  GoogleOAuth
	Connections  []connectionapi.ProviderConfig
	DevBootstrap DevBootstrap
	BootstrapSQL string `json:"bootstrap_sql,omitempty"`
}

type GoogleOAuth struct {
	Enabled bool
}

type DevBootstrap struct {
	Enabled          bool
	DefaultUserEmail string
	DefaultTenantID  string
}
