// Package mcpgateway exposes a private, provider-neutral MCP gateway.
//
// The gateway deliberately sits below the public router.  It owns the
// loopback Streamable HTTP listener, request authentication, capability
// filtering, and the translation between MCP tool calls and Scenery's neutral
// dispatcher.  Provider implementations are represented only by interfaces in
// this package; no provider package is imported here.
package mcpgateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/mcpcontract"
)

const (
	// AssertionHeader is the private request header consumed by the default
	// HMAC verifier.  The header is intentionally not an Authorization header:
	// application credentials must never be forwarded to the helper process.
	AssertionHeader = "Scenery-Assistant-Assertion"

	// ProtocolVersion is the one MCP revision accepted by this gateway.
	ProtocolVersion = mcpcontract.ProtocolVersion

	defaultMaxBodyBytes = int64(16 << 20)
)

var (
	// ErrUnauthorized is returned when a request does not carry a valid,
	// non-expired assistant assertion.
	ErrUnauthorized = errors.New("assistant assertion is missing or invalid")
	// ErrStaleCapability is returned when a request was signed against an old
	// capability revision.
	ErrStaleCapability = errors.New("assistant capability revision is stale")
	// ErrPrivateAddress is returned when a request attempts to reach the private
	// listener through a non-loopback address or host/origin.
	ErrPrivateAddress = errors.New("private MCP gateway requires a loopback address")
	// ErrFederationUnavailable is returned when one or more required external
	// MCP connections are not currently ready.  The gateway fails closed rather
	// than exposing a stale remote inventory.
	ErrFederationUnavailable = errors.New("required federated MCP connection is unavailable")
	// ErrFederationToolCollision is returned when a federated tool would shadow
	// a local or framework-owned tool name.
	ErrFederationToolCollision = errors.New("federated MCP tool name collides with a local tool")
	// ErrFederationCall is the safe, provider-neutral error exposed when a
	// federated transport fails.  Remote URLs, headers, and credential-bearing
	// errors are intentionally never copied into the public result.
	ErrFederationCall = errors.New("federated MCP tool call failed")
)

var gatewayToolNameRE = regexp.MustCompile(mcpcontract.ToolNamePattern)

// ToolDispatcher is the generated, provider-neutral execution boundary.  The
// gateway never calls an app service directly.
type ToolDispatcher = mcpcontract.ToolDispatcher

// AssertionVerifier authenticates every HTTP request, including requests on an
// existing MCP session.  The returned context is copied into the MCP tool
// handler context.
type AssertionVerifier interface {
	Verify(context.Context, *http.Request) (mcpcontract.ToolCallContext, error)
}

// CapabilityAuthorizer applies principal-specific visibility and authorization
// checks.  Returning false hides the capability from tools/list and rejects
// direct calls as if the tool did not exist.
type CapabilityAuthorizer interface {
	Visible(context.Context, string, mcpcontract.Capability) bool
}

// DurableOperations backs the framework-owned status and cancel tools.  The
// JSON result is deliberately opaque to this package so that execution storage
// remains an application/runtime concern.
type DurableOperations interface {
	Status(context.Context, mcpcontract.ToolCallContext, string) (json.RawMessage, error)
	Cancel(context.Context, mcpcontract.ToolCallContext, string) (json.RawMessage, error)
}

// FederatedTools is the narrow gateway boundary implemented by the external
// MCP federation.  The gateway consumes only the ready, policy-projected
// capability inventory and dispatches through the same assertion-derived
// ToolCallContext used by local operations.  URL and authentication material
// never cross this interface.
type FederatedTools interface {
	Ready() bool
	Capabilities() []mcpcontract.Capability
	CallTool(context.Context, mcpcontract.ToolCallContext, string, json.RawMessage) (mcpcontract.ToolOutcome, error)
}

