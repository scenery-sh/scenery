// Package mcpfederation connects Scenery-owned MCP connections to remote
// Streamable HTTP servers.
//
// The package is deliberately provider-neutral.  A connection owns its
// endpoint and authentication material, while the returned tool inventory
// contains only remote metadata and Scenery's local policy.  Remote metadata
// is never used to decide whether a call needs approval or may have effects.
package mcpfederation

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/mcpcontract"
)

const (
	// ProtocolVersion is the only MCP protocol revision accepted by the
	// federation client.  The official SDK may negotiate older revisions, but
	// Scenery intentionally fails closed instead of silently changing wire
	// behavior.
	ProtocolVersion = mcpcontract.ProtocolVersion

	defaultConnectTimeout = 10 * time.Second
	defaultCallTimeout    = 30 * time.Second
	defaultRefreshTTL     = 5 * time.Minute
	defaultRefreshEvery   = time.Second
	defaultDiagnosticTTL  = time.Minute
	defaultMaxInputBytes  = mcpcontract.MaximumInputBytes
	defaultMaxResultBytes = mcpcontract.MaximumResultBytes
	maxToolDescription    = 64 << 10
	maxToolTitle          = 64 << 10
	maxToolSchema         = 1 << 20
	maxHTTPResponse       = 32 << 20
	defaultMaxTools       = 4096
	maxRefreshTTL         = 24 * time.Hour
	maxDiagnosticTTL      = 24 * time.Hour
	maxAuthSecret         = 64 << 10
	maxConnectionAddress  = 512
)

var toolNameRE = regexp.MustCompile(mcpcontract.ToolNamePattern)

var (
	ErrClosed              = errors.New("MCP federation is closed")
	ErrRequiredUnavailable = errors.New("required MCP connection is unavailable")
	ErrOptionalUnavailable = errors.New("optional MCP connection is unavailable")
	ErrToolNotFound        = errors.New("federated MCP tool is unavailable")
	ErrToolCollision       = errors.New("federated MCP tool name collides")
	ErrInvalidConfig       = errors.New("invalid MCP federation configuration")
	ErrProtocolVersion     = errors.New("remote MCP protocol version is unsupported")
	ErrInputTooLarge       = errors.New("MCP tool input exceeds the configured limit")
	ErrResultTooLarge      = errors.New("MCP tool result exceeds the configured limit")
	ErrResponseTooLarge    = errors.New("remote MCP response exceeds the configured limit")
	ErrRemoteUnavailable   = errors.New("remote MCP connection is unavailable")
	ErrRemoteCallFailed    = errors.New("remote MCP tool call failed")
	ErrRefreshInProgress   = errors.New("MCP federation refresh is already in progress")
)

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
// URL and Auth are intentionally consumed only by this package's transport.
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

// Config configures a federation instance.  LocalToolNames is the set of
// generated/local capability names; remote names colliding with it are
// rejected before the snapshot is committed.
type Config struct {
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
	OnDiagnostic func(Diagnostic)
}

// Diagnostic is a safe, provider-neutral developer diagnostic.  It contains
// no URL, auth header, secret, or remote error text.
type Diagnostic struct {
	Address string
	Code    string
	Message string
	At      time.Time
}

// Tool is a remote MCP tool under its deterministic Scenery namespace.
// Description and schemas originate at an untrusted server and are copied
// only as descriptive metadata.  Approval, effect, and limits always come
// from the configured local policy.
type Tool struct {
	Name              string
	RemoteName        string
	ConnectionAddress string
	Namespace         string
	Title             string
	Description       string
	InputSchema       json.RawMessage
	OutputSchema      json.RawMessage
	Approval          mcpcontract.Approval
	Effect            mcpcontract.Effect
	Limits            mcpcontract.Limits
}

