// Package mcpapi is the boundary between a Scenery process and the MCP
// implementation it may link. It imports no MCP SDK: a process that serves an
// assistant gateway links
// internal/mcpgateway, which installs its constructor here; every other process, such as a development service
// process, links neither and pays nothing for them.
package mcpapi

import (
	"context"

	"scenery.sh/internal/mcpcontract"
)

// NewGateway is installed by the gateway package when it is linked. It is
// written during package initialization only.
var NewGateway func(GatewayConfig) (Gateway, error)

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
	ListenAddr         string
	Version            string
}
