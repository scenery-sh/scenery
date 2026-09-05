package assistantruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scenery.sh/internal/assistantcontrol"
)

const (
	// ControlTokenHeader is the only credential sent to a private helper. It
	// is deliberately not an Authorization header so application credentials
	// cannot be confused with the helper's control identity.
	ControlTokenHeader = "X-Scenery-Assistant-Control-Token"

	controlPath = "/scenery/v1/control"

	defaultControlTimeout = 15 * time.Second
	defaultStreamTimeout  = 5 * time.Minute
	defaultRequestBytes   = int64(16 << 20)
	defaultResponseBytes  = int64(16 << 20)
	defaultEventBytes     = int64(1 << 20)
	maxControlTimeout     = 5 * time.Minute
	maxStreamTimeout      = time.Hour
	maxRequestBytes       = int64(64 << 20)
	maxResponseBytes      = int64(64 << 20)
	maxEventBytes         = int64(16 << 20)
)

var (
	// ErrInvalidControlAddress means that the helper endpoint was not an
	// explicit loopback HTTP address.
	ErrInvalidControlAddress = errors.New("assistant helper control address is invalid")
	// ErrInvalidClientConfig means a helper client limit, timeout, or identity
	// setting is outside the supported bounded range.
	ErrInvalidClientConfig = errors.New("assistant helper client configuration is invalid")
	// ErrRequestTooLarge means the private control request exceeded its bound.
	ErrRequestTooLarge = errors.New("assistant helper control request is too large")
	// ErrResponseTooLarge means a private control response or event stream
	// exceeded its bound.
	ErrResponseTooLarge = errors.New("assistant helper control response is too large")
	// ErrMalformedResponse means a helper returned a response that is not the
	// current assistantcontrol vocabulary.
	ErrMalformedResponse = errors.New("malformed private assistant response")
	// ErrRedirectRejected prevents a control token from following a redirect.
	ErrRedirectRejected = errors.New("assistant helper redirect rejected")
	// ErrHelperRequest is a neutral error for a valid helper error response.
	// The provider's error text is never retained in this error's message.
	ErrHelperRequest = errors.New("assistant helper request failed")
)

// HTTPClientConfig configures the private helper control client. ControlBase
// must be a loopback HTTP origin with an explicit port and no path, query,
// fragment, or user info.
// All limits and timeouts are finite; zero values use conservative defaults.
type HTTPClientConfig struct {
	ControlBase        string
	ControlToken       string
	AssistantAddress   string
	RuntimeRevision    string
	CapabilityRevision string

	ControlTimeout   time.Duration
	StreamTimeout    time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
	MaxEventBytes    int64
}

// HTTPClient implements Client over the provider-neutral helper HTTP
// protocol. It owns no provider vocabulary and sends requests only to the
// loopback control origin supplied at construction time.
type HTTPClient struct {
	baseURL            *url.URL
	controlToken       string
	assistantAddress   string
	runtimeRevision    string
	capabilityRevision string
	controlTimeout     time.Duration
	streamTimeout      time.Duration
	maxRequestBytes    int64
	maxResponseBytes   int64
	maxEventBytes      int64
	httpClient         *http.Client
	lifecycleCtx       context.Context
	lifecycleCancel    context.CancelFunc

	mu      sync.Mutex
	closed  bool
	streams map[*eventStream]struct{}
}

var _ Client = (*HTTPClient)(nil)