// Capability converts the remote tool into Scenery's provider-neutral
// capability contract.  Operation and execution addresses are deterministic
// remote identities and do not imply local implementation code.
func (t Tool) Capability() mcpcontract.Capability {
	return mcpcontract.Capability{
		ID:               t.ConnectionAddress + "/tool/" + t.RemoteName,
		Name:             t.Name,
		Title:            t.Title,
		Description:      t.Description,
		InputSchema:      cloneJSON(t.InputSchema),
		OutputSchema:     cloneJSON(t.OutputSchema),
		OperationAddress: "mcp/federated/operation/" + t.Name,
		ExecutionAddress: "mcp/federated/execution/" + t.Name,
		Origin: mcpcontract.Origin{
			Kind:      "federated",
			Address:   t.ConnectionAddress,
			Namespace: t.Namespace,
		},
		Auth:     mcpcontract.Auth{Authentication: "std.authentication.inherit", Authorization: "std.authorization.local"},
		Limits:   t.Limits,
		Effect:   t.Effect,
		Approval: t.Approval,
	}
}

// Snapshot is an immutable copy of the currently discoverable federated
// tools. Optional unhealthy connections contribute no tools.
type Snapshot struct {
	Tools               []Tool
	Ready               bool
	RequiredUnavailable []string
	RefreshedAt         time.Time
}

// Federation is a concurrent-safe set of remote MCP connections.
type Federation struct {
	cfg Config

	mu           sync.RWMutex
	connections  []*remoteConnection
	tools        map[string]boundTool
	snapshot     Snapshot
	closed       bool
	refreshing   bool
	startOnce    sync.Once
	started      atomic.Bool
	stop         chan struct{}
	wake         chan struct{}
	stopOnce     sync.Once
	workerDone   chan struct{}
	workerCancel context.CancelFunc
}

type remoteConnection struct {
	cfg  Connection
	stop chan struct{}

	mu             sync.RWMutex
	client         *mcp.Client
	session        *mcp.ClientSession
	tools          []Tool
	ready          bool
	lastRefresh    time.Time
	lastDiagnostic time.Time
	closed         bool
}

type boundTool struct {
	connection *remoteConnection
	tool       Tool
}

// New validates static connection configuration and returns an unconnected
// federation. Call Refresh before serving tools and Start to enable change/
// TTL refreshes.
func New(cfg Config) (*Federation, error) {
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	// Detach caller-owned slices before the federation starts its background
	// refresh worker. Connections are copied again below so their auth and
	// filter material cannot be mutated through the original config.
	cfg.LocalToolNames = append([]string(nil), cfg.LocalToolNames...)
	cfg.Connections = append([]Connection(nil), cfg.Connections...)
	connectionConfigs := cfg.Connections
	// Federation.cfg carries only refresh-wide settings after construction;
	// never retain the deployment-time URLs or credential slices there.
	cfg.Connections = nil
	f := &Federation{
		cfg:        cfg,
		tools:      make(map[string]boundTool),
		stop:       make(chan struct{}),
		wake:       make(chan struct{}, 1),
		workerDone: make(chan struct{}),
	}
	for _, c := range connectionConfigs {
		c.Auth.Secret = append([]byte(nil), c.Auth.Secret...)
		c.Allow = append([]string(nil), c.Allow...)
		c.Block = append([]string(nil), c.Block...)
		f.connections = append(f.connections, &remoteConnection{cfg: c, stop: make(chan struct{})})
	}
	return f, nil
}

// Start enables bounded TTL refresh and SDK tool-list change notifications.
// Calling Start more than once is safe. The caller owns ctx cancellation.
func (f *Federation) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := f.ensureOpen(); err != nil {
		return err
	}
	f.startOnce.Do(func() {
		loopCtx, cancel := context.WithCancel(ctx)
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			cancel()
			return
		}
		f.workerCancel = cancel
		f.mu.Unlock()
		f.started.Store(true)
		go f.refreshLoop(loopCtx)
	})
	return nil
}

func (f *Federation) refreshLoop(ctx context.Context) {
	defer close(f.workerDone)
	ticker := time.NewTicker(f.cfg.RefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.stop:
			return
		case <-f.wake:
			_ = f.Refresh(ctx)
		case <-ticker.C:
			if f.needsRefresh() {
				_ = f.Refresh(ctx)
			}
		}
	}
}

