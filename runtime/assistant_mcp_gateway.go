package runtime

// This file owns the app-child side of the private assistant MCP bridge.
// Generated composition supplies a strict, provider-neutral manifest while
// the supervisor supplies only a loopback listen address and a short-lived
// bridge secret. The private mcpgateway package never becomes part of the
// public runtime API.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpgateway"
)

const (
	assistantMCPGatewayServicePrefix = "scenery/assistant-mcp/"
	assistantMCPManifestLimit        = int64(4 << 20)
	assistantMCPRetryAttempts        = 8
	assistantMCPRetryInitial         = 100 * time.Millisecond
	assistantMCPRetryMaximum         = 2 * time.Second
	assistantMCPBridgeSecretMin      = 32
)

var errAssistantMCPManifest = errors.New("assistant MCP manifest is invalid")

// RegisterAssistantMCPManifestChecked binds the generated MCP manifest and
// its canonical federation owner to one assistant. The bytes are parsed and
// validated before they enter runtime state; generated callers cannot bypass
// the provider-neutral mcpcontract checks with a hand-written struct.
func RegisterAssistantMCPManifestChecked(assistantAddress, serverAddress string, manifestJSON []byte) error {
	assistantAddress = strings.TrimSpace(assistantAddress)
	serverAddress = strings.TrimSpace(serverAddress)
	if assistantAddress == "" {
		return fmt.Errorf("%w: assistant address is required", errAssistantMCPManifest)
	}
	if !validMCPRuntimeAddress(serverAddress) {
		return fmt.Errorf("%w: server address is invalid", errAssistantMCPManifest)
	}
	if len(manifestJSON) == 0 || int64(len(manifestJSON)) > assistantMCPManifestLimit {
		return fmt.Errorf("%w: manifest size is invalid", errAssistantMCPManifest)
	}
	// mcpcontract.Parse rejects unknown fields and trailing values. The
	// contract decoder adds duplicate-member and malformed-UTF-8 rejection so
	// the generated boundary is deterministic even for hostile source bytes.
	if _, err := contract.DecodeJSONObject(manifestJSON); err != nil {
		return fmt.Errorf("%w: %v", errAssistantMCPManifest, err)
	}
	manifest, err := mcpcontract.Parse(manifestJSON)
	if err != nil {
		return fmt.Errorf("%w: %v", errAssistantMCPManifest, err)
	}
	global.mu.Lock()
	if global.assistantMCPManifests == nil {
		global.assistantMCPManifests = make(map[string]mcpcontract.Manifest)
	}
	registration, ok := global.assistants[assistantAddress]
	if !ok {
		global.mu.Unlock()
		return fmt.Errorf("%w: assistant %s is not registered", errAssistantMCPManifest, assistantAddress)
	}
	if manifest.ContractRevision != registration.CapabilityRevision {
		global.mu.Unlock()
		return fmt.Errorf("%w: assistant %s capability revision mismatch", errAssistantMCPManifest, assistantAddress)
	}
	if _, exists := global.assistantMCPManifests[assistantAddress]; exists {
		global.mu.Unlock()
		return fmt.Errorf("%w: duplicate assistant manifest %s", errAssistantMCPManifest, assistantAddress)
	}
	global.assistantMCPManifests[assistantAddress] = cloneMCPManifest(manifest)
	global.mu.Unlock()

	ensureAssistantBootstrapService()
	serviceAddress := assistantMCPGatewayServiceAddress(assistantAddress)
	if err := RegisterNativeService(NativeServiceRegistration{
		Address:      serviceAddress,
		Dependencies: []string{serverAddress},
		Initialize:   func(ctx context.Context) error { return initializeAssistantMCPGateway(ctx, assistantAddress) },
		Shutdown:     func(ctx context.Context) error { return shutdownAssistantMCPGateway(ctx, assistantAddress) },
	}); err != nil {
		global.mu.Lock()
		delete(global.assistantMCPManifests, assistantAddress)
		global.mu.Unlock()
		return err
	}
	addAssistantBootstrapDependency(serviceAddress)
	return nil
}

