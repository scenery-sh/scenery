package runtime

// This file is the generated-runtime boundary for MCP federation. It
// deliberately contains only provider-neutral values. The federation lives in
// internal/mcpfederation and is constructed only during service startup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"sync"

	"scenery.sh/internal/mcpapi"
	"scenery.sh/internal/mcpcontract"
)

const (
	defaultMCPFederationInputBytes  int64 = mcpcontract.MaximumInputBytes
	defaultMCPFederationResultBytes int64 = mcpcontract.MaximumResultBytes
)

var runtimeMCPNameRE = regexp.MustCompile(mcpcontract.ToolNamePattern)

var mcpFederationRegistrationMu sync.Mutex

// MCPFederatedCapability is the public runtime alias for the provider-neutral
// capability consumed by the private MCP gateway.
type MCPFederatedCapability = mcpcontract.Capability

// MCPFederation is the narrow runtime lookup surface used to construct a
// private mcpgateway.Config. Implementations expose only the current,
// policy-projected inventory and dispatch.
type MCPFederation interface {
	Ready() bool
	Capabilities() []MCPFederatedCapability
	CallTool(context.Context, MCPToolCallContext, string, json.RawMessage) (MCPToolOutcome, error)
}

// MCPFederationRegistration describes one canonical mcp_server and the
// assistants that share its inventory. All assistant lookups for the
// same server return the same federation instance; assistant authorization and
// assertions are still enforced by the private gateway.
type MCPFederationRegistration struct {
	Address            string
	AssistantAddresses []string
	CapabilityRevision string
	LocalToolNames     []string
	MaxInputBytes      int64
	MaxResultBytes     int64
}

type mcpFederationState struct {
	serverAddress string
	federation    MCPFederation
	close         func() error
}

// RegisterMCPFederationChecked validates one canonical mcp_server and
// registers its NativeService lifecycle. The federation is not constructed until
// InitializeServices runs, preserving ContractRegistry's apply/rollback
// boundary.
func RegisterMCPFederationChecked(registration MCPFederationRegistration) error {
	// Serialize the reservation, NativeService insertion, and map commit as one
	// boundary. This prevents a concurrent composition from leaving an orphan
	// service initializer after a duplicate check races.
	mcpFederationRegistrationMu.Lock()
	defer mcpFederationRegistrationMu.Unlock()
	registration, err := normalizeMCPFederationRegistration(registration)
	if err != nil {
		return err
	}
	global.mu.Lock()
	if global.mcpFederationSpecs == nil {
		global.mcpFederationSpecs = make(map[string]MCPFederationRegistration)
	}
	if global.mcpFederations == nil {
		global.mcpFederations = make(map[string]*mcpFederationState)
	}
	if global.mcpFederationAssistants == nil {
		global.mcpFederationAssistants = make(map[string]string)
	}
	if _, exists := global.mcpFederationSpecs[registration.Address]; exists {
		global.mu.Unlock()
		return errors.New("runtime: duplicate MCP federation registration")
	}
	for _, assistant := range registration.AssistantAddresses {
		if existing := global.mcpFederationAssistants[assistant]; existing != "" {
			global.mu.Unlock()
			return errors.New("runtime: assistant is mapped to multiple MCP federations")
		}
	}
	if _, exists := global.serviceInitializers[registration.Address]; exists {
		global.mu.Unlock()
		return errors.New("runtime: duplicate MCP federation service")
	}
	// Reserve the server and assistant identities before releasing the lock so
	// concurrent generated composition calls cannot both pass validation.
	global.mcpFederationSpecs[registration.Address] = cloneMCPFederationRegistration(registration)
	for _, assistant := range registration.AssistantAddresses {
		global.mcpFederationAssistants[assistant] = registration.Address
	}
	global.mu.Unlock()

	initialize := func(ctx context.Context) error {
		return initializeMCPFederation(ctx, registration)
	}
	shutdown := func(ctx context.Context) error {
		return shutdownMCPFederation(ctx, registration.Address)
	}
	if err := RegisterNativeService(NativeServiceRegistration{Address: registration.Address, Initialize: initialize, Shutdown: shutdown}); err != nil {
		global.mu.Lock()
		delete(global.mcpFederationSpecs, registration.Address)
		for _, assistant := range registration.AssistantAddresses {
			if global.mcpFederationAssistants[assistant] == registration.Address {
				delete(global.mcpFederationAssistants, assistant)
			}
		}
		global.mu.Unlock()
		return errors.New("runtime: MCP federation service registration failed")
	}
	return nil
}

