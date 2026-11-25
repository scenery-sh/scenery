package runtime

import (
	"encoding/json"
	"time"
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