// NewHTTPClient validates the helper address and creates a transport with
// proxy lookup disabled and redirects rejected. The returned client is ready
// for Probe; no network request is made by this constructor.
func NewHTTPClient(config HTTPClientConfig) (*HTTPClient, error) {
	base, err := normalizeControlBase(config.ControlBase)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.ControlToken) == "" {
		return nil, fmt.Errorf("%w: control token is required", ErrInvalidClientConfig)
	}
	if strings.TrimSpace(config.AssistantAddress) == "" || strings.TrimSpace(config.RuntimeRevision) == "" || strings.TrimSpace(config.CapabilityRevision) == "" {
		return nil, fmt.Errorf("%w: expected assistant and revisions are required", ErrInvalidClientConfig)
	}
	if config.ControlTimeout < 0 || config.ControlTimeout > maxControlTimeout {
		return nil, ErrInvalidClientConfig
	}
	if config.StreamTimeout < 0 || config.StreamTimeout > maxStreamTimeout {
		return nil, ErrInvalidClientConfig
	}
	if config.MaxRequestBytes < 0 || config.MaxRequestBytes > maxRequestBytes {
		return nil, ErrInvalidClientConfig
	}
	if config.MaxResponseBytes < 0 || config.MaxResponseBytes > maxResponseBytes {
		return nil, ErrInvalidClientConfig
	}
	if config.MaxEventBytes < 0 || config.MaxEventBytes > maxEventBytes {
		return nil, ErrInvalidClientConfig
	}
	if config.ControlTimeout == 0 {
		config.ControlTimeout = defaultControlTimeout
	}
	if config.StreamTimeout == 0 {
		config.StreamTimeout = defaultStreamTimeout
	}
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = defaultRequestBytes
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = defaultResponseBytes
	}
	if config.MaxEventBytes == 0 {
		config.MaxEventBytes = defaultEventBytes
	}
	if config.MaxEventBytes > config.MaxResponseBytes {
		return nil, ErrInvalidClientConfig
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return ErrRedirectRejected
		},
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	return &HTTPClient{
		baseURL:            base,
		controlToken:       config.ControlToken,
		assistantAddress:   config.AssistantAddress,
		runtimeRevision:    config.RuntimeRevision,
		capabilityRevision: config.CapabilityRevision,
		controlTimeout:     config.ControlTimeout,
		streamTimeout:      config.StreamTimeout,
		maxRequestBytes:    config.MaxRequestBytes,
		maxResponseBytes:   config.MaxResponseBytes,
		maxEventBytes:      config.MaxEventBytes,
		httpClient:         client,
		lifecycleCtx:       lifecycleCtx,
		lifecycleCancel:    lifecycleCancel,
		streams:            make(map[*eventStream]struct{}),
	}, nil
}

func normalizeControlBase(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidControlAddress
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, ErrInvalidControlAddress
	}
	if parsed.Port() == "" {
		return nil, ErrInvalidControlAddress
	}
	if !loopbackHost(parsed.Hostname()) {
		return nil, ErrInvalidControlAddress
	}
	// A malformed port is rejected by URL.Port's parser in current Go, but
	// checking the host explicitly keeps this invariant stable across versions.
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, ErrInvalidControlAddress
		}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func loopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *HTTPClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *HTTPClient) withTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	merged, mergeCancel := context.WithCancel(c.lifecycleCtx)
	stopParent := context.AfterFunc(parent, mergeCancel)
	timed, timeoutCancel := context.WithTimeout(merged, timeout)
	return timed, func() {
		timeoutCancel()
		mergeCancel()
		stopParent()
	}
}

