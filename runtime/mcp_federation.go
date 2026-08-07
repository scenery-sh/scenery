package runtime

// This file is the generated-runtime boundary for external MCP federation.
// It deliberately contains only provider-neutral values: generated code may
// describe a connection and its symbolic secret reference, but it cannot pass
// an URL credential or import the MCP SDK. The SDK client lives in
// internal/mcpfederation and is constructed only during service startup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpfederation"
)

const (
	defaultMCPFederationInputBytes  int64 = mcpcontract.MaximumInputBytes
	defaultMCPFederationResultBytes int64 = mcpcontract.MaximumResultBytes
)

var runtimeMCPNameRE = regexp.MustCompile(mcpcontract.ToolNamePattern)

var mcpFederationRegistrationMu sync.Mutex

var (
	// ErrMCPFederationNotFound is returned by readiness helpers when a server
	// has not been registered or has not finished initialization.
	ErrMCPFederationNotFound = errors.New("MCP federation is not registered")
	// ErrMCPFederationNotReady is a provider-neutral readiness result. The
	// detailed remote error and credentials are intentionally not retained.
	ErrMCPFederationNotReady = errors.New("MCP federation is not ready")
)

// MCPFederatedCapability is the public runtime alias for the provider-neutral
// capability consumed by the private MCP gateway. It carries no URL or auth
// material.
type MCPFederatedCapability = mcpcontract.Capability

// MCPFederation is the narrow runtime lookup surface used to construct a
// private mcpgateway.Config. Implementations expose only the current,
// policy-projected inventory and dispatch; connection credentials remain
// inside the federation owner.
type MCPFederation interface {
	Ready() bool
	Capabilities() []MCPFederatedCapability
	CallTool(context.Context, MCPToolCallContext, string, json.RawMessage) (MCPToolOutcome, error)
}

// MCPSecretReference identifies a framework secret without carrying its
// value. StoreAddress and ResourceAddress are canonical Scenery addresses;
// Key is the provider-defined value key.
type MCPSecretReference struct {
	ResourceAddress string
	StoreAddress    string
	Key             string
}

// MCPSecretResolver is registered by a provider-backed secret store. The
// resolver receives symbolic metadata only and returns a short-lived copy of
// the secret for one federation startup/refresh. Runtime errors never include
// the returned bytes.
type MCPSecretResolver func(context.Context, MCPSecretReference) ([]byte, error)

// MCPConnectionSpec is the deployment-time description of one external MCP
// connection. Auth credentials can only be supplied through Secret; there is
// no plaintext credential field in this generated-code-safe type.
type MCPConnectionSpec struct {
	Address   string
	Namespace string
	URL       string
	Required  bool
	Allow     []string
	Block     []string

	ConnectTimeout time.Duration
	CallTimeout    time.Duration
	RefreshTTL     time.Duration

	AuthScheme string
	AuthHeader string
	Secret     MCPSecretReference
}

// MCPFederationRegistration describes one canonical mcp_server and the
// assistants that share its remote inventory. All assistant lookups for the
// same server return the same federation instance; assistant authorization and
// assertions are still enforced by the private gateway.
type MCPFederationRegistration struct {
	Address            string
	AssistantAddresses []string
	CapabilityRevision string
	LocalToolNames     []string
	MaxInputBytes      int64
	MaxResultBytes     int64
	Connections        []MCPConnectionSpec
}

// MCPFederationReadinessError is retained for a required-connection outage.
// It is deliberately opaque: callers can inspect the typed error and use the
// federation's Ready method without receiving URLs, network text, or secrets.
type MCPFederationReadinessError struct {
	ServerAddress       string
	RequiredUnavailable []string
}

func (e *MCPFederationReadinessError) Error() string { return ErrMCPFederationNotReady.Error() }

func (e *MCPFederationReadinessError) Unwrap() error { return ErrMCPFederationNotReady }

type mcpFederationState struct {
	serverAddress string
	federation    MCPFederation
	close         func() error
	readiness     error
}

