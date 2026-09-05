package runtime

// This file owns the boundary between generated assistant registrations and
// helper processes started by the supervisor.  The helper process is
// intentionally represented only by the provider-neutral assistantruntime
// HTTP client; implementation adapters never enter this package.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/envpolicy"
)

// AssistantRuntimeConfigEnv is the one runtime configuration seam used by a
// generated app for helper descriptors.  Its value is a path to a
// provider-neutral JSON file written by the supervisor or production
// launcher.  Keeping one file for all assistants avoids a singular ambient
// address/token environment variable and makes multi-assistant startup
// deterministic.
const AssistantRuntimeConfigEnv = "SCENERY_ASSISTANT_RUNTIME_CONFIG"

// AssistantTokenKeyEnv and AssistantTokenKeyFileEnv are the server-only
// stable sealing-key handoff used by generated assistant registrations. The
// file variant is the preferred local-development path; production may use
// either an existing secret environment or a mode-0600 file.
const (
	AssistantTokenKeyEnv     = "SCENERY_ASSISTANT_TOKEN_KEY"
	AssistantTokenKeyFileEnv = "SCENERY_ASSISTANT_TOKEN_KEY_FILE"
)

const (
	assistantRuntimeBootstrapService = "scenery/assistant-runtime"
	assistantRuntimeConfigLimit      = int64(1 << 20)
	assistantRuntimeProbeTimeout     = 30 * time.Second
	assistantRuntimeInitialProbe     = 250 * time.Millisecond
	assistantRuntimeRetryProbe       = 2 * time.Second
	assistantRuntimeRetryInitial     = 250 * time.Millisecond
	assistantRuntimeRetryMaximum     = 2 * time.Second
	maxAssistantRuntimeDescriptors   = 256
)

var (
	errAssistantRuntimeConfig  = errors.New("assistant runtime configuration is invalid")
	errAssistantRuntimeStopped = errors.New("assistant runtime bootstrap is stopped")
)