// Close cancels every active event stream and prevents new requests. It is
// safe to call more than once.
func (c *HTTPClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	streams := make([]*eventStream, 0, len(c.streams))
	for stream := range c.streams {
		streams = append(streams, stream)
	}
	c.mu.Unlock()
	c.lifecycleCancel()
	for _, stream := range streams {
		_ = stream.Close()
	}
	if transport, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

// Probe verifies readiness and the exact helper descriptor before an
// assistant is advertised as available. Both health and info are checked.
func (c *HTTPClient) Probe(ctx context.Context) error {
	health, err := c.Health(ctx)
	if err != nil {
		return err
	}
	if !health.Ready {
		return ErrUnavailable
	}
	if _, err := c.Info(ctx); err != nil {
		return err
	}
	return nil
}

func (c *HTTPClient) Health(ctx context.Context) (Health, error) {
	request := c.probeRequest(assistantcontrol.RequestHealth)
	response, err := c.doControl(ctx, request)
	if err != nil {
		return Health{}, err
	}
	if response.Type != assistantcontrol.ResponseHealth || response.Health == nil {
		return Health{}, ErrMalformedResponse
	}
	if err := response.Health.Validate(); err != nil {
		return Health{}, ErrMalformedResponse
	}
	if err := c.checkRevisions(response.RuntimeRevision, response.CapabilityRevision); err != nil {
		return Health{}, err
	}
	if err := c.checkRevisions(response.Health.RuntimeRevision, response.Health.CapabilityRevision); err != nil {
		return Health{}, err
	}
	return Health{Ready: response.Health.Ready, RuntimeRevision: response.Health.RuntimeRevision, CapabilityRevision: response.Health.CapabilityRevision, Status: response.Health.Status, Detail: response.Health.Detail}, nil
}

func (c *HTTPClient) Info(ctx context.Context) (Info, error) {
	request := c.probeRequest(assistantcontrol.RequestInfo)
	response, err := c.doControl(ctx, request)
	if err != nil {
		return Info{}, err
	}
	if response.Type != assistantcontrol.ResponseInfo || response.Descriptor == nil {
		return Info{}, ErrMalformedResponse
	}
	descriptor := response.Descriptor
	if err := descriptor.Validate(); err != nil {
		return Info{}, ErrMalformedResponse
	}
	if err := c.checkRevisions(response.RuntimeRevision, response.CapabilityRevision); err != nil {
		return Info{}, err
	}
	if descriptor.AssistantAddress != c.assistantAddress {
		return Info{}, revisionMismatch("assistant_address", c.assistantAddress, descriptor.AssistantAddress)
	}
	if descriptor.RuntimeRevision != c.runtimeRevision {
		return Info{}, revisionMismatch("runtime_revision", c.runtimeRevision, descriptor.RuntimeRevision)
	}
	if descriptor.CapabilityRevision != c.capabilityRevision {
		return Info{}, revisionMismatch("capability_revision", c.capabilityRevision, descriptor.CapabilityRevision)
	}
	if descriptor.ControlProtocol != assistantcontrol.ControlProtocol || descriptor.MCPProtocol != assistantcontrol.MCPProtocolVersion {
		return Info{}, ErrRevisionMismatch
	}
	return Info{Kind: descriptor.Kind, SchemaRevision: descriptor.SchemaRevision, AssistantAddress: descriptor.AssistantAddress, RuntimeRevision: descriptor.RuntimeRevision, CapabilityRevision: descriptor.CapabilityRevision, ControlProtocol: descriptor.ControlProtocol, MCPProtocol: descriptor.MCPProtocol}, nil
}

func (c *HTTPClient) StartConversation(ctx context.Context, request StartRequest) (StartResult, error) {
	control := request.controlBase(assistantcontrol.RequestCreateConversation)
	control.RunID = request.RunID
	control.Message = request.Message
	control.Data = append([]byte(nil), request.Data...)
	response, err := c.doControl(ctx, control)
	if err != nil {
		return StartResult{}, err
	}
	if response.Type != assistantcontrol.ResponseConversationCreated || response.PrivateSessionID == "" || response.ContinuationToken == "" || response.RunID == "" {
		return StartResult{}, ErrMalformedResponse
	}
	conversationID := ""
	if len(response.Data) != 0 {
		var data struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := decodeData(response.Data, &data); err != nil {
			return StartResult{}, ErrMalformedResponse
		}
		conversationID = data.ConversationID
	}
	return StartResult{ConversationID: conversationID, PrivateSessionID: response.PrivateSessionID, ContinuationToken: response.ContinuationToken, RunID: response.RunID}, nil
}

func (c *HTTPClient) SendTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	control := request.controlBase(assistantcontrol.RequestSendTurn)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	control.Message = request.Message
	control.Data = append([]byte(nil), request.Data...)
	response, err := c.doControl(ctx, control)
	if err != nil {
		return TurnResult{}, err
	}
	if response.Type != assistantcontrol.ResponseTurnAccepted || response.PrivateSessionID == "" || response.ContinuationToken == "" || response.RunID == "" {
		return TurnResult{}, ErrMalformedResponse
	}
	if response.PrivateSessionID != request.PrivateSessionID {
		return TurnResult{}, ErrMalformedResponse
	}
	return TurnResult{PrivateSessionID: response.PrivateSessionID, ContinuationToken: response.ContinuationToken, RunID: response.RunID}, nil
}

func (c *HTTPClient) ResolveApproval(ctx context.Context, request ApprovalRequest) error {
	control := request.controlBase(assistantcontrol.RequestResolveApproval)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	control.ApprovalID = request.ApprovalID
	control.Decision = request.Decision
	response, err := c.doControl(ctx, control)
	if err != nil {
		return err
	}
	if response.Type != assistantcontrol.ResponseApprovalResolved || response.PrivateSessionID != request.PrivateSessionID || response.RunID != request.RunID || response.ApprovalID != request.ApprovalID || response.Decision != request.Decision {
		return ErrMalformedResponse
	}
	return nil
}

func (c *HTTPClient) CancelRun(ctx context.Context, request CancelRequest) error {
	control := request.controlBase(assistantcontrol.RequestCancelRun)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.RunID = request.RunID
	response, err := c.doControl(ctx, control)
	if err != nil {
		return err
	}
	if response.Type != assistantcontrol.ResponseRunCancelled || response.PrivateSessionID != request.PrivateSessionID || response.RunID != request.RunID {
		return ErrMalformedResponse
	}
	return nil
}