// Config configures a private gateway.
type Config struct {
	// Manifest is the immutable generated capability manifest.
	Manifest mcpcontract.Manifest
	// CapabilityRevision is the revision expected in every assistant assertion.
	// If empty, Manifest.ContractRevision is used.
	CapabilityRevision string
	// Verify authenticates every incoming request.  A nil verifier fails closed.
	Verify AssertionVerifier
	// Dispatch executes authorized local operations.
	Dispatch ToolDispatcher
	// Authorizer controls principal-specific capability visibility.
	Authorizer CapabilityAuthorizer
	// Durable provides status/cancel operations.  Framework tools are only
	// exposed when Durable is non-nil and at least one manifest capability is
	// marked durable.
	Durable DurableOperations
	// Federation supplies currently-ready external MCP tools.  Optional
	// connections are omitted by the federation; an unready required
	// connection makes tools/list fail closed.
	Federation FederatedTools
	// MaxBodyBytes bounds every HTTP request body.  Zero uses 16 MiB.
	MaxBodyBytes int64
	// SessionTimeout bounds idle SDK sessions.  Zero leaves the SDK default.
	SessionTimeout time.Duration
	// ListenAddr is ignored unless it is a loopback TCP address.  Empty uses
	// 127.0.0.1:0 (an allocated ephemeral port).
	ListenAddr string
	// Version appears in the MCP server implementation metadata.
	Version string
}

// Gateway is a private MCP gateway and its loopback listener.  Constructing a
// Gateway allocates the listener but does not start serving; call Serve in a
// goroutine and Close during shutdown.
type Gateway struct {
	Handler http.Handler
	URL     string

	listener  net.Listener
	server    *http.Server
	closeOnce sync.Once
	closeErr  error
}

type gateway struct {
	cfg Config

	// sessions binds an SDK session ID to the complete stable assistant context
	// that authenticated its initial request. The SDK itself keeps sessions
	// private, so this map is the additional Scenery authorization boundary for
	// every subsequent request.
	sessionsMu sync.Mutex
	sessions   map[string]sessionBinding

	sdk http.Handler
}

type contextKey uint8

var requestIDCounter atomic.Uint64

const (
	assertionContextKey contextKey = iota + 1
)

// sessionBinding is the stable portion of an assistant assertion. Per-call
// values (request ID, trace context, and idempotency key) deliberately do not
// participate: a session may issue many independent tool calls, while its
// principal, assistant, conversation, and capability revision must not change.
type sessionBinding struct {
	Principal          string
	AssistantAddress   string
	ConversationDigest string
	CapabilityRevision string
}

func newSessionBinding(callCtx mcpcontract.ToolCallContext) sessionBinding {
	return sessionBinding{
		Principal:          callCtx.Principal,
		AssistantAddress:   callCtx.AssistantAddress,
		ConversationDigest: callCtx.ConversationDigest,
		CapabilityRevision: callCtx.CapabilityRevision,
	}
}

func (binding sessionBinding) equal(other sessionBinding) bool {
	return subtle.ConstantTimeCompare([]byte(binding.Principal), []byte(other.Principal)) == 1 &&
		subtle.ConstantTimeCompare([]byte(binding.AssistantAddress), []byte(other.AssistantAddress)) == 1 &&
		subtle.ConstantTimeCompare([]byte(binding.ConversationDigest), []byte(other.ConversationDigest)) == 1 &&
		subtle.ConstantTimeCompare([]byte(binding.CapabilityRevision), []byte(other.CapabilityRevision)) == 1
}

// New creates a private gateway with an allocated loopback listener.
func New(cfg Config) (*Gateway, error) {
	if err := normalizeConfig(&cfg); err != nil {
		return nil, err
	}
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("mcpgateway listen: %w", err)
	}
	if !isLoopbackAddr(ln.Addr().String()) {
		_ = ln.Close()
		return nil, ErrPrivateAddress
	}

	g := &gateway{cfg: cfg, sessions: make(map[string]sessionBinding)}
	g.sdk = g.newSDKHandler()
	gatewayHandler := http.HandlerFunc(g.serveHTTP)

	s := &http.Server{Handler: gatewayHandler}
	return &Gateway{
		Handler:  gatewayHandler,
		URL:      "http://" + ln.Addr().String(),
		listener: ln,
		server:   s,
	}, nil
}

// Serve runs the HTTP server until the listener is closed.  A canceled context
// initiates a graceful shutdown and returns the context error only if shutdown
// itself succeeds.
func (g *Gateway) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = g.Close()
		case <-stop:
		}
	}()
	err := g.server.Serve(g.listener)
	close(stop)
	if errors.Is(err, http.ErrServerClosed) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	return err
}

// Close stops the listener and waits for in-flight HTTP requests to finish.
func (g *Gateway) Close() error {
	g.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.closeErr = g.server.Shutdown(ctx)
		if err := g.listener.Close(); g.closeErr == nil && err != nil && !errors.Is(err, net.ErrClosed) {
			g.closeErr = err
		}
	})
	return g.closeErr
}

