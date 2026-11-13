package mcpfederation

import (
	"context"
	"errors"
	"testing"

	"scenery.sh/internal/mcpapi"
	"scenery.sh/internal/mcpcontract"
)

func TestNewRejectsInvalidAndDuplicateLocalToolNames(t *testing.T) {
	for name, tools := range map[string][]string{
		"invalid":   {"not a tool name"},
		"empty":     {""},
		"duplicate": {"alpha", "alpha"},
	} {
		if _, err := New(Config{LocalToolNames: tools}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s local tool names: error = %v, want ErrInvalidConfig", name, err)
		}
	}
	if _, err := mcpapi.NewFederation(mcpapi.FederationConfig{LocalToolNames: []string{"alpha", "alpha"}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("linked constructor error = %v, want ErrInvalidConfig", err)
	}
}

func TestFederationHasNoToolsAndClosesIdempotently(t *testing.T) {
	federation, err := New(Config{LocalToolNames: []string{"alpha", "zeta"}})
	if err != nil {
		t.Fatal(err)
	}
	if !federation.Ready() || len(federation.Capabilities()) != 0 {
		t.Fatalf("ready = %v, capabilities = %v", federation.Ready(), federation.Capabilities())
	}
	if _, err := federation.CallTool(context.Background(), mcpcontract.ToolCallContext{}, "alpha", nil); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("open call error = %v, want ErrToolNotFound", err)
	}
	if err := federation.Close(); err != nil {
		t.Fatal(err)
	}
	if err := federation.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := federation.CallTool(context.Background(), mcpcontract.ToolCallContext{}, "alpha", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed call error = %v, want ErrClosed", err)
	}
}