func (c *HTTPClient) StreamEvents(ctx context.Context, request StreamRequest) (io.ReadCloser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.isClosed() {
		return nil, ErrStopped
	}
	// Validate the complete private identity even though GET carries the
	// session and cursor in its URL. This keeps all control inputs on the same
	// assistantcontrol validation path as POST operations.
	control := request.controlBase(assistantcontrol.RequestResumeEvents)
	control.PrivateSessionID = request.PrivateSessionID
	control.ContinuationToken = request.ContinuationToken
	control.After = request.After
	if err := c.validateRequestIdentity(control); err != nil {
		return nil, err
	}
	if _, err := assistantcontrol.MarshalRequest(control); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	streamCtx, cancel := c.withTimeout(ctx, c.streamTimeout)
	endpoint := c.endpoint("/scenery/v1/control/sessions/" + request.PrivateSessionID + "/events")
	endpoint.RawPath = "/scenery/v1/control/sessions/" + url.PathEscape(request.PrivateSessionID) + "/events"
	query := endpoint.Query()
	query.Set("after", strconv.FormatUint(request.After, 10))
	endpoint.RawQuery = query.Encode()
	httpRequest, err := http.NewRequestWithContext(streamCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		cancel()
		return nil, ErrInvalidControlAddress
	}
	httpRequest.Header.Set(ControlTokenHeader, c.controlToken)
	httpRequest.Header.Set("Accept", "application/x-ndjson")
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrRedirectRejected) {
			return nil, ErrRedirectRejected
		}
		return nil, ErrUnavailable
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		cancel()
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return nil, ErrRedirectRejected
		}
		return nil, ErrUnavailable
	}
	if !hasMediaType(response.Header.Get("Content-Type"), "application/x-ndjson") {
		_ = response.Body.Close()
		cancel()
		return nil, ErrMalformedResponse
	}
	stream := &eventStream{
		body:                 response.Body,
		reader:               bufio.NewReaderSize(response.Body, int(minInt64(c.maxEventBytes, 64<<10))),
		ctx:                  streamCtx,
		cancel:               cancel,
		after:                request.After,
		maxResponseBytes:     c.maxResponseBytes,
		maxEventBytes:        c.maxEventBytes,
		expectedAssistant:    c.assistantAddress,
		expectedRuntime:      c.runtimeRevision,
		expectedCapability:   c.capabilityRevision,
		expectedSession:      request.PrivateSessionID,
		expectedContinuation: request.ContinuationToken,
	}
	stream.onClose = func() {
		c.mu.Lock()
		delete(c.streams, stream)
		c.mu.Unlock()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = stream.Close()
		return nil, ErrStopped
	}
	c.streams[stream] = struct{}{}
	c.mu.Unlock()
	return stream, nil
}

func (c *HTTPClient) probeRequest(typ string) assistantcontrol.Request {
	return assistantcontrol.Request{
		Kind:               assistantcontrol.RequestKind,
		SchemaRevision:     assistantcontrol.RequestSchemaRevision,
		Type:               typ,
		RequestID:          nextControlRequestID(typ),
		AssistantAddress:   c.assistantAddress,
		RuntimeRevision:    c.runtimeRevision,
		CapabilityRevision: c.capabilityRevision,
	}
}