// NewHandler builds a private gateway handler without allocating a listener.
// This is useful for tests that mount the handler on httptest.Server.  The
// returned handler still enforces loopback Host/Origin and assertion checks.
func NewHandler(cfg Config) (http.Handler, error) {
	if err := normalizeConfig(&cfg); err != nil {
		return nil, err
	}
	g := &gateway{cfg: cfg, sessions: make(map[string]sessionBinding)}
	g.sdk = g.newSDKHandler()
	return http.HandlerFunc(g.serveHTTP), nil
}

func normalizeConfig(cfg *Config) error {
	if cfg.Dispatch == nil {
		return errors.New("mcpgateway: nil tool dispatcher")
	}
	if cfg.Verify == nil {
		return errors.New("mcpgateway: nil assertion verifier")
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	if cfg.MaxBodyBytes > defaultMaxBodyBytes {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	if cfg.CapabilityRevision == "" {
		cfg.CapabilityRevision = cfg.Manifest.ContractRevision
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if err := cfg.Manifest.Validate(); err != nil {
		return fmt.Errorf("mcpgateway manifest: %w", err)
	}
	for _, capability := range cfg.Manifest.Capabilities {
		if capability.Name == mcpcontract.StatusToolName || capability.Name == mcpcontract.CancelToolName {
			return fmt.Errorf("mcpgateway manifest reserves framework tool name %q", capability.Name)
		}
	}
	return nil
}

func (g *gateway) newSDKHandler() http.Handler {
	getServer := func(req *http.Request) *mcp.Server {
		ctx := req.Context()
		callCtx, _ := ctx.Value(assertionContextKey).(mcpcontract.ToolCallContext)
		return g.serverFor(ctx, callCtx)
	}
	return mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		SessionTimeout: g.cfg.SessionTimeout,
		// Keep the SDK's built-in DNS rebinding protection enabled.  The outer
		// middleware adds the stricter loopback/Origin checks required by Scenery.
		DisableLocalhostProtection: false,
	})
}

func (g *gateway) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if err := validatePrivateRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err := g.prepareBodyAndProtocol(w, req); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	callCtx, err := g.cfg.Verify.Verify(req.Context(), req)
	if err != nil {
		http.Error(w, ErrUnauthorized.Error(), http.StatusUnauthorized)
		return
	}
	if g.cfg.CapabilityRevision != "" && callCtx.CapabilityRevision != g.cfg.CapabilityRevision {
		http.Error(w, ErrStaleCapability.Error(), http.StatusConflict)
		return
	}

	if sessionID := req.Header.Get("Mcp-Session-Id"); sessionID != "" {
		if !g.acceptSessionBinding(sessionID, newSessionBinding(callCtx)) {
			http.Error(w, "MCP session assertion context mismatch", http.StatusForbidden)
			return
		}
	}
	req = req.WithContext(context.WithValue(req.Context(), assertionContextKey, callCtx))
	capWriter := &captureWriter{ResponseWriter: w}
	g.sdk.ServeHTTP(capWriter, req)

	// The SDK emits the session ID on the initialize response.  Bind it to the
	// authenticated principal before returning, so subsequent requests cannot
	// swap assertions.  DELETE removes the binding after SDK cleanup.
	if sessionID := capWriter.Header().Get("Mcp-Session-Id"); sessionID != "" {
		g.bindSession(sessionID, newSessionBinding(callCtx))
	}
	if req.Method == http.MethodDelete {
		if sessionID := req.Header.Get("Mcp-Session-Id"); sessionID != "" {
			g.removeSession(sessionID)
		}
	}
}

var errBodyTooLarge = errors.New("MCP request body exceeds configured limit")