// RegisterAssistantMCPManifest is the panic-on-invalid convenience for hand
// written compositions. Generated code uses the checked form.
func RegisterAssistantMCPManifest(assistantAddress, serverAddress string, manifestJSON []byte) {
	if err := RegisterAssistantMCPManifestChecked(assistantAddress, serverAddress, manifestJSON); err != nil {
		panic(err)
	}
}

func cloneMCPManifest(manifest mcpcontract.Manifest) mcpcontract.Manifest {
	manifest.Capabilities = append([]mcpcontract.Capability(nil), manifest.Capabilities...)
	for index := range manifest.Capabilities {
		manifest.Capabilities[index].InputSchema = append([]byte(nil), manifest.Capabilities[index].InputSchema...)
		manifest.Capabilities[index].OutputSchema = append([]byte(nil), manifest.Capabilities[index].OutputSchema...)
	}
	manifest.Connections = append([]mcpcontract.Connection(nil), manifest.Connections...)
	for index := range manifest.Connections {
		manifest.Connections[index].Allow = append([]string(nil), manifest.Connections[index].Allow...)
		manifest.Connections[index].Block = append([]string(nil), manifest.Connections[index].Block...)
	}
	return manifest
}

func assistantMCPManifestFor(address string) (mcpcontract.Manifest, bool) {
	global.mu.RLock()
	manifest, ok := global.assistantMCPManifests[address]
	global.mu.RUnlock()
	if !ok {
		return mcpcontract.Manifest{}, false
	}
	return cloneMCPManifest(manifest), true
}

func assistantMCPGatewayServiceAddress(assistantAddress string) string {
	return assistantMCPGatewayServicePrefix + strings.TrimSpace(assistantAddress)
}

func addAssistantBootstrapDependency(serviceAddress string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	initializer, ok := global.serviceInitializers[assistantRuntimeBootstrapService]
	if !ok {
		return
	}
	for _, dependency := range initializer.dependencies {
		if dependency == serviceAddress {
			return
		}
	}
	initializer.dependencies = append(initializer.dependencies, serviceAddress)
	global.serviceInitializers[assistantRuntimeBootstrapService] = initializer
}

var activeAssistantMCPGateways struct {
	sync.Mutex
	values map[string]*mcpgateway.Gateway
}

type assistantMCPGatewayReadiness struct {
	gateway   *mcpgateway.Gateway
	ready     bool
	errorCode string
}

var assistantMCPGatewayReadinessState struct {
	sync.Mutex
	values map[string]assistantMCPGatewayReadiness
}

func init() {
	activeAssistantMCPGateways.values = make(map[string]*mcpgateway.Gateway)
	assistantMCPGatewayReadinessState.values = make(map[string]assistantMCPGatewayReadiness)
}

func setAssistantMCPGatewayReadiness(address string, gateway *mcpgateway.Gateway, ready bool, errorCode string) {
	assistantMCPGatewayReadinessState.Lock()
	assistantMCPGatewayReadinessState.values[address] = assistantMCPGatewayReadiness{gateway: gateway, ready: ready, errorCode: errorCode}
	assistantMCPGatewayReadinessState.Unlock()
}

func setAssistantMCPGatewayReadinessIfCurrent(address string, gateway *mcpgateway.Gateway, ready bool, errorCode string) {
	assistantMCPGatewayReadinessState.Lock()
	current := assistantMCPGatewayReadinessState.values[address]
	if current.gateway == gateway {
		assistantMCPGatewayReadinessState.values[address] = assistantMCPGatewayReadiness{gateway: gateway, ready: ready, errorCode: errorCode}
	}
	assistantMCPGatewayReadinessState.Unlock()
}

func assistantMCPGatewayReady(address string) (bool, string) {
	assistantMCPGatewayReadinessState.Lock()
	status, ok := assistantMCPGatewayReadinessState.values[address]
	assistantMCPGatewayReadinessState.Unlock()
	if !ok || !status.ready {
		if status.errorCode == "" {
			return false, "gateway_unavailable"
		}
		return false, status.errorCode
	}
	return true, ""
}