func (c *HTTPClient) doControl(ctx context.Context, request assistantcontrol.Request) (assistantcontrol.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.isClosed() {
		return assistantcontrol.Response{}, ErrStopped
	}
	if err := c.validateRequestIdentity(request); err != nil {
		return assistantcontrol.Response{}, err
	}
	body, err := assistantcontrol.MarshalRequest(request)
	if err != nil {
		return assistantcontrol.Response{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if int64(len(body)) > c.maxRequestBytes {
		return assistantcontrol.Response{}, ErrRequestTooLarge
	}
	requestCtx, cancel := c.withTimeout(ctx, c.controlTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.endpoint(controlPath).String(), bytes.NewReader(body))
	if err != nil {
		return assistantcontrol.Response{}, ErrInvalidControlAddress
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set(ControlTokenHeader, c.controlToken)
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return assistantcontrol.Response{}, ctx.Err()
		}
		if errors.Is(err, ErrRedirectRejected) {
			return assistantcontrol.Response{}, ErrRedirectRejected
		}
		return assistantcontrol.Response{}, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return assistantcontrol.Response{}, ErrRedirectRejected
	}
	if !hasMediaType(response.Header.Get("Content-Type"), "application/json") {
		return assistantcontrol.Response{}, ErrMalformedResponse
	}
	encoded, err := readBounded(response.Body, c.maxResponseBytes)
	if err != nil {
		if ctx.Err() != nil {
			return assistantcontrol.Response{}, ctx.Err()
		}
		return assistantcontrol.Response{}, err
	}
	parsed, parseErr := assistantcontrol.ParseResponse(encoded)
	if parseErr != nil {
		return assistantcontrol.Response{}, ErrMalformedResponse
	}
	if parsed.RequestID != request.RequestID {
		return assistantcontrol.Response{}, ErrMalformedResponse
	}
	if parsed.AssistantAddress != c.assistantAddress {
		return assistantcontrol.Response{}, revisionMismatch("assistant_address", c.assistantAddress, parsed.AssistantAddress)
	}
	if err := c.checkRevisions(parsed.RuntimeRevision, parsed.CapabilityRevision); err != nil {
		return assistantcontrol.Response{}, err
	}
	if parsed.Type == assistantcontrol.ResponseError || response.StatusCode < 200 || response.StatusCode >= 300 {
		if parsed.Error == nil {
			return assistantcontrol.Response{}, ErrHelperRequest
		}
		return assistantcontrol.Response{}, &ControlError{Code: parsed.Error.Code, Retryable: parsed.Error.Retryable}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return assistantcontrol.Response{}, ErrHelperRequest
	}
	return parsed, nil
}

func (c *HTTPClient) validateRequestIdentity(request assistantcontrol.Request) error {
	if request.AssistantAddress != c.assistantAddress {
		return revisionMismatch("assistant_address", c.assistantAddress, request.AssistantAddress)
	}
	if request.RuntimeRevision != c.runtimeRevision {
		return revisionMismatch("runtime_revision", c.runtimeRevision, request.RuntimeRevision)
	}
	if request.CapabilityRevision != c.capabilityRevision {
		return revisionMismatch("capability_revision", c.capabilityRevision, request.CapabilityRevision)
	}
	return nil
}

func (c *HTTPClient) checkRevisions(runtimeRevision, capabilityRevision string) error {
	if runtimeRevision != c.runtimeRevision {
		return revisionMismatch("runtime_revision", c.runtimeRevision, runtimeRevision)
	}
	if capabilityRevision != c.capabilityRevision {
		return revisionMismatch("capability_revision", c.capabilityRevision, capabilityRevision)
	}
	return nil
}

func revisionMismatch(field, expected, actual string) error {
	return fmt.Errorf("%w: %w", ErrRevisionMismatch, assistantcontrol.RevisionMismatchError{Field: field, Expected: expected, Actual: actual})
}

func decodeData(data []byte, target any) error {
	if len(data) == 0 {
		return nil
	}
	if err := jsonUnmarshalStrict(data, target); err != nil {
		return err
	}
	return nil
}

// jsonUnmarshalStrict is kept local so the client does not accept duplicate
// or unknown data fields merely because the outer assistantcontrol envelope
// was valid.
func jsonUnmarshalStrict(data []byte, target any) error {
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

func (c *HTTPClient) endpoint(path string) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = path
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return &endpoint
}

func nextControlRequestID(kind string) string {
	sequence := controlRequestSequence.Add(1)
	return "scenery-control-" + sanitizeRequestKind(kind) + "-" + strconv.FormatUint(sequence, 10)
}

func sanitizeRequestKind(kind string) string {
	kind = strings.TrimSpace(kind)
	kind = strings.TrimPrefix(kind, "assistant.")
	if kind == "" {
		return "request"
	}
	return strings.NewReplacer(".", "-", "/", "-", " ", "-").Replace(kind)
}

var controlRequestSequence atomic.Uint64

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = defaultResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(data)) > limit {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

func hasMediaType(value, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, expected)
}

type eventStream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	ctx    context.Context
	cancel context.CancelFunc

	after                uint64
	lastSequence         uint64
	bytesRead            int64
	maxResponseBytes     int64
	maxEventBytes        int64
	expectedAssistant    string
	expectedRuntime      string
	expectedCapability   string
	expectedSession      string
	expectedContinuation string

	onClose func()

	mu       sync.Mutex
	closed   bool
	closeErr error
	pending  []byte
	terminal error
}