func (g *gateway) prepareBodyAndProtocol(w http.ResponseWriter, req *http.Request) error {
	if req.Method == http.MethodPost {
		// Read through MaxBytesReader before the SDK sees the stream, then restore
		// the body for the JSON-RPC decoder.
		if req.Body == nil {
			req.Body = http.NoBody
		}
		limited := http.MaxBytesReader(w, req.Body, g.cfg.MaxBodyBytes+1)
		body, err := io.ReadAll(limited)
		if err != nil {
			if errors.As(err, new(*http.MaxBytesError)) || int64(len(body)) > g.cfg.MaxBodyBytes {
				return errBodyTooLarge
			}
			return fmt.Errorf("read MCP request: %w", err)
		}
		if int64(len(body)) > g.cfg.MaxBodyBytes {
			return errBodyTooLarge
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if err := enforceProtocolVersion(req, body); err != nil {
			return err
		}
	} else {
		version := req.Header.Get("Mcp-Protocol-Version")
		if version != "" && version != ProtocolVersion {
			return fmt.Errorf("unsupported MCP protocol version %q", version)
		}
		if req.Header.Get("Mcp-Session-Id") != "" && version == "" {
			return errors.New("MCP protocol version header is required after initialize")
		}
	}
	return nil
}

func enforceProtocolVersion(req *http.Request, body []byte) error {
	if version := req.Header.Get("Mcp-Protocol-Version"); version != "" && version != ProtocolVersion {
		return fmt.Errorf("unsupported MCP protocol version %q", version)
	}
	var messages []json.RawMessage
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return nil
		}
	} else {
		messages = []json.RawMessage{trimmed}
	}
	for _, message := range messages {
		var envelope struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil || envelope.Method != "initialize" {
			continue
		}
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(envelope.Params, &params); err != nil || params.ProtocolVersion != ProtocolVersion {
			return fmt.Errorf("unsupported MCP protocol version %q", params.ProtocolVersion)
		}
	}
	if req.Header.Get("Mcp-Protocol-Version") == "" && req.Header.Get("Mcp-Session-Id") != "" {
		return errors.New("MCP protocol version header is required after initialize")
	}
	return nil
}

func validatePrivateRequest(req *http.Request) error {
	if req == nil {
		return ErrPrivateAddress
	}
	if req.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil || !isLoopbackAddr(host) {
			return ErrPrivateAddress
		}
	}
	host := req.Host
	if host == "" {
		return ErrPrivateAddress
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if !isLoopbackAddr(host) && !strings.EqualFold(host, "localhost") {
		return ErrPrivateAddress
	}
	if origin := req.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" || (!isLoopbackAddr(u.Hostname()) && !strings.EqualFold(u.Hostname(), "localhost")) {
			return ErrPrivateAddress
		}
	}
	return nil
}

func isLoopbackAddr(address string) bool {
	if address == "localhost" {
		return true
	}
	address = strings.Trim(address, "[]")
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}

func (g *gateway) acceptSessionBinding(sessionID string, binding sessionBinding) bool {
	g.sessionsMu.Lock()
	defer g.sessionsMu.Unlock()
	bound, ok := g.sessions[sessionID]
	return !ok || bound.equal(binding)
}

func (g *gateway) bindSession(sessionID string, binding sessionBinding) {
	g.sessionsMu.Lock()
	// Never overwrite an established binding. The request that created a
	// session has no session header, but this also protects against races where
	// duplicate initialize responses expose the same SDK session ID.
	if existing, ok := g.sessions[sessionID]; !ok || existing.equal(binding) {
		g.sessions[sessionID] = binding
	}
	g.sessionsMu.Unlock()
}

func (g *gateway) removeSession(sessionID string) {
	g.sessionsMu.Lock()
	delete(g.sessions, sessionID)
	g.sessionsMu.Unlock()
}

func (g *gateway) serverFor(ctx context.Context, callCtx mcpcontract.ToolCallContext) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "scenery-private-mcp", Version: g.cfg.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	// Build the per-request inventory from the current assertion and the
	// federation snapshot.  A required outage or a collision is remembered by
	// the receiving middleware below so the SDK returns a typed JSON-RPC error
	// instead of silently serving a stale/ambiguous list.
	remoteCapabilities, federationErr := g.federatedCapabilities(ctx, callCtx)
	for _, capability := range g.cfg.Manifest.Capabilities {
		if capability.Name == "" || (g.cfg.Authorizer != nil && !g.cfg.Authorizer.Visible(ctx, callCtx.Principal, capability)) {
			continue
		}
		capability := capability
		inputSchema := schemaValue(capability.InputSchema)
		outputSchema := schemaValue(capability.OutputSchema)
		server.AddTool(&mcp.Tool{
			Name:         capability.Name,
			Title:        capability.Title,
			Description:  capability.Description,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
			Annotations:  capabilityAnnotations(capability.Effect),
		}, g.toolHandler(capability))
	}
	for _, capability := range remoteCapabilities {
		capability := capability
		server.AddTool(&mcp.Tool{
			Name:         capability.Name,
			Title:        capability.Title,
			Description:  capability.Description,
			InputSchema:  schemaValue(capability.InputSchema),
			OutputSchema: schemaValue(capability.OutputSchema),
			Annotations:  capabilityAnnotations(capability.Effect),
		}, g.toolHandler(capability))
	}
	if g.cfg.Durable != nil && hasDurableCapability(ctx, callCtx.Principal, g.cfg.Manifest.Capabilities, g.cfg.Authorizer) {
		g.addFrameworkTools(server)
	}
	if g.cfg.Federation != nil {
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(methodCtx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method != "tools/list" && method != "tools/call" {
					return next(methodCtx, method, req)
				}
				if federationErr != nil {
					return nil, federationErr
				}
				return next(methodCtx, method, req)
			}
		})
	}
	return server
}