func (f *Federation) needsRefresh() bool {
	now := time.Now()
	retryEvery := f.cfg.RefreshEvery
	for _, c := range f.connections {
		c.mu.RLock()
		interval := c.refreshTTL()
		if !c.ready && retryEvery > 0 && retryEvery < interval {
			interval = retryEvery
		}
		stale := c.lastRefresh.IsZero() || now.Sub(c.lastRefresh) >= interval
		c.mu.RUnlock()
		if stale {
			return true
		}
	}
	return false
}

// Refresh connects/reconnects remotes and atomically publishes a deterministic
// snapshot. A required outage returns ErrRequiredUnavailable and leaves the
// published snapshot with no tools from that connection.
func (f *Federation) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := f.ensureOpen(); err != nil {
		return err
	}
	f.mu.Lock()
	if f.refreshing {
		f.mu.Unlock()
		return ErrRefreshInProgress
	}
	f.refreshing = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.refreshing = false
		f.mu.Unlock()
	}()

	var requiredUnavailable []string
	all := make([]Tool, 0)
	for _, c := range f.connections {
		err := c.refresh(ctx, f.cfg.MaxHTTPResponse, f.cfg.MaxTools, f.wake)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if c.cfg.Required {
				requiredUnavailable = append(requiredUnavailable, c.cfg.Address)
			} else {
				f.emitOptionalDiagnostic(c, err)
			}
			continue
		}
		c.mu.RLock()
		all = append(all, c.tools...)
		c.mu.RUnlock()
	}

	sort.Strings(requiredUnavailable)
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	bound := make(map[string]boundTool, len(all))
	for _, name := range f.cfg.LocalToolNames {
		bound[name] = boundTool{}
	}
	for _, tool := range all {
		if _, exists := bound[tool.Name]; exists {
			return ErrToolCollision
		}
		bound[tool.Name] = boundTool{connection: f.connectionFor(tool.ConnectionAddress), tool: tool}
	}

	snapshot := Snapshot{Tools: cloneTools(all), Ready: len(requiredUnavailable) == 0, RequiredUnavailable: requiredUnavailable, RefreshedAt: time.Now().UTC()}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return ErrClosed
	}
	f.tools = bound
	f.snapshot = snapshot
	f.mu.Unlock()
	if len(requiredUnavailable) > 0 {
		return ErrRequiredUnavailable
	}
	return nil
}

func (f *Federation) emitOptionalDiagnostic(c *remoteConnection, _ error) {
	now := time.Now().UTC()
	c.mu.Lock()
	if !c.lastDiagnostic.IsZero() && now.Sub(c.lastDiagnostic) < f.cfg.DiagnosticTTL {
		c.mu.Unlock()
		return
	}
	c.lastDiagnostic = now
	c.mu.Unlock()
	if f.cfg.OnDiagnostic != nil {
		f.cfg.OnDiagnostic(Diagnostic{Address: c.cfg.Address, Code: "MCP_CONNECTION_UNAVAILABLE", Message: "optional MCP connection unavailable", At: now})
	}
}

func (f *Federation) connectionFor(address string) *remoteConnection {
	for _, c := range f.connections {
		if c.cfg.Address == address {
			return c
		}
	}
	return nil
}

// Snapshot returns an immutable copy of the current inventory.
func (f *Federation) Snapshot() Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := f.snapshot
	s.Tools = cloneTools(s.Tools)
	s.RequiredUnavailable = append([]string(nil), s.RequiredUnavailable...)
	return s
}

// Ready reports whether every required connection is currently available.
func (f *Federation) Ready() bool { return f.Snapshot().Ready }

// Tools returns the deterministic currently discoverable remote inventory.
func (f *Federation) Tools() []Tool { return f.Snapshot().Tools }