func initializeAssistantMCPGateway(ctx context.Context, assistantAddress string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := LoadAssistantRuntimeConfigFromEnv()
	if err != nil {
		setAssistantMCPGatewayReadiness(assistantAddress, nil, false, "gateway_unavailable")
		// A private descriptor is supplied by the supervisor and may be absent
		// during a first app start. Keep the ordinary app alive; bootstrap status
		// remains unavailable until a later app restart receives valid config.
		return nil
	}
	descriptor, ok := assistantRuntimeDescriptor(config, assistantAddress)
	if !ok || descriptor.MCPListenAddress == "" || descriptor.MCPBridgeSecret == "" {
		setAssistantMCPGatewayReadiness(assistantAddress, nil, false, "gateway_unavailable")
		return nil
	}
	manifest, ok := assistantMCPManifestFor(assistantAddress)
	if !ok {
		setAssistantMCPGatewayReadiness(assistantAddress, nil, false, "gateway_unavailable")
		return nil
	}
	var federation MCPFederation
	if value, exists := LookupAssistantMCPFederation(assistantAddress); exists {
		federation = value
	}
	gateway, err := mcpgateway.New(mcpgateway.Config{
		Manifest:           manifest,
		CapabilityRevision: descriptor.CapabilityRevision,
		Verify:             mcpgateway.HMACAssertionVerifier{Secret: []byte(descriptor.MCPBridgeSecret), Audience: "scenery"},
		Dispatch:           MCPToolDispatcher{},
		Durable:            MCPToolDispatcher{},
		Federation:         federation,
		ListenAddr:         descriptor.MCPListenAddress,
		Version:            "scenery-app-assistant",
	})
	if err != nil {
		setAssistantMCPGatewayReadiness(assistantAddress, nil, false, "gateway_unavailable")
		return nil
	}
	activeAssistantMCPGateways.Lock()
	previous := activeAssistantMCPGateways.values[assistantAddress]
	activeAssistantMCPGateways.values[assistantAddress] = gateway
	activeAssistantMCPGateways.Unlock()
	setAssistantMCPGatewayReadiness(assistantAddress, gateway, true, "")
	go func() {
		if err := gateway.Serve(ctx); err != nil {
			setAssistantMCPGatewayReadinessIfCurrent(assistantAddress, gateway, false, "gateway_unavailable")
		}
	}()
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

func shutdownAssistantMCPGateway(_ context.Context, assistantAddress string) error {
	activeAssistantMCPGateways.Lock()
	gateway := activeAssistantMCPGateways.values[assistantAddress]
	delete(activeAssistantMCPGateways.values, assistantAddress)
	activeAssistantMCPGateways.Unlock()
	if gateway == nil {
		setAssistantMCPGatewayReadiness(assistantAddress, nil, false, "gateway_unavailable")
		return nil
	}
	setAssistantMCPGatewayReadiness(assistantAddress, gateway, false, "gateway_unavailable")
	return gateway.Close()
}

func assistantRuntimeDescriptor(config AssistantRuntimeConfig, address string) (AssistantBootstrapDescriptor, bool) {
	for _, descriptor := range config.Assistants {
		if descriptor.AssistantAddress == address {
			return descriptor, true
		}
	}
	return AssistantBootstrapDescriptor{}, false
}

func validateAssistantMCPListenAddress(value string) error {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != "127.0.0.1" {
		return errors.New("MCP listen address must be 127.0.0.1:<port>")
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed < 1 || parsed > 65535 {
		return errors.New("MCP listen address port is invalid")
	}
	return nil
}

func probeAssistantClientWithRetry(ctx context.Context, client AssistantClient, descriptor AssistantBootstrapDescriptor) error {
	var lastErr error
	backoff := assistantMCPRetryInitial
	for attempt := 0; attempt < assistantMCPRetryAttempts; attempt++ {
		lastErr = probeAssistantClient(ctx, client, descriptor)
		if lastErr == nil {
			return nil
		}
		if !errors.Is(lastErr, assistantruntime.ErrUnavailable) {
			return lastErr
		}
		if attempt+1 == assistantMCPRetryAttempts {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < assistantMCPRetryMaximum {
			backoff *= 2
			if backoff > assistantMCPRetryMaximum {
				backoff = assistantMCPRetryMaximum
			}
		}
	}
	return lastErr
}