func capabilityAnnotations(effect mcpcontract.Effect) *mcp.ToolAnnotations {
	destructive, openWorld := effect.Destructive, effect.OpenWorld
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    effect.ReadOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  effect.Idempotent,
		OpenWorldHint:   &openWorld,
	}
}

// federatedCapabilities returns only tools that are ready and authorized for
// the current assertion.  Collision checks deliberately happen before the
// authorizer so hiding a capability for one principal cannot make a global
// name collision disappear for another principal.
func (g *gateway) federatedCapabilities(ctx context.Context, callCtx mcpcontract.ToolCallContext) ([]mcpcontract.Capability, error) {
	if g.cfg.Federation == nil {
		return nil, nil
	}
	if !g.cfg.Federation.Ready() {
		return nil, ErrFederationUnavailable
	}
	capabilities := append([]mcpcontract.Capability(nil), g.cfg.Federation.Capabilities()...)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	localNames := make(map[string]struct{}, len(g.cfg.Manifest.Capabilities)+2)
	for _, capability := range g.cfg.Manifest.Capabilities {
		localNames[capability.Name] = struct{}{}
	}
	localNames[mcpcontract.StatusToolName] = struct{}{}
	localNames[mcpcontract.CancelToolName] = struct{}{}
	seen := make(map[string]struct{}, len(capabilities))
	visible := make([]mcpcontract.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		if !gatewayToolNameRE.MatchString(capability.Name) || capability.Origin.Kind != "federated" {
			return nil, ErrFederationToolCollision
		}
		if _, exists := localNames[capability.Name]; exists {
			return nil, ErrFederationToolCollision
		}
		if _, exists := seen[capability.Name]; exists {
			return nil, ErrFederationToolCollision
		}
		seen[capability.Name] = struct{}{}
		if g.cfg.Authorizer != nil && !g.cfg.Authorizer.Visible(ctx, callCtx.Principal, capability) {
			continue
		}
		visible = append(visible, capability)
	}
	return visible, nil
}

func schemaValue(raw json.RawMessage) any {
	var schema map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema == nil {
		schema = map[string]any{"type": "object"}
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	return schema
}

func (g *gateway) toolHandler(capability mcpcontract.Capability) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req == nil || req.Params == nil {
			return nil, errors.New("missing MCP tool parameters")
		}
		callCtx, ok := ctx.Value(assertionContextKey).(mcpcontract.ToolCallContext)
		if !ok {
			return nil, ErrUnauthorized
		}
		if g.cfg.Authorizer != nil && !g.cfg.Authorizer.Visible(ctx, callCtx.Principal, capability) {
			return nil, ErrUnauthorized
		}
		args := req.Params.Arguments
		if len(args) > capability.Limits.MaxInputBytes && capability.Limits.MaxInputBytes > 0 {
			return errorToolResult(errors.New("tool input exceeds configured limit")), nil
		}
		// Request IDs are gateway-owned invocation identities. Never honor a
		// request ID from the signed assertion or an HTTP header supplied by the
		// MCP client: neither is authoritative for this tool invocation.
		callCtx.RequestID = newRequestID()
		var (
			outcome mcpcontract.ToolOutcome
			err     error
		)
		if capability.Origin.Kind == "federated" {
			if g.cfg.Federation == nil {
				return errorToolResult(ErrFederationCall), nil
			}
			outcome, err = g.cfg.Federation.CallTool(ctx, callCtx, capability.Name, args)
			if err != nil {
				// Keep cancellation and deadline semantics useful to callers while
				// preventing remote transports from leaking URLs or credentials.
				if errors.Is(err, context.Canceled) {
					err = context.Canceled
				} else if errors.Is(err, context.DeadlineExceeded) {
					err = context.DeadlineExceeded
				} else {
					err = ErrFederationCall
				}
			}
		} else {
			outcome, err = g.cfg.Dispatch.CallTool(ctx, callCtx, capability.Name, args)
		}
		if err != nil {
			return errorToolResult(err), nil
		}
		result, encoded := outcomeResult(outcome)
		if limit := capability.Limits.MaxResultBytes; limit > 0 && int64(len(encoded)) > int64(limit) {
			return errorToolResult(errors.New("tool result exceeds configured limit")), nil
		}
		return result, nil
	}
}