// AssistantBootstrapDescriptor is one private helper endpoint descriptor.
// ControlAddress and ControlToken are server-only values.  MarshalJSON omits
// both so status, diagnostics, and accidental config serialization cannot
// disclose a private address or credential.
type AssistantBootstrapDescriptor struct {
	AssistantAddress   string `json:"assistant_address"`
	ControlAddress     string `json:"-"`
	ControlToken       string `json:"-"`
	MCPListenAddress   string `json:"-"`
	MCPBridgeSecret    string `json:"-"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	Required           bool   `json:"required"`
}

// AssistantRuntimeConfig is the strict provider-neutral descriptor map
// supplied by the supervisor/production launcher.  A missing file means all
// declared assistants remain registered but unavailable.
type AssistantRuntimeConfig struct {
	Assistants []AssistantBootstrapDescriptor `json:"assistants"`
}

type assistantBootstrapDescriptorWire struct {
	AssistantAddress   string `json:"assistant_address"`
	ControlAddress     string `json:"control_address"`
	ControlToken       string `json:"control_token"`
	MCPListenAddress   string `json:"mcp_listen_address"`
	MCPBridgeSecret    string `json:"mcp_bridge_secret"`
	RuntimeRevision    string `json:"runtime_revision"`
	CapabilityRevision string `json:"capability_revision"`
	Required           bool   `json:"required"`
}

type assistantRuntimeConfigWire struct {
	Assistants []assistantBootstrapDescriptorWire `json:"assistants"`
}

// UnmarshalJSON accepts private descriptor fields only at the server-side
// config boundary.  The custom method is deliberately paired with
// MarshalJSON below so credentials and loopback addresses never serialize.
func (descriptor *AssistantBootstrapDescriptor) UnmarshalJSON(data []byte) error {
	type wire struct {
		AssistantAddress   string `json:"assistant_address"`
		ControlAddress     string `json:"control_address"`
		ControlToken       string `json:"control_token"`
		MCPListenAddress   string `json:"mcp_listen_address"`
		MCPBridgeSecret    string `json:"mcp_bridge_secret"`
		RuntimeRevision    string `json:"runtime_revision"`
		CapabilityRevision string `json:"capability_revision"`
		Required           *bool  `json:"required"`
	}
	var value wire
	if err := decodeAssistantJSON(data, &value); err != nil {
		return err
	}
	required := true
	if value.Required != nil {
		required = *value.Required
	}
	*descriptor = AssistantBootstrapDescriptor{
		AssistantAddress:   strings.TrimSpace(value.AssistantAddress),
		ControlAddress:     strings.TrimSpace(value.ControlAddress),
		ControlToken:       value.ControlToken,
		MCPListenAddress:   strings.TrimSpace(value.MCPListenAddress),
		MCPBridgeSecret:    value.MCPBridgeSecret,
		RuntimeRevision:    strings.TrimSpace(value.RuntimeRevision),
		CapabilityRevision: strings.TrimSpace(value.CapabilityRevision),
		Required:           required,
	}
	return nil
}

// MarshalJSON never includes the private helper address or control token.
func (descriptor AssistantBootstrapDescriptor) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AssistantAddress   string `json:"assistant_address"`
		RuntimeRevision    string `json:"runtime_revision"`
		CapabilityRevision string `json:"capability_revision"`
		Required           bool   `json:"required"`
	}{
		AssistantAddress:   descriptor.AssistantAddress,
		RuntimeRevision:    descriptor.RuntimeRevision,
		CapabilityRevision: descriptor.CapabilityRevision,
		Required:           descriptor.Required,
	})
}

// Validate checks the complete descriptor set before any client is created.
// It does not probe the helper; an unavailable or revision-mismatched helper
// is represented as an unavailable assistant while the Go app remains alive.
func (config AssistantRuntimeConfig) Validate() error {
	if len(config.Assistants) > maxAssistantRuntimeDescriptors {
		return fmt.Errorf("%w: too many assistants", errAssistantRuntimeConfig)
	}
	seen := make(map[string]struct{}, len(config.Assistants))
	for index, descriptor := range config.Assistants {
		address := strings.TrimSpace(descriptor.AssistantAddress)
		if address == "" {
			return fmt.Errorf("%w: assistant %d has no assistant_address", errAssistantRuntimeConfig, index)
		}
		if len(address) > 256 || strings.IndexFunc(address, func(r rune) bool { return r <= 0x20 || r == '\\' || r == '?' || r == '#' }) >= 0 {
			return fmt.Errorf("%w: assistant %d has an invalid assistant_address", errAssistantRuntimeConfig, index)
		}
		if _, exists := seen[address]; exists {
			return fmt.Errorf("%w: duplicate assistant_address", errAssistantRuntimeConfig)
		}
		seen[address] = struct{}{}
		if strings.TrimSpace(descriptor.ControlAddress) == "" {
			return fmt.Errorf("%w: assistant %s has no control_address", errAssistantRuntimeConfig, address)
		}
		if strings.TrimSpace(descriptor.ControlToken) == "" {
			return fmt.Errorf("%w: assistant %s has no control_token", errAssistantRuntimeConfig, address)
		}
		if (strings.TrimSpace(descriptor.MCPListenAddress) == "") != (strings.TrimSpace(descriptor.MCPBridgeSecret) == "") {
			return fmt.Errorf("%w: assistant %s has incomplete MCP bridge descriptor", errAssistantRuntimeConfig, address)
		}
		if descriptor.MCPListenAddress != "" {
			if err := validateAssistantMCPListenAddress(descriptor.MCPListenAddress); err != nil {
				return fmt.Errorf("%w: assistant %s MCP listen address: %v", errAssistantRuntimeConfig, address, err)
			}
			if len(descriptor.MCPBridgeSecret) < assistantMCPBridgeSecretMin || len(descriptor.MCPBridgeSecret) > 4096 || strings.TrimSpace(descriptor.MCPBridgeSecret) == "" || strings.IndexFunc(descriptor.MCPBridgeSecret, func(r rune) bool { return r <= 0x20 }) >= 0 {
				return fmt.Errorf("%w: assistant %s MCP bridge secret is invalid", errAssistantRuntimeConfig, address)
			}
		}
		if strings.TrimSpace(descriptor.RuntimeRevision) == "" || strings.TrimSpace(descriptor.CapabilityRevision) == "" {
			return fmt.Errorf("%w: assistant %s has incomplete revisions", errAssistantRuntimeConfig, address)
		}
		if err := validateBootstrapRevision(descriptor.RuntimeRevision); err != nil {
			return fmt.Errorf("%w: assistant %s runtime revision: %v", errAssistantRuntimeConfig, address, err)
		}
		if err := validateBootstrapRevision(descriptor.CapabilityRevision); err != nil {
			return fmt.Errorf("%w: assistant %s capability revision: %v", errAssistantRuntimeConfig, address, err)
		}
	}
	return nil
}

func validateBootstrapRevision(value string) error {
	if len(value) > 256 || strings.IndexFunc(strings.TrimSpace(value), func(r rune) bool { return r <= 0x20 || r == '\\' || r == '"' }) >= 0 {
		return errors.New("revision is invalid")
	}
	return nil
}

// LoadAssistantRuntimeConfig reads one strict descriptor file.  Unknown
// fields, duplicate JSON members, trailing values, and oversized files fail
// closed before any helper client is touched.
func LoadAssistantRuntimeConfig(path string) (AssistantRuntimeConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return AssistantRuntimeConfig{}, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errAssistantRuntimeConfig)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: descriptor file must be a private regular file", errAssistantRuntimeConfig)
	}
	file, err := os.Open(path)
	if err != nil {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errAssistantRuntimeConfig)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, assistantRuntimeConfigLimit+1))
	if err != nil {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: unable to read descriptor file", errAssistantRuntimeConfig)
	}
	if int64(len(data)) > assistantRuntimeConfigLimit {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: descriptor file is too large", errAssistantRuntimeConfig)
	}
	var config AssistantRuntimeConfig
	if err := decodeAssistantJSON(data, &config); err != nil {
		return AssistantRuntimeConfig{}, fmt.Errorf("%w: %v", errAssistantRuntimeConfig, err)
	}
	if err := config.Validate(); err != nil {
		return AssistantRuntimeConfig{}, err
	}
	return config, nil
}

// WriteAssistantRuntimeConfig writes the private supervisor descriptor map
// atomically with mode 0600. Unlike MarshalJSON, this explicit server-only
// writer includes control address/token fields because it is the handoff
// boundary to the generated app child; callers must never expose its bytes in
// public responses or diagnostics.
func WriteAssistantRuntimeConfig(path string, config AssistantRuntimeConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: descriptor path is required", errAssistantRuntimeConfig)
	}
	if err := config.Validate(); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if parent == "." {
		parent = ""
	}
	if parent != "" {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("%w: unable to create descriptor directory", errAssistantRuntimeConfig)
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: descriptor path is not a regular file", errAssistantRuntimeConfig)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: unable to inspect descriptor path", errAssistantRuntimeConfig)
	}
	wire := assistantRuntimeConfigWire{Assistants: make([]assistantBootstrapDescriptorWire, 0, len(config.Assistants))}
	for _, descriptor := range config.Assistants {
		wire.Assistants = append(wire.Assistants, assistantBootstrapDescriptorWire(descriptor))
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("%w: encode descriptor file", errAssistantRuntimeConfig)
	}
	temporary, err := os.CreateTemp(parentOrCurrent(parent), ".assistant-runtime-*")
	if err != nil {
		return fmt.Errorf("%w: create descriptor staging file", errAssistantRuntimeConfig)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("%w: protect descriptor staging file", errAssistantRuntimeConfig)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("%w: write descriptor staging file", errAssistantRuntimeConfig)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("%w: sync descriptor staging file", errAssistantRuntimeConfig)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close descriptor staging file", errAssistantRuntimeConfig)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("%w: install descriptor file", errAssistantRuntimeConfig)
	}
	removeTemporary = false
	if directory, err := os.Open(parentOrCurrent(parent)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func parentOrCurrent(parent string) string {
	if strings.TrimSpace(parent) == "" {
		return "."
	}
	return parent
}

// assistantTokenKeyFromRuntime resolves the framework-owned key without ever
// returning raw error text or falling back to process-random bytes. Invalid
// configured material fails closed (nil), leaving assistant routes registered
// but unable to mint/accept handles until the supervisor supplies a valid key.
func assistantTokenKeyFromRuntime() []byte {
	if value, present := envpolicy.Lookup(AssistantTokenKeyEnv); present && strings.TrimSpace(value) != "" {
		key, ok := parseAssistantTokenKey(value)
		if !ok {
			return nil
		}
		return key
	}
	path := strings.TrimSpace(envpolicy.Get(AssistantTokenKeyFileEnv))
	if path == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 || !utf8.Valid(data) {
		return nil
	}
	key, ok := parseAssistantTokenKey(string(data))
	if !ok {
		return nil
	}
	return key
}

func parseAssistantTokenKey(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	if key := []byte(value); validAssistantTokenKey(key) {
		return append([]byte(nil), key...), true
	}
	if key, err := hex.DecodeString(value); err == nil && validAssistantTokenKey(key) {
		return key, true
	}
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if key, err := encoding.DecodeString(value); err == nil && validAssistantTokenKey(key) {
			return key, true
		}
	}
	return nil, false
}

func validAssistantTokenKey(key []byte) bool {
	return len(key) == 32
}

// LoadAssistantRuntimeConfigFromEnv reads the configured descriptor path. An
// unset seam is valid and produces an empty config (all assistants remain
// neutral/unavailable).
func LoadAssistantRuntimeConfigFromEnv() (AssistantRuntimeConfig, error) {
	return LoadAssistantRuntimeConfig(envpolicy.Get(AssistantRuntimeConfigEnv))
}

func decodeAssistantJSON(data []byte, target any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// AssistantBootstrapStatus is safe for provider-neutral inspection.  It
// deliberately contains no control address, token, process details, or
// implementation/provider identity.
type AssistantBootstrapStatus struct {
	AssistantAddress   string `json:"assistant_address"`
	State              string `json:"state"`
	Required           bool   `json:"required"`
	RuntimeRevision    string `json:"runtime_revision,omitempty"`
	CapabilityRevision string `json:"capability_revision,omitempty"`
	ErrorCode          string `json:"error_code,omitempty"`
}

// AssistantClientFactory is injectable only to keep runtime bootstrap tests
// deterministic.  Production uses the provider-neutral HTTP client factory.
type AssistantClientFactory func(AssistantBootstrapDescriptor) (AssistantClient, error)

// AssistantBootstrapOptions configures the provider-neutral bootstrap
// manager. Zero values use the real HTTP client factory and a bounded probe
// timeout.
type AssistantBootstrapOptions struct {
	Factory      AssistantClientFactory
	ProbeTimeout time.Duration
}

// AssistantBootstrap owns helper client replacement and cleanup. It keeps
// public registrations intact while swapping only the private client pointer;
// an unavailable child therefore leaves the public surface alive and neutral.
type AssistantBootstrap struct {
	mu           sync.Mutex
	applyMu      sync.Mutex
	closed       bool
	factory      AssistantClientFactory
	probeTimeout time.Duration
	clients      map[string]AssistantClient
	statuses     map[string]AssistantBootstrapStatus
	retryCancel  context.CancelFunc
	retryWG      sync.WaitGroup
}

// NewAssistantBootstrap constructs a manager without reading process
// environment or making network requests.
func NewAssistantBootstrap(options AssistantBootstrapOptions) *AssistantBootstrap {
	factory := options.Factory
	if factory == nil {
		factory = defaultAssistantClientFactory
	}
	if options.ProbeTimeout <= 0 {
		options.ProbeTimeout = assistantRuntimeProbeTimeout
	}
	return &AssistantBootstrap{factory: factory, probeTimeout: options.ProbeTimeout, clients: map[string]AssistantClient{}, statuses: map[string]AssistantBootstrapStatus{}}
}

func defaultAssistantClientFactory(descriptor AssistantBootstrapDescriptor) (AssistantClient, error) {
	return assistantruntime.NewHTTPClient(assistantruntime.HTTPClientConfig{
		ControlBase:        descriptor.ControlAddress,
		ControlToken:       descriptor.ControlToken,
		AssistantAddress:   descriptor.AssistantAddress,
		RuntimeRevision:    descriptor.RuntimeRevision,
		CapabilityRevision: descriptor.CapabilityRevision,
	})
}

// Apply probes every configured helper and atomically commits successful
// clients. Failed probes remove any old client for that assistant; no
// capability request can continue using a stale child after a restart.
func (bootstrap *AssistantBootstrap) Apply(ctx context.Context, config AssistantRuntimeConfig) error {
	return bootstrap.apply(ctx, config, true)
}

func (bootstrap *AssistantBootstrap) apply(ctx context.Context, config AssistantRuntimeConfig, retryUnavailable bool) error {
	if bootstrap == nil {
		return errAssistantRuntimeStopped
	}
	bootstrap.applyMu.Lock()
	defer bootstrap.applyMu.Unlock()
	if err := config.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bootstrap.mu.Lock()
	if bootstrap.closed {
		bootstrap.mu.Unlock()
		return errAssistantRuntimeStopped
	}
	bootstrap.mu.Unlock()

	registered := listAssistants()
	registeredByAddress := make(map[string]AssistantRegistration, len(registered))
	for _, registration := range registered {
		registeredByAddress[registration.Address] = registration
	}
	for _, descriptor := range config.Assistants {
		if _, ok := registeredByAddress[descriptor.AssistantAddress]; !ok {
			return fmt.Errorf("%w: assistant %s is not registered", errAssistantRuntimeConfig, descriptor.AssistantAddress)
		}
	}
	descriptors := make(map[string]AssistantBootstrapDescriptor, len(config.Assistants))
	for _, descriptor := range config.Assistants {
		descriptors[descriptor.AssistantAddress] = descriptor
	}

	type result struct {
		client AssistantClient
		status AssistantBootstrapStatus
	}
	results := make(map[string]result, len(registered))
	for _, registration := range registered {
		descriptor, configured := descriptors[registration.Address]
		status := AssistantBootstrapStatus{AssistantAddress: registration.Address, State: string(assistantruntime.StateUnavailable), Required: registration.Required, RuntimeRevision: registration.RuntimeRevision, CapabilityRevision: registration.CapabilityRevision, ErrorCode: "unconfigured"}
		if !configured {
			results[registration.Address] = result{status: status}
			continue
		}
		status.Required = descriptor.Required
		status.RuntimeRevision = descriptor.RuntimeRevision
		status.CapabilityRevision = descriptor.CapabilityRevision
		if descriptor.MCPListenAddress != "" {
			if ready, errorCode := assistantMCPGatewayReady(registration.Address); !ready {
				status.ErrorCode = errorCode
				results[registration.Address] = result{status: status}
				continue
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, bootstrap.probeTimeout)
		client, err := bootstrap.factory(descriptor)
		if err == nil {
			if retryUnavailable {
				err = probeAssistantClientWithRetry(probeCtx, client, descriptor)
			} else {
				err = probeAssistantClient(probeCtx, client, descriptor)
			}
		}
		cancel()
		if err != nil {
			if client != nil {
				_ = client.Close()
			}
			status.ErrorCode = assistantBootstrapErrorCode(err)
			results[registration.Address] = result{status: status}
			continue
		}
		status.State = string(assistantruntime.StateReady)
		status.ErrorCode = ""
		results[registration.Address] = result{client: client, status: status}
	}

	bootstrap.mu.Lock()
	if bootstrap.closed {
		bootstrap.mu.Unlock()
		for _, item := range results {
			if item.client != nil {
				_ = item.client.Close()
			}
		}
		return errAssistantRuntimeStopped
	}
	newClients := make(map[string]AssistantClient)
	clientGeneration := make(map[string]AssistantClient, len(registered))
	for _, registration := range registered {
		address := registration.Address
		item := results[address]
		if item.client != nil {
			newClients[address] = item.client
		}
		clientGeneration[address] = item.client
		bootstrap.statuses[address] = item.status
	}
	previous, err := swapAssistantClients(clientGeneration)
	if err != nil {
		bootstrap.mu.Unlock()
		for _, item := range results {
			if item.client != nil {
				_ = item.client.Close()
			}
		}
		return err
	}
	bootstrap.clients = newClients
	bootstrap.mu.Unlock()
	for address, client := range previous {
		if client == nil || sameAssistantClient(client, clientGeneration[address]) {
			continue
		}
		_ = client.Close()
	}
	return nil
}

// startRetry owns one bounded retry loop for helpers that were unavailable at
// app startup. It deliberately has one goroutine per app bootstrap, not one
// goroutine per assistant or per request. The loop exits when every configured
// helper is ready, when its context is canceled, or when Close is called.
func (bootstrap *AssistantBootstrap) startRetry(ctx context.Context, config AssistantRuntimeConfig) {
	if bootstrap == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	retryCtx, cancel := context.WithCancel(ctx)
	bootstrap.mu.Lock()
	if bootstrap.closed || bootstrap.retryCancel != nil {
		bootstrap.mu.Unlock()
		cancel()
		return
	}
	bootstrap.retryCancel = cancel
	bootstrap.retryWG.Add(1)
	bootstrap.mu.Unlock()
	go func() {
		defer bootstrap.retryWG.Done()
		bootstrap.retryLoop(retryCtx, config)
	}()
}

func (bootstrap *AssistantBootstrap) retryLoop(ctx context.Context, config AssistantRuntimeConfig) {
	backoff := assistantRuntimeRetryInitial
	for {
		if bootstrap.allConfiguredReady(config) {
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, assistantRuntimeRetryProbe)
		_ = bootstrap.apply(attemptCtx, config, false)
		cancel()
		if bootstrap.allConfiguredReady(config) {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < assistantRuntimeRetryMaximum {
			backoff *= 2
			if backoff > assistantRuntimeRetryMaximum {
				backoff = assistantRuntimeRetryMaximum
			}
		}
	}
}

func (bootstrap *AssistantBootstrap) allConfiguredReady(config AssistantRuntimeConfig) bool {
	bootstrap.mu.Lock()
	defer bootstrap.mu.Unlock()
	for _, descriptor := range config.Assistants {
		status, ok := bootstrap.statuses[descriptor.AssistantAddress]
		if !ok || status.State != string(assistantruntime.StateReady) {
			return false
		}
	}
	return true
}

func probeAssistantClient(ctx context.Context, client AssistantClient, descriptor AssistantBootstrapDescriptor) error {
	if client == nil {
		return assistantruntime.ErrUnavailable
	}
	health, err := client.Health(ctx)
	if err != nil {
		return err
	}
	if !health.Ready {
		return assistantruntime.ErrUnavailable
	}
	if health.RuntimeRevision != descriptor.RuntimeRevision || health.CapabilityRevision != descriptor.CapabilityRevision {
		return fmt.Errorf("%w: helper health revisions do not match", assistantruntime.ErrRevisionMismatch)
	}
	info, err := client.Info(ctx)
	if err != nil {
		return err
	}
	if info.AssistantAddress != descriptor.AssistantAddress || info.RuntimeRevision != descriptor.RuntimeRevision || info.CapabilityRevision != descriptor.CapabilityRevision {
		return fmt.Errorf("%w: helper info revisions do not match", assistantruntime.ErrRevisionMismatch)
	}
	return nil
}

func sameAssistantClient(left, right AssistantClient) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() || !leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}

func assistantBootstrapErrorCode(err error) string {
	switch {
	case errors.Is(err, assistantruntime.ErrRevisionMismatch):
		return "revision_mismatch"
	case errors.Is(err, assistantruntime.ErrInvalidControlAddress), errors.Is(err, assistantruntime.ErrInvalidClientConfig):
		return "invalid_descriptor"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, assistantruntime.ErrUnavailable):
		return "unavailable"
	default:
		return "unavailable"
	}
}

// Statuses returns a stable, provider-neutral snapshot sorted by assistant
// address. Private descriptor fields are never present in the result.
func (bootstrap *AssistantBootstrap) Statuses() []AssistantBootstrapStatus {
	if bootstrap == nil {
		return nil
	}
	bootstrap.mu.Lock()
	defer bootstrap.mu.Unlock()
	result := make([]AssistantBootstrapStatus, 0, len(bootstrap.statuses))
	for _, status := range bootstrap.statuses {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AssistantAddress < result[j].AssistantAddress })
	return result
}

// Close unregisters every managed helper client and is idempotent. Public
// assistant endpoints remain registered and return the neutral unavailable
// response after cleanup.
func (bootstrap *AssistantBootstrap) Close() error {
	if bootstrap == nil {
		return nil
	}
	bootstrap.mu.Lock()
	if bootstrap.closed {
		bootstrap.mu.Unlock()
		return nil
	}
	bootstrap.closed = true
	retryCancel := bootstrap.retryCancel
	bootstrap.retryCancel = nil
	bootstrap.mu.Unlock()
	if retryCancel != nil {
		retryCancel()
		bootstrap.retryWG.Wait()
	}
	bootstrap.applyMu.Lock()
	defer bootstrap.applyMu.Unlock()
	bootstrap.mu.Lock()
	clients := bootstrap.clients
	bootstrap.clients = map[string]AssistantClient{}
	for address, status := range bootstrap.statuses {
		status.State = string(assistantruntime.StateStopped)
		bootstrap.statuses[address] = status
	}
	bootstrap.mu.Unlock()
	for address, client := range clients {
		_ = UnregisterAssistantClient(address, client)
		_ = client.Close()
	}
	return nil
}

// BootstrapAssistantRuntime is the generated-app entry point. It creates a
// provider-neutral client manager, reads the one descriptor seam, verifies
// health/info revisions, and leaves unavailable assistants neutral.
func BootstrapAssistantRuntime(ctx context.Context) (*AssistantBootstrap, error) {
	return bootstrapAssistantRuntime(ctx, AssistantBootstrapOptions{})
}

func bootstrapAssistantRuntime(ctx context.Context, options AssistantBootstrapOptions) (*AssistantBootstrap, error) {
	config, err := LoadAssistantRuntimeConfigFromEnv()
	if err != nil {
		return nil, err
	}
	bootstrap := NewAssistantBootstrap(options)
	if ctx == nil {
		ctx = context.Background()
	}
	initialProbeTimeout := min(bootstrap.probeTimeout, assistantRuntimeInitialProbe)
	probeCtx, cancel := context.WithTimeout(ctx, initialProbeTimeout)
	err = bootstrap.apply(probeCtx, config, false)
	cancel()
	if err != nil {
		_ = bootstrap.Close()
		return nil, err
	}
	bootstrap.startRetry(ctx, config)
	return bootstrap, nil
}

var activeAssistantBootstrap struct {
	sync.Mutex
	value *AssistantBootstrap
}

// AssistantRuntimeStatuses returns the active bootstrap snapshot for
// provider-neutral inspection. If startup has not run yet, declared assistants
// are reported as unconfigured without exposing any private descriptor data.
func AssistantRuntimeStatuses() []AssistantBootstrapStatus {
	activeAssistantBootstrap.Lock()
	bootstrap := activeAssistantBootstrap.value
	activeAssistantBootstrap.Unlock()
	if bootstrap != nil {
		return bootstrap.Statuses()
	}
	registered := listAssistants()
	statuses := make([]AssistantBootstrapStatus, 0, len(registered))
	for _, registration := range registered {
		statuses = append(statuses, AssistantBootstrapStatus{
			AssistantAddress:   registration.Address,
			State:              string(assistantruntime.StateUnavailable),
			Required:           registration.Required,
			RuntimeRevision:    registration.RuntimeRevision,
			CapabilityRevision: registration.CapabilityRevision,
			ErrorCode:          "unconfigured",
		})
	}
	return statuses
}

// bootstrapAssistantRuntimeService is installed with the generated assistant
// registration. It intentionally absorbs config/probe errors so a required
// helper outage cannot terminate the public Go app.
func bootstrapAssistantRuntimeService(ctx context.Context) error {
	bootstrap, err := BootstrapAssistantRuntime(ctx)
	if err != nil {
		// Keep the app alive; no client is registered, so the public gateway's
		// existing neutral unavailable path is used. Do not log raw errors that
		// could contain a private address or implementation detail.
		activeAssistantBootstrap.Lock()
		previous := activeAssistantBootstrap.value
		activeAssistantBootstrap.value = nil
		activeAssistantBootstrap.Unlock()
		if previous != nil {
			_ = previous.Close()
		}
		return nil
	}
	activeAssistantBootstrap.Lock()
	previous := activeAssistantBootstrap.value
	activeAssistantBootstrap.value = bootstrap
	activeAssistantBootstrap.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

func shutdownAssistantRuntimeService(context.Context) error {
	activeAssistantBootstrap.Lock()
	bootstrap := activeAssistantBootstrap.value
	activeAssistantBootstrap.value = nil
	activeAssistantBootstrap.Unlock()
	if bootstrap == nil {
		return nil
	}
	return bootstrap.Close()
}

func ensureAssistantBootstrapService() {
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.serviceInitializers[assistantRuntimeBootstrapService]; exists {
		return
	}
	global.serviceInitializers[assistantRuntimeBootstrapService] = serviceInitializer{
		service:    assistantRuntimeBootstrapService,
		initialize: bootstrapAssistantRuntimeService,
		shutdown:   shutdownAssistantRuntimeService,
	}
}