// Capabilities returns the current inventory projected to Scenery's canonical
// capability contract.
func (f *Federation) Capabilities() []mcpcontract.Capability {
	tools := f.Tools()
	result := make([]mcpcontract.Capability, 0, len(tools))
	for _, tool := range tools {
		result = append(result, tool.Capability())
	}
	return result
}

// Call invokes a federated tool through its authenticated remote session.
func (f *Federation) Call(ctx context.Context, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := f.ensureOpen(); err != nil {
		return nil, err
	}
	if len(arguments) == 0 {
		arguments = []byte(`{}`)
	}
	if !json.Valid(arguments) {
		return nil, ErrRemoteCallFailed
	}
	var object map[string]any
	if err := json.Unmarshal(arguments, &object); err != nil || object == nil {
		return nil, ErrRemoteCallFailed
	}
	f.mu.RLock()
	b, ok := f.tools[name]
	f.mu.RUnlock()
	if !ok || b.connection == nil {
		return nil, ErrToolNotFound
	}
	if len(arguments) > b.tool.Limits.MaxInputBytes {
		return nil, ErrInputTooLarge
	}
	return b.connection.call(ctx, b.tool, object)
}

// CallTool implements mcpcontract.ToolDispatcher. The remote result is kept
// opaque inside the neutral value envelope; remote errors do not become Go
// errors unless the transport itself failed.
func (f *Federation) CallTool(ctx context.Context, _ mcpcontract.ToolCallContext, name string, arguments json.RawMessage) (mcpcontract.ToolOutcome, error) {
	result, err := f.Call(ctx, name, arguments)
	if err != nil {
		return mcpcontract.ToolOutcome{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return mcpcontract.ToolOutcome{}, ErrRemoteCallFailed
	}
	f.mu.RLock()
	b, ok := f.tools[name]
	f.mu.RUnlock()
	if !ok || b.connection == nil {
		return mcpcontract.ToolOutcome{}, ErrToolNotFound
	}
	if len(encoded) > b.tool.Limits.MaxResultBytes {
		return mcpcontract.ToolOutcome{}, ErrResultTooLarge
	}
	if result.IsError {
		return mcpcontract.ToolOutcome{Outcome: "failed", Problem: encoded}, nil
	}
	return mcpcontract.ToolOutcome{Outcome: "processed", Value: encoded}, nil
}

// Close closes all remote sessions and stops background refresh. It is safe to
// call more than once.
func (f *Federation) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	workerCancel := f.workerCancel
	f.mu.Unlock()
	if workerCancel != nil {
		workerCancel()
	}
	f.stopOnce.Do(func() { close(f.stop) })
	for _, c := range f.connections {
		c.close()
	}
	if f.started.Load() {
		select {
		case <-f.workerDone:
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func (f *Federation) ensureOpen() error {
	f.mu.RLock()
	closed := f.closed
	f.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	return nil
}

func (c *remoteConnection) refresh(ctx context.Context, maxResponse int64, maxTools int, wake chan<- struct{}) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	config := c.cfg
	config.Auth.Secret = append([]byte(nil), c.cfg.Auth.Secret...)
	session := c.session
	c.mu.Unlock()
	if session == nil {
		var client *mcp.Client
		var err error
		client, session, err = connect(ctx, config, maxResponse, wake, c.stop)
		if err != nil {
			c.mu.Lock()
			c.ready = false
			c.tools = nil
			c.lastRefresh = time.Now()
			c.mu.Unlock()
			if errors.Is(err, ErrProtocolVersion) {
				return ErrProtocolVersion
			}
			if errors.Is(err, ErrResponseTooLarge) {
				return ErrResponseTooLarge
			}
			return ErrRemoteUnavailable
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = session.Close()
			return ErrClosed
		}
		c.client, c.session = client, session
		c.mu.Unlock()
	}
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	var remoteTools []*mcp.Tool
	var listErr error
	// The SDK's Tools iterator follows cursors, but it does not detect a
	// malicious server that repeats a cursor forever. Walk pages explicitly so
	// a bounded inventory cannot be turned into an unbounded refresh loop.
	seenCursors := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		if seenCursors[cursor] {
			listErr = ErrResponseTooLarge
			break
		}
		seenCursors[cursor] = true
		pages++
		if pages > maxTools {
			listErr = ErrResponseTooLarge
			break
		}
		result, err := session.ListTools(callCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			listErr = err
			break
		}
		if result == nil {
			listErr = ErrRemoteUnavailable
			break
		}
		for _, tool := range result.Tools {
			if len(remoteTools) >= maxTools {
				listErr = ErrResponseTooLarge
				break
			}
			remoteTools = append(remoteTools, tool)
		}
		if listErr != nil || result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	cancel()
	if listErr != nil {
		c.markUnavailable()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(listErr, ErrResponseTooLarge) {
			return ErrResponseTooLarge
		}
		return ErrRemoteUnavailable
	}
	tools, err := c.projectTools(remoteTools)
	if err != nil {
		c.mu.Lock()
		c.ready = false
		c.tools = nil
		c.lastRefresh = time.Now()
		c.mu.Unlock()
		return err
	}
	c.mu.Lock()
	c.tools = tools
	c.ready = true
	c.lastRefresh = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *remoteConnection) projectTools(remote []*mcp.Tool) ([]Tool, error) {
	seen := make(map[string]bool, len(remote))
	result := make([]Tool, 0, len(remote))
	for _, raw := range remote {
		if raw == nil || !toolNameRE.MatchString(raw.Name) {
			return nil, ErrInvalidConfig
		}
		if !allowed(raw.Name, c.cfg.Allow, c.cfg.Block) {
			continue
		}
		name := c.cfg.Namespace + "__" + raw.Name
		if !toolNameRE.MatchString(name) {
			return nil, ErrToolCollision
		}
		if seen[name] {
			return nil, ErrToolCollision
		}
		seen[name] = true
		input, err := marshalSchema(raw.InputSchema, true)
		if err != nil {
			return nil, ErrInvalidConfig
		}
		output, err := marshalSchema(raw.OutputSchema, false)
		if err != nil {
			return nil, ErrInvalidConfig
		}
		policy := normalizePolicy(c.cfg.Policy)
		title := boundedMetadata(raw.Title, maxToolTitle)
		description := boundedMetadata(raw.Description, maxToolDescription)
		result = append(result, Tool{
			Name: name, RemoteName: raw.Name, ConnectionAddress: c.cfg.Address,
			Namespace: c.cfg.Namespace, Title: title, Description: description,
			InputSchema: input, OutputSchema: output,
			Approval: policy.Approval, Effect: policy.Effect,
			Limits: mcpcontract.Limits{MaxInputBytes: policy.MaxInputBytes, MaxResultBytes: policy.MaxResultBytes},
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *remoteConnection) call(ctx context.Context, tool Tool, arguments map[string]any) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	session := c.session
	closed := c.closed
	c.mu.RUnlock()
	if closed || session == nil {
		return nil, ErrRemoteUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()
	result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: tool.RemoteName, Arguments: arguments})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		c.markUnavailable()
		if errors.Is(err, ErrResponseTooLarge) {
			return nil, ErrResponseTooLarge
		}
		return nil, ErrRemoteCallFailed
	}
	if result == nil {
		return nil, ErrRemoteCallFailed
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > tool.Limits.MaxResultBytes {
		return nil, ErrResultTooLarge
	}
	return result, nil
}

func (c *remoteConnection) markUnavailable() {
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.client = nil
	c.ready = false
	c.tools = nil
	c.lastRefresh = time.Now()
	c.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

func (c *remoteConnection) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.stop != nil {
		close(c.stop)
	}
	session := c.session
	c.session = nil
	c.client = nil
	secret := c.cfg.Auth.Secret
	c.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	clearBytes(secret)
	c.mu.Lock()
	c.cfg.Auth.Secret = nil
	c.mu.Unlock()
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func (c *remoteConnection) refreshTTL() time.Duration {
	if c.cfg.RefreshTTL > 0 {
		return c.cfg.RefreshTTL
	}
	return defaultRefreshTTL
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return ErrInvalidConfig
	}
	if cfg.RefreshEvery <= 0 {
		cfg.RefreshEvery = defaultRefreshEvery
	}
	if cfg.RefreshEvery < 10*time.Millisecond {
		cfg.RefreshEvery = 10 * time.Millisecond
	}
	if cfg.DiagnosticTTL <= 0 {
		cfg.DiagnosticTTL = defaultDiagnosticTTL
	}
	if cfg.DiagnosticTTL > maxDiagnosticTTL {
		return ErrInvalidConfig
	}
	if cfg.MaxHTTPResponse <= 0 {
		cfg.MaxHTTPResponse = maxHTTPResponse
	}
	if cfg.MaxHTTPResponse > 128<<20 {
		return ErrInvalidConfig
	}
	if cfg.MaxTools <= 0 {
		cfg.MaxTools = defaultMaxTools
	}
	if cfg.MaxTools > 65536 {
		return ErrInvalidConfig
	}
	local := make(map[string]bool, len(cfg.LocalToolNames))
	for _, name := range cfg.LocalToolNames {
		if !toolNameRE.MatchString(name) || local[name] {
			return ErrInvalidConfig
		}
		local[name] = true
	}
	seenAddress := map[string]bool{}
	seenNamespace := map[string]bool{}
	for i := range cfg.Connections {
		c := &cfg.Connections[i]
		if !validConnectionAddress(c.Address) || seenAddress[c.Address] || !toolNameRE.MatchString(c.Namespace) || seenNamespace[c.Namespace] {
			return ErrInvalidConfig
		}
		seenAddress[c.Address], seenNamespace[c.Namespace] = true, true
		if err := validateURL(c.URL); err != nil {
			return err
		}
		if len(c.Allow) > 0 && len(c.Block) > 0 {
			return ErrInvalidConfig
		}
		for _, values := range [][]string{c.Allow, c.Block} {
			last := ""
			seen := map[string]bool{}
			for _, name := range values {
				if !toolNameRE.MatchString(name) || seen[name] || (last != "" && name < last) {
					return ErrInvalidConfig
				}
				seen[name] = true
				last = name
			}
		}
		if err := validateAuth(c.Auth); err != nil {
			return err
		}
		if c.ConnectTimeout <= 0 {
			c.ConnectTimeout = defaultConnectTimeout
		}
		if c.CallTimeout <= 0 {
			c.CallTimeout = defaultCallTimeout
		}
		if c.ConnectTimeout > 10*time.Minute || c.CallTimeout > 10*time.Minute || c.RefreshTTL < 0 || c.RefreshTTL > maxRefreshTTL {
			return ErrInvalidConfig
		}
		if c.Policy.Approval != "" && c.Policy.Approval != mcpcontract.ApprovalNever && c.Policy.Approval != mcpcontract.ApprovalAlways {
			return ErrInvalidConfig
		}
		if c.Policy.Effect.ReadOnly && c.Policy.Effect.Destructive {
			return ErrInvalidConfig
		}
		if c.Policy.MaxInputBytes < 0 || c.Policy.MaxResultBytes < 0 || c.Policy.MaxInputBytes > defaultMaxInputBytes || c.Policy.MaxResultBytes > defaultMaxResultBytes {
			return ErrInvalidConfig
		}
		c.Policy = normalizePolicy(c.Policy)
	}
	return nil
}

func normalizePolicy(policy ToolPolicy) ToolPolicy {
	if policy.Approval == "" {
		// External metadata cannot establish trust. When the source has not
		// supplied a local remote policy, expose the capability only as an
		// approval-gated, open-world, potentially destructive tool.
		policy.Approval = mcpcontract.ApprovalAlways
	}
	if policy.Approval != mcpcontract.ApprovalNever && policy.Approval != mcpcontract.ApprovalAlways {
		policy.Approval = mcpcontract.ApprovalAlways
	}
	if policy.MaxInputBytes <= 0 || policy.MaxInputBytes > defaultMaxInputBytes {
		policy.MaxInputBytes = defaultMaxInputBytes
	}
	if policy.MaxResultBytes <= 0 || policy.MaxResultBytes > defaultMaxResultBytes {
		policy.MaxResultBytes = defaultMaxResultBytes
	}
	if policy.Effect.ReadOnly && policy.Effect.Destructive {
		policy.Effect.Destructive = false
	}
	if !policy.Effect.ReadOnly && !policy.Effect.Destructive && !policy.Effect.Idempotent && !policy.Effect.OpenWorld {
		policy.Effect.Destructive = true
		policy.Effect.OpenWorld = true
	}
	return policy
}

func validateAuth(auth Auth) error {
	scheme := auth.Scheme
	if scheme == "" {
		scheme = AuthNone
	}
	switch scheme {
	case AuthNone:
		if len(auth.Secret) != 0 || auth.Header != "" {
			return ErrInvalidConfig
		}
	case AuthBearer:
		if len(auth.Secret) == 0 || len(auth.Secret) > maxAuthSecret || auth.Header != "" {
			return ErrInvalidConfig
		}
	case AuthHeader:
		if len(auth.Secret) == 0 || len(auth.Secret) > maxAuthSecret || !validHeaderName(auth.Header) {
			return ErrInvalidConfig
		}
	default:
		return ErrInvalidConfig
	}
	return nil
}

func validateURL(raw string) error {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return ErrInvalidConfig
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return ErrInvalidConfig
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return ErrInvalidConfig
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > 256 || strings.TrimSpace(name) != name {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isHTTPTokenByte(name[i]) {
			return false
		}
	}
	// These fields are owned by the MCP transport and cannot safely be
	// replaced by a one-header credential.
	switch strings.ToLower(name) {
	case "accept", "content-type", "content-length", "host", "cookie", "transfer-encoding", "mcp-protocol-version", "mcp-session-id", "last-event-id", "user-agent":
		return false
	default:
		return true
	}
}

func isHTTPTokenByte(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value))
}

func validConnectionAddress(address string) bool {
	if address == "" || len(address) > maxConnectionAddress || strings.TrimSpace(address) != address || !utf8.ValidString(address) {
		return false
	}
	return !strings.ContainsFunc(address, unicode.IsSpace) && !strings.ContainsFunc(address, unicode.IsControl)
}

func boundedMetadata(value string, maximum int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maximum {
		return value
	}
	cut := maximum
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func allowed(name string, allow, block []string) bool {
	if len(allow) > 0 {
		return sort.SearchStrings(allow, name) < len(allow) && allow[sort.SearchStrings(allow, name)] == name
	}
	return sort.SearchStrings(block, name) >= len(block) || block[sort.SearchStrings(block, name)] != name
}

func marshalSchema(value any, input bool) (json.RawMessage, error) {
	if value == nil {
		if input {
			return json.RawMessage(`{"type":"object"}`), nil
		}
		return json.RawMessage(`{"type":"object"}`), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maxToolSchema || !json.Valid(encoded) {
		return nil, ErrInvalidConfig
	}
	var object map[string]any
	if json.Unmarshal(encoded, &object) != nil || object == nil || (input && object["type"] != "object") {
		return nil, ErrInvalidConfig
	}
	return append(json.RawMessage(nil), encoded...), nil
}

func cloneJSON(value json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), value...) }

func cloneTools(tools []Tool) []Tool {
	copyTools := make([]Tool, len(tools))
	copy(copyTools, tools)
	for i := range copyTools {
		copyTools[i].InputSchema = cloneJSON(copyTools[i].InputSchema)
		copyTools[i].OutputSchema = cloneJSON(copyTools[i].OutputSchema)
	}
	return copyTools
}

// compile-time checks keep the package on the provider-neutral contract.
var _ mcpcontract.ToolDispatcher = (*Federation)(nil)
