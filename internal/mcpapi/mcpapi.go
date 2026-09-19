// Package mcpapi is the boundary between a Scenery process and the MCP
// implementation it may link. It imports no MCP SDK: a process that serves an
// assistant gateway or connects to remote MCP servers links
// internal/mcpgateway and internal/mcpfederation, which install their
// constructors here; every other process, such as a development service
// process, links neither and pays nothing for them.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"scenery.sh/internal/mcpcontract"
)

// ErrNotLinked reports that this process was linked without the MCP
// implementation it was asked to start.
var ErrNotLinked = errors.New("MCP support is not linked into this process")

// ErrRequiredUnavailable reports that a required MCP connection is not ready.
var ErrRequiredUnavailable = errors.New("required MCP connection is unavailable")

// NewFederation and NewGateway are installed by the implementation packages
// when they are linked. They are written during package initialization only.
var (
	NewFederation func(FederationConfig) (Federation, error)
	NewGateway    func(GatewayConfig) (Gateway, error)
)

// Federation is a running set of remote MCP connections.
type Federation interface {
	FederatedTools
	Refresh(context.Context) error
	Start(context.Context) error
	Close() error
	// RequiredUnavailable names the required connections that are not ready.
	RequiredUnavailable() []string
}

// FederatedTools is the ready, policy-projected inventory a gateway exposes.
// URL and authentication material never cross this interface.
type FederatedTools interface {
	Ready() bool
	Capabilities() []mcpcontract.Capability
	CallTool(context.Context, mcpcontract.ToolCallContext, string, json.RawMessage) (mcpcontract.ToolOutcome, error)
}

// DurableOperations backs the framework-owned status and cancel tools.
type DurableOperations interface {
	Status(context.Context, mcpcontract.ToolCallContext, string) (json.RawMessage, error)
	Cancel(context.Context, mcpcontract.ToolCallContext, string) (json.RawMessage, error)
}

// Gateway is a private assistant MCP gateway.
type Gateway interface {
	Serve(context.Context) error
	Close() error
}

// GatewayConfig configures a private gateway whose requests carry an HMAC
// assertion signed with Secret for Audience.
type GatewayConfig struct {
	Manifest           mcpcontract.Manifest
	CapabilityRevision string
	Secret             []byte
	Audience           string
	Dispatch           mcpcontract.ToolDispatcher
	Durable            DurableOperations
	Federation         FederatedTools
	ListenAddr         string
	Version            string
}

// AuthScheme identifies one of the static authentication schemes supported by
// an mcp_connection.  OAuth and arbitrary multi-header authentication are
// intentionally not represented.
type AuthScheme string

const (
	AuthNone   AuthScheme = "none"
	AuthBearer AuthScheme = "bearer"
	AuthHeader AuthScheme = "header"
)

// Auth contains Scenery-owned secret material.  Secret is copied during
// construction and is never present in Tool, Snapshot, Diagnostic, or any
// returned error.
type Auth struct {
	Scheme AuthScheme
	Secret []byte
	Header string
}

// ToolPolicy is the local policy applied to every remote tool in a
// connection.  Remote annotations are ignored.  Limits are bounded before a
// tool is exposed and again before every call.
type ToolPolicy struct {
	Approval       mcpcontract.Approval
	Effect         mcpcontract.Effect
	MaxInputBytes  int
	MaxResultBytes int
}

// Connection is the deployment-time configuration for one mcp_connection.
// URL and Auth are intentionally consumed only by the federation's transport.
type Connection struct {
	Address   string
	Namespace string
	URL       string
	Required  bool
	Auth      Auth
	Allow     []string
	Block     []string

	ConnectTimeout time.Duration
	CallTimeout    time.Duration
	RefreshTTL     time.Duration
	Policy         ToolPolicy
}

// FederationConfig configures a federation instance.  LocalToolNames is the set of
// generated/local capability names; remote names colliding with it are
// rejected before the snapshot is committed.
type FederationConfig struct {
	Connections    []Connection
	LocalToolNames []string

	// RefreshEvery controls the background refresh cadence started by Start.
	// A zero value uses one second.  It is bounded to at least 10ms.
	RefreshEvery time.Duration
	// DiagnosticTTL rate-limits optional-connection outage diagnostics.
	// A zero value uses one minute.
	DiagnosticTTL time.Duration
	// MaxHTTPResponse bounds a single remote HTTP response body.  It protects
	// list and call responses before SDK JSON decoding.  A zero value uses 32MiB.
	MaxHTTPResponse int64
	// MaxTools bounds one remote inventory, including paginated tools/list
	// responses.
	MaxTools int

	// OnDiagnostic receives a generic developer diagnostic for optional
	// connection outages.  The callback is never given an underlying network
	// error or credential.
	OnDiagnostic func(FederationDiagnostic)
}

// FederationDiagnostic is a safe, provider-neutral developer diagnostic.  It contains
// no URL, auth header, secret, or remote error text.
type FederationDiagnostic struct {
	Address string
	Code    string
	Message string
	At      time.Time
}