// LookupAssistantMCPFederation resolves the shared federation for an
// assistant address.
func LookupAssistantMCPFederation(assistantAddress string) (MCPFederation, bool) {
	assistantAddress = strings.TrimSpace(assistantAddress)
	global.mu.RLock()
	serverAddress := global.mcpFederationAssistants[assistantAddress]
	state := global.mcpFederations[serverAddress]
	global.mu.RUnlock()
	if serverAddress == "" || state == nil || state.federation == nil {
		return nil, false
	}
	return state.federation, true
}

func initializeMCPFederation(_ context.Context, registration MCPFederationRegistration) error {
	config := mcpapi.FederationConfig{LocalToolNames: append([]string(nil), registration.LocalToolNames...)}
	if mcpapi.NewFederation == nil {
		return mcpapi.ErrNotLinked
	}
	federation, err := mcpapi.NewFederation(config)
	if err != nil {
		return errors.New("runtime: MCP federation configuration is invalid")
	}
	state := &mcpFederationState{serverAddress: registration.Address, federation: federation, close: federation.Close}
	global.mu.Lock()
	previous := global.mcpFederations[registration.Address]
	global.mcpFederations[registration.Address] = state
	global.mu.Unlock()
	if previous != nil && previous.close != nil {
		_ = previous.close()
	}
	return nil
}

func shutdownMCPFederation(_ context.Context, serverAddress string) error {
	global.mu.Lock()
	state := global.mcpFederations[serverAddress]
	delete(global.mcpFederations, serverAddress)
	global.mu.Unlock()
	if state == nil || state.close == nil {
		return nil
	}
	if err := state.close(); err != nil {
		return errors.New("runtime: MCP federation shutdown failed")
	}
	return nil
}

func normalizeMCPFederationRegistration(registration MCPFederationRegistration) (MCPFederationRegistration, error) {
	registration.Address = strings.TrimSpace(registration.Address)
	if !validMCPRuntimeAddress(registration.Address) {
		return MCPFederationRegistration{}, errors.New("runtime: MCP federation requires a valid server address")
	}
	registration.CapabilityRevision = strings.TrimSpace(registration.CapabilityRevision)
	if registration.CapabilityRevision == "" {
		return MCPFederationRegistration{}, errors.New("runtime: MCP federation requires a capability revision")
	}
	var err error
	registration.AssistantAddresses, err = canonicalMCPAddresses(registration.AssistantAddresses, "assistant")
	if err != nil {
		return MCPFederationRegistration{}, err
	}
	registration.LocalToolNames, err = canonicalMCPNames(registration.LocalToolNames, "local MCP tool")
	if err != nil {
		return MCPFederationRegistration{}, err
	}
	if registration.MaxInputBytes == 0 {
		registration.MaxInputBytes = defaultMCPFederationInputBytes
	}
	if registration.MaxResultBytes == 0 {
		registration.MaxResultBytes = defaultMCPFederationResultBytes
	}
	if registration.MaxInputBytes <= 0 || registration.MaxResultBytes <= 0 || registration.MaxInputBytes > defaultMCPFederationInputBytes || registration.MaxResultBytes > defaultMCPFederationResultBytes {
		return MCPFederationRegistration{}, errors.New("runtime: MCP federation limits are invalid")
	}
	return cloneMCPFederationRegistration(registration), nil
}

func canonicalMCPAddresses(values []string, kind string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validMCPRuntimeAddress(value) {
			return nil, fmt.Errorf("runtime: invalid MCP %s address", kind)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalMCPNames(values []string, kind string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !runtimeMCPNameRE.MatchString(value) {
			return nil, fmt.Errorf("runtime: invalid %s name", kind)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validMCPRuntimeAddress(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' || r == '\t' || r == ' ' {
			return false
		}
	}
	return true
}

func cloneMCPFederationRegistration(registration MCPFederationRegistration) MCPFederationRegistration {
	registration.AssistantAddresses = append([]string(nil), registration.AssistantAddresses...)
	registration.LocalToolNames = append([]string(nil), registration.LocalToolNames...)
	return registration
}

func cloneMCPFederationRegistrations(values map[string]MCPFederationRegistration) map[string]MCPFederationRegistration {
	clone := make(map[string]MCPFederationRegistration, len(values))
	for address, registration := range values {
		clone[address] = cloneMCPFederationRegistration(registration)
	}
	return clone
}

func cloneMCPFederationStates(values map[string]*mcpFederationState) map[string]*mcpFederationState {
	clone := make(map[string]*mcpFederationState, len(values))
	maps.Copy(clone, values)
	return clone
}

var _ MCPFederation = mcpapi.Federation(nil)