// RegisterMCPSecretResolver installs one provider-backed resolver for a
// canonical secret-store address. Resolvers are process-local capability
// registrations and are never serialized into manifests or errors.
func RegisterMCPSecretResolver(storeAddress string, resolver MCPSecretResolver) error {
	storeAddress = strings.TrimSpace(storeAddress)
	if !validMCPRuntimeAddress(storeAddress) {
		return errors.New("runtime: MCP secret resolver requires a valid store address")
	}
	if resolver == nil {
		return errors.New("runtime: MCP secret resolver is nil")
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.mcpSecretResolvers == nil {
		global.mcpSecretResolvers = make(map[string]MCPSecretResolver)
	}
	if _, exists := global.mcpSecretResolvers[storeAddress]; exists {
		return errors.New("runtime: duplicate MCP secret resolver")
	}
	global.mcpSecretResolvers[storeAddress] = resolver
	return nil
}

// RegisterMCPFederation is the panic-on-invalid convenience used by hand
// written generated compositions. Generated code should generally call the
// checked form so ContractRegistry can roll back atomically.
func RegisterMCPFederation(registration MCPFederationRegistration) {
	if err := RegisterMCPFederationChecked(registration); err != nil {
		panic(err)
	}
}

// RegisterMCPFederationChecked validates one canonical mcp_server and
// registers its NativeService lifecycle. The service is not connected until
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

// LookupMCPFederation resolves a canonical mcp_server after initialization.
func LookupMCPFederation(serverAddress string) (MCPFederation, bool) {
	serverAddress = strings.TrimSpace(serverAddress)
	global.mu.RLock()
	defer global.mu.RUnlock()
	state := global.mcpFederations[serverAddress]
	if state == nil || state.federation == nil {
		return nil, false
	}
	return state.federation, true
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

// LookupMCPFederationForAssistant is an explicit alias for generated callers
// that prefer the lookup target in the function name.
func LookupMCPFederationForAssistant(assistantAddress string) (MCPFederation, bool) {
	return LookupAssistantMCPFederation(assistantAddress)
}

// MCPFederationReadiness reports the current required-connection readiness.
// It samples the live federation snapshot so a background refresh can clear an
// earlier outage without another registration or process restart.
func MCPFederationReadiness(serverAddress string) error {
	serverAddress = strings.TrimSpace(serverAddress)
	global.mu.RLock()
	state := global.mcpFederations[serverAddress]
	global.mu.RUnlock()
	if state == nil {
		return ErrMCPFederationNotFound
	}
	if state.federation != nil && state.federation.Ready() {
		return nil
	}
	if concrete, ok := state.federation.(*mcpfederation.Federation); ok {
		snapshot := concrete.Snapshot()
		return &MCPFederationReadinessError{ServerAddress: serverAddress, RequiredUnavailable: append([]string(nil), snapshot.RequiredUnavailable...)}
	}
	if state.readiness == nil {
		return ErrMCPFederationNotReady
	}
	return state.readiness
}

func initializeMCPFederation(ctx context.Context, registration MCPFederationRegistration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	global.mu.RLock()
	resolvers := make(map[string]MCPSecretResolver, len(global.mcpSecretResolvers))
	for address, resolver := range global.mcpSecretResolvers {
		resolvers[address] = resolver
	}
	global.mu.RUnlock()

	connections := make([]mcpfederation.Connection, 0, len(registration.Connections))
	for _, spec := range registration.Connections {
		auth, err := resolveMCPConnectionAuth(ctx, spec, resolvers)
		if err != nil {
			return err
		}
		connections = append(connections, mcpfederation.Connection{
			Address: spec.Address, Namespace: spec.Namespace, URL: spec.URL, Required: spec.Required,
			Auth: auth, Allow: append([]string(nil), spec.Allow...), Block: append([]string(nil), spec.Block...),
			ConnectTimeout: spec.ConnectTimeout, CallTimeout: spec.CallTimeout, RefreshTTL: spec.RefreshTTL,
			Policy: mcpfederation.ToolPolicy{
				// Remote metadata is untrusted. External capabilities are
				// conservatively approval-gated and marked open-world.
				Approval:      mcpcontract.ApprovalAlways,
				Effect:        mcpcontract.Effect{Destructive: true, OpenWorld: true},
				MaxInputBytes: int(registration.MaxInputBytes), MaxResultBytes: int(registration.MaxResultBytes),
			},
		})
	}
	config := mcpfederation.Config{
		Connections: connections, LocalToolNames: append([]string(nil), registration.LocalToolNames...),
		OnDiagnostic: func(diagnostic mcpfederation.Diagnostic) {
			// The federation diagnostic is already sanitized; keep the log
			// fields explicitly limited so future provider errors cannot leak.
			slog.Warn("optional MCP connection unavailable", "server", registration.Address, "connection", diagnostic.Address, "code", diagnostic.Code, "message", diagnostic.Message)
		},
	}
	federation, err := mcpfederation.New(config)
	for i := range connections {
		clear(connections[i].Auth.Secret)
	}
	if err != nil {
		return errors.New("runtime: MCP federation configuration is invalid")
	}
	// Refresh establishes the initial inventory. Required outages are a
	// readiness state, not a process-start failure; malformed configuration,
	// collisions, and protocol violations remain initialization failures.
	refreshErr := federation.Refresh(ctx)
	if refreshErr != nil && !errors.Is(refreshErr, mcpfederation.ErrRequiredUnavailable) {
		_ = federation.Close()
		return errors.New("runtime: MCP federation initialization failed")
	}
	if err := federation.Start(ctx); err != nil {
		_ = federation.Close()
		return errors.New("runtime: MCP federation initialization failed")
	}
	state := &mcpFederationState{serverAddress: registration.Address, federation: federation, close: federation.Close}
	if errors.Is(refreshErr, mcpfederation.ErrRequiredUnavailable) {
		snapshot := federation.Snapshot()
		state.readiness = &MCPFederationReadinessError{ServerAddress: registration.Address, RequiredUnavailable: append([]string(nil), snapshot.RequiredUnavailable...)}
	}
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

func resolveMCPConnectionAuth(ctx context.Context, spec MCPConnectionSpec, resolvers map[string]MCPSecretResolver) (mcpfederation.Auth, error) {
	scheme := strings.ToLower(strings.TrimSpace(spec.AuthScheme))
	if scheme == "" {
		scheme = string(mcpfederation.AuthNone)
	}
	switch scheme {
	case string(mcpfederation.AuthNone):
		if spec.AuthHeader != "" || !emptyMCPSecretReference(spec.Secret) {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		return mcpfederation.Auth{Scheme: mcpfederation.AuthNone}, nil
	case string(mcpfederation.AuthBearer), string(mcpfederation.AuthHeader):
		if !validMCPSecretReference(spec.Secret) {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection secret reference is invalid")
		}
		if scheme == string(mcpfederation.AuthBearer) && spec.AuthHeader != "" {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		if scheme == string(mcpfederation.AuthHeader) && !validMCPHeader(spec.AuthHeader) {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		resolver := resolvers[spec.Secret.StoreAddress]
		if resolver == nil {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection secret resolver is unavailable")
		}
		value, err := resolver(ctx, spec.Secret)
		if err != nil || len(value) == 0 {
			return mcpfederation.Auth{}, errors.New("runtime: MCP connection secret could not be resolved")
		}
		defer clear(value)
		copyValue := append([]byte(nil), value...)
		// The federation constructor copies the credential into its private
		// transport configuration. Clear our temporary copy as soon as possible.
		defer clear(copyValue)
		auth := mcpfederation.Auth{Scheme: mcpfederation.AuthScheme(scheme), Header: spec.AuthHeader, Secret: append([]byte(nil), copyValue...)}
		return auth, nil
	default:
		return mcpfederation.Auth{}, errors.New("runtime: MCP connection authentication is invalid")
	}
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
	connections := make([]MCPConnectionSpec, len(registration.Connections))
	seenAddress, seenNamespace := map[string]bool{}, map[string]bool{}
	for i, spec := range registration.Connections {
		spec.Address, spec.Namespace, spec.URL = strings.TrimSpace(spec.Address), strings.TrimSpace(spec.Namespace), strings.TrimSpace(spec.URL)
		if !validMCPRuntimeAddress(spec.Address) || !runtimeMCPNameRE.MatchString(spec.Namespace) || spec.URL == "" {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection specification is invalid")
		}
		if seenAddress[spec.Address] || seenNamespace[spec.Namespace] {
			return MCPFederationRegistration{}, errors.New("runtime: duplicate MCP connection identity")
		}
		seenAddress[spec.Address], seenNamespace[spec.Namespace] = true, true
		if len(spec.Allow) > 0 && len(spec.Block) > 0 {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection filters are mutually exclusive")
		}
		spec.Allow, err = canonicalMCPNames(spec.Allow, "MCP allow filter")
		if err != nil {
			return MCPFederationRegistration{}, err
		}
		spec.Block, err = canonicalMCPNames(spec.Block, "MCP block filter")
		if err != nil {
			return MCPFederationRegistration{}, err
		}
		if spec.ConnectTimeout < 0 || spec.CallTimeout < 0 || spec.RefreshTTL < 0 {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection timeout is invalid")
		}
		scheme := strings.ToLower(strings.TrimSpace(spec.AuthScheme))
		if scheme == "" {
			scheme = string(mcpfederation.AuthNone)
		}
		spec.AuthScheme = scheme
		spec.AuthHeader = strings.TrimSpace(spec.AuthHeader)
		spec.Secret.ResourceAddress = strings.TrimSpace(spec.Secret.ResourceAddress)
		spec.Secret.StoreAddress = strings.TrimSpace(spec.Secret.StoreAddress)
		spec.Secret.Key = strings.TrimSpace(spec.Secret.Key)
		if scheme == string(mcpfederation.AuthHeader) && !validMCPHeader(spec.AuthHeader) {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		if scheme != string(mcpfederation.AuthNone) && scheme != string(mcpfederation.AuthBearer) && scheme != string(mcpfederation.AuthHeader) {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		if scheme == string(mcpfederation.AuthNone) && (!emptyMCPSecretReference(spec.Secret) || spec.AuthHeader != "") {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection authentication is invalid")
		}
		if scheme != string(mcpfederation.AuthNone) && !validMCPSecretReference(spec.Secret) {
			return MCPFederationRegistration{}, errors.New("runtime: MCP connection secret reference is invalid")
		}
		connections[i] = cloneMCPConnectionSpec(spec)
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].Address < connections[j].Address })
	registration.Connections = connections
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

func validMCPHeader(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || http.CanonicalHeaderKey(value) == "" {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r > 0x7e || strings.ContainsRune("()<>@,;:\\\"/[]?={} ", r) {
			return false
		}
	}
	return true
}

func emptyMCPSecretReference(reference MCPSecretReference) bool {
	return strings.TrimSpace(reference.ResourceAddress) == "" && strings.TrimSpace(reference.StoreAddress) == "" && strings.TrimSpace(reference.Key) == ""
}

func validMCPSecretReference(reference MCPSecretReference) bool {
	if !validMCPRuntimeAddress(strings.TrimSpace(reference.ResourceAddress)) || !validMCPRuntimeAddress(strings.TrimSpace(reference.StoreAddress)) {
		return false
	}
	key := strings.TrimSpace(reference.Key)
	if key == "" || key != reference.Key {
		return false
	}
	for _, r := range key {
		if r <= 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func cloneMCPConnectionSpec(spec MCPConnectionSpec) MCPConnectionSpec {
	spec.Allow = append([]string(nil), spec.Allow...)
	spec.Block = append([]string(nil), spec.Block...)
	return spec
}

func cloneMCPFederationRegistration(registration MCPFederationRegistration) MCPFederationRegistration {
	registration.AssistantAddresses = append([]string(nil), registration.AssistantAddresses...)
	registration.LocalToolNames = append([]string(nil), registration.LocalToolNames...)
	registration.Connections = append([]MCPConnectionSpec(nil), registration.Connections...)
	for i := range registration.Connections {
		registration.Connections[i] = cloneMCPConnectionSpec(registration.Connections[i])
	}
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
	for address, state := range values {
		clone[address] = state
	}
	return clone
}

var _ MCPFederation = (*mcpfederation.Federation)(nil)