func (stream *eventStream) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	for {
		stream.mu.Lock()
		if len(stream.pending) > 0 {
			n := copy(target, stream.pending)
			stream.pending = stream.pending[n:]
			stream.mu.Unlock()
			return n, nil
		}
		if stream.closed {
			err := stream.terminal
			if err == nil {
				err = io.EOF
			}
			stream.mu.Unlock()
			return 0, err
		}
		stream.mu.Unlock()
		select {
		case <-stream.ctx.Done():
			return 0, stream.fail(stream.ctx.Err())
		default:
		}

		line, err := stream.readLine()
		if err != nil {
			if stream.ctx.Err() != nil {
				return 0, stream.fail(stream.ctx.Err())
			}
			if errors.Is(err, io.EOF) {
				_ = stream.Close()
				return 0, io.EOF
			}
			return 0, stream.fail(err)
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		event, parseErr := assistantcontrol.ParseEvent(trimmed)
		if parseErr != nil {
			return 0, stream.fail(ErrMalformedEvent)
		}
		if err := stream.validateEvent(event); err != nil {
			return 0, stream.fail(err)
		}
		encoded, marshalErr := assistantcontrol.MarshalEvent(event)
		if marshalErr != nil {
			return 0, stream.fail(ErrMalformedEvent)
		}
		encoded = append(encoded, '\n')
		stream.mu.Lock()
		stream.pending = encoded
		stream.mu.Unlock()
	}
}

func (stream *eventStream) readLine() ([]byte, error) {
	var line []byte
	for {
		fragment, err := stream.reader.ReadSlice('\n')
		line = append(line, fragment...)
		if int64(len(line)) > stream.maxEventBytes {
			return nil, ErrResponseTooLarge
		}
		if err == nil {
			stream.mu.Lock()
			stream.bytesRead += int64(len(line))
			tooLarge := stream.bytesRead > stream.maxResponseBytes
			stream.mu.Unlock()
			if tooLarge {
				return nil, ErrResponseTooLarge
			}
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			stream.mu.Lock()
			stream.bytesRead += int64(len(line))
			tooLarge := stream.bytesRead > stream.maxResponseBytes
			stream.mu.Unlock()
			if tooLarge {
				return nil, ErrResponseTooLarge
			}
			return line, nil
		}
		return nil, err
	}
}

func (stream *eventStream) validateEvent(event assistantcontrol.Event) error {
	if event.AssistantAddress != stream.expectedAssistant {
		return revisionMismatch("assistant_address", stream.expectedAssistant, event.AssistantAddress)
	}
	if event.RuntimeRevision != stream.expectedRuntime {
		return revisionMismatch("runtime_revision", stream.expectedRuntime, event.RuntimeRevision)
	}
	if event.CapabilityRevision != stream.expectedCapability {
		return revisionMismatch("capability_revision", stream.expectedCapability, event.CapabilityRevision)
	}
	if event.PrivateSessionID != stream.expectedSession {
		return ErrMalformedEvent
	}
	if event.ContinuationToken != stream.expectedContinuation {
		return ErrMalformedEvent
	}
	if event.Sequence <= stream.after || (stream.lastSequence != 0 && event.Sequence <= stream.lastSequence) {
		return ErrMalformedEvent
	}
	stream.lastSequence = event.Sequence
	return nil
}

func (stream *eventStream) fail(err error) error {
	stream.mu.Lock()
	if stream.terminal == nil {
		stream.terminal = err
	}
	stream.mu.Unlock()
	_ = stream.Close()
	stream.mu.Lock()
	deferred := stream.terminal
	stream.mu.Unlock()
	return deferred
}

func (stream *eventStream) Close() error {
	stream.mu.Lock()
	if stream.closed {
		err := stream.closeErr
		stream.mu.Unlock()
		return err
	}
	stream.closed = true
	stream.cancel()
	stream.mu.Unlock()
	err := stream.body.Close()
	stream.mu.Lock()
	stream.closeErr = err
	stream.mu.Unlock()
	if stream.onClose != nil {
		stream.onClose()
	}
	return err
}

// ControlError preserves only the helper's neutral machine code and retry
// hint. Error() intentionally remains provider-neutral even if an untrusted
// helper sent a surprising code.
type ControlError struct {
	Code      string
	Retryable bool
}

func (err *ControlError) Error() string {
	return ErrHelperRequest.Error()
}

func (err *ControlError) Unwrap() error { return ErrHelperRequest }

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
