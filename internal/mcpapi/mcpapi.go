// Package mcpapi is the boundary between a Scenery process and the MCP
// implementation it may link. It imports no MCP SDK: a process that serves an
// assistant gateway links
// internal/mcpgateway and internal/mcpfederation, which install their
// constructors here; every other process, such as a development service
// process, links neither and pays nothing for them.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"scenery.sh/internal/mcpcontract"
)

// ErrNotLinked reports that this process was linked without the MCP
// implementation it was asked to start.
var ErrNotLinked = errors.New("MCP support is not linked into this process")

// NewFederation and NewGateway are installed by the implementation packages
// when they are linked. They are written during package initialization only.
var (
	NewFederation func(FederationConfig) (Federation, error)
	NewGateway    func(GatewayConfig) (Gateway, error)
)

// Federation is the running federation of one mcp_server.
type Federation interface {
	FederatedTools
	Close() error
}

// FederatedTools is the ready, policy-projected inventory a gateway exposes.
type FederatedTools interface {
	Ready() bool
	Capabilities() []mcpcontract.Capability
	CallTool(context.Context, mcpcontract.ToolCallContext, string, json.RawMessage) (mcpcontract.ToolOutcome, error)
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
	Federation         FederatedTools
	ListenAddr         string
	Version            string
}

// FederationConfig configures a federation instance. LocalToolNames is the set
// of generated/local capability names.
type FederationConfig struct {
	LocalToolNames []string
}