func errorToolResult(err error) *mcp.CallToolResult {
	problem, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: "internal_error", Message: err.Error()})
	result, _ := outcomeResult(mcpcontract.ToolOutcome{
		Outcome: "failed",
		Problem: problem,
	})
	result.IsError = true
	return result
}

func outcomeResult(outcome mcpcontract.ToolOutcome) (*mcp.CallToolResult, []byte) {
	encoded, err := mcpcontract.MarshalOutcome(outcome)
	if err != nil {
		if outcome.Outcome == "failed" && len(outcome.Problem) > 0 {
			// The fallback is guaranteed valid and avoids recursive error mapping.
			problem, _ := json.Marshal(struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}{Code: "internal_error", Message: "invalid tool outcome"})
			outcome = mcpcontract.ToolOutcome{Outcome: "failed", Problem: problem}
			encoded, _ = mcpcontract.MarshalOutcome(outcome)
		} else {
			return errorToolResult(err), nil
		}
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return errorToolResult(err), nil
	}
	result := &mcp.CallToolResult{StructuredContent: envelope}
	if len(outcome.Problem) > 0 {
		result.IsError = true
	}
	result.Content = []mcp.Content{&mcp.TextContent{Text: string(encoded)}}
	return result, encoded
}

func hasDurableCapability(ctx context.Context, principal string, capabilities []mcpcontract.Capability, authorizer CapabilityAuthorizer) bool {
	for _, capability := range capabilities {
		if capability.Durable && (authorizer == nil || authorizer.Visible(ctx, principal, capability)) {
			return true
		}
	}
	return false
}

func (g *gateway) addFrameworkTools(server *mcp.Server) {
	statusName := mcpcontract.StatusToolName
	cancelName := mcpcontract.CancelToolName
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"execution_id": map[string]any{"type": "string"}},
		"required":   []any{"execution_id"},
	}
	server.AddTool(&mcp.Tool{Name: statusName, Description: "Read durable execution status", InputSchema: schema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.frameworkCall(ctx, req, false)
	})
	server.AddTool(&mcp.Tool{Name: cancelName, Description: "Cancel durable execution", InputSchema: schema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.frameworkCall(ctx, req, true)
	})
}

func (g *gateway) frameworkCall(ctx context.Context, req *mcp.CallToolRequest, cancel bool) (*mcp.CallToolResult, error) {
	if req == nil || req.Params == nil {
		return nil, errors.New("missing execution parameters")
	}
	var input struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil || input.ExecutionID == "" {
		return errorToolResult(errors.New("execution_id is required")), nil
	}
	callCtx, ok := ctx.Value(assertionContextKey).(mcpcontract.ToolCallContext)
	if !ok {
		return nil, ErrUnauthorized
	}
	callCtx.RequestID = newRequestID()
	var (
		value json.RawMessage
		err   error
	)
	if cancel {
		value, err = g.cfg.Durable.Cancel(ctx, callCtx, input.ExecutionID)
	} else {
		value, err = g.cfg.Durable.Status(ctx, callCtx, input.ExecutionID)
	}
	if err != nil {
		return errorToolResult(err), nil
	}
	result, encoded := outcomeResult(mcpcontract.ToolOutcome{Outcome: "completed", Value: value})
	if int64(len(encoded)) > defaultMaxBodyBytes {
		return errorToolResult(errors.New("durable result exceeds configured limit")), nil
	}
	return result, nil
}

func newRequestID() string {
	return fmt.Sprintf("mcp-%d-%d", time.Now().UnixNano(), requestIDCounter.Add(1))
}

type captureWriter struct {
	http.ResponseWriter
	status int
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
