// Package mcpfederation holds the federation an mcp_server registers for its
// assistants. A federation reserves the server's local tool names; it has no
// capability source of its own.
package mcpfederation

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"

	"scenery.sh/internal/mcpapi"
	"scenery.sh/internal/mcpcontract"
)

var toolNameRE = regexp.MustCompile(mcpcontract.ToolNamePattern)

var (
	ErrClosed        = errors.New("MCP federation is closed")
	ErrToolNotFound  = errors.New("federated MCP tool is unavailable")
	ErrInvalidConfig = errors.New("invalid MCP federation configuration")
)

// Config belongs to the SDK-free boundary package, so a process can describe
// a federation without linking this implementation.
type Config = mcpapi.FederationConfig

func init() {
	mcpapi.NewFederation = func(config mcpapi.FederationConfig) (mcpapi.Federation, error) {
		federation, err := New(config)
		if err != nil {
			return nil, err
		}
		return federation, nil
	}
}

// Federation is a concurrent-safe federation of one mcp_server.
type Federation struct {
	mu     sync.RWMutex
	closed bool
}

// New validates the configuration and returns its federation.
func New(cfg Config) (*Federation, error) {
	local := make(map[string]bool, len(cfg.LocalToolNames))
	for _, name := range cfg.LocalToolNames {
		if !toolNameRE.MatchString(name) || local[name] {
			return nil, ErrInvalidConfig
		}
		local[name] = true
	}
	return &Federation{}, nil
}

// Ready reports whether the federation's inventory is available.
func (f *Federation) Ready() bool { return true }

// Capabilities returns the current federated inventory.
func (f *Federation) Capabilities() []mcpcontract.Capability { return nil }

// CallTool implements mcpcontract.ToolDispatcher for federated tools.
func (f *Federation) CallTool(context.Context, mcpcontract.ToolCallContext, string, json.RawMessage) (mcpcontract.ToolOutcome, error) {
	f.mu.RLock()
	closed := f.closed
	f.mu.RUnlock()
	if closed {
		return mcpcontract.ToolOutcome{}, ErrClosed
	}
	return mcpcontract.ToolOutcome{}, ErrToolNotFound
}

// Close is safe to call more than once.
func (f *Federation) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// compile-time checks keep the package on the provider-neutral contract.
var _ mcpcontract.ToolDispatcher = (*Federation)(nil)
