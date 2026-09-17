package runtime

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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeapi"
)

// A process link connects the processes of one development session. The
// supervisor writes a private file naming the session token and the host's
// private dispatch listener. A binding absent from this process's registry is
// sent to the host, which dispatches it to the owning service process of the
// caller's pinned generation with the caller's invocation token and request
// state.
const processLinkBindingPath = "/__scenery/process/v1/bindings/invoke"

// Headers of a dispatched call: the binding address lets the host route without
// decoding the body, and the generation pins the call to the application
// generation its request entered.
const (
	processLinkBindingHeader    = "X-Scenery-Binding"
	processGenerationHeader     = "X-Scenery-Process-Generation"
	processIdentityPIDHeader    = "X-Scenery-Process-ID"
	processIdentityBuildHeader  = "X-Scenery-Build-Input-Digest"
	processIdentityImplHeader   = "X-Scenery-Implementation-Revision"
	processIdentityTargetHeader = "X-Scenery-Go-Target"
	processIdentityContractHdr  = "X-Scenery-Contract-Revision"
)

const processLinkMaxBody = 32 << 20

type processLinkTarget struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type processLinkConfig struct {
	Token    string            `json:"token"`
	Dispatch processLinkTarget `json:"dispatch"`
	// HostState is the session's private directory of process host state that
	// outlives one host incarnation; only the host reads and writes it.
	HostState string `json:"host_state,omitempty"`
}

type processLinkInvocation struct {
	ID            string     `json:"id"`
	Principal     string     `json:"principal,omitempty"`
	TenantID      string     `json:"tenant_id,omitempty"`
	TraceID       string     `json:"trace_id,omitempty"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	CallerBinding string     `json:"caller_binding,omitempty"`
	ExecutionID   string     `json:"execution_id,omitempty"`
	Deployment    string     `json:"deployment,omitempty"`
	Locale        string     `json:"locale,omitempty"`
}

type processLinkEnvelope struct {
	Address       string                `json:"address"`
	CallerPackage string                `json:"caller_package,omitempty"`
	Invocation    processLinkInvocation `json:"invocation"`
	Call          *processLinkCallState `json:"call,omitempty"`
	Input         json.RawMessage       `json:"input"`
}

type processLinkResponse struct {
	Output json.RawMessage   `json:"output,omitempty"`
	Error  *processLinkError `json:"error,omitempty"`
}

type processLinkRuntime struct {
	once    sync.Once
	config  *processLinkConfig
	err     error
	mu      sync.Mutex
	clients map[processLinkTarget]*http.Client
}

var processLinkState = &processLinkRuntime{clients: map[processLinkTarget]*http.Client{}}

func currentProcessLink() (*processLinkConfig, error) {
	processLinkState.once.Do(func() {
		path := strings.TrimSpace(envpolicy.Get("SCENERY_PROCESS_LINK"))
		if path == "" {
			return
		}
		processLinkState.config, processLinkState.err = readProcessLink(path)
	})
	return processLinkState.config, processLinkState.err
}

// requireValidProcessLink fails startup when process wiring was injected but
// cannot be used; a runtime without wiring stays a single-process runtime.
func requireValidProcessLink() error {
	_, err := currentProcessLink()
	return err
}

func readProcessLink(path string) (*processLinkConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("runtime: read process link: %w", err)
	}
	var config processLinkConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("runtime: decode process link: %w", err)
	}
	if len(config.Token) < 32 {
		return nil, fmt.Errorf("runtime: process link token is too short")
	}
	if !validProcessLinkTarget(config.Dispatch) {
		return nil, fmt.Errorf("runtime: process link dispatch target is invalid")
	}
	return &config, nil
}

func validProcessLinkTarget(target processLinkTarget) bool {
	return (target.Network == "unix" || target.Network == "tcp") && strings.TrimSpace(target.Address) != ""
}

// processLinkDialError marks a call that never reached its owner, as opposed
// to a connection lost after the request may have executed.
type processLinkDialError struct{ err error }

func (e *processLinkDialError) Error() string { return e.err.Error() }

func (e *processLinkDialError) Unwrap() error { return e.err }

func processLinkClient(target processLinkTarget) *http.Client {
	processLinkState.mu.Lock()
	defer processLinkState.mu.Unlock()
	if client := processLinkState.clients[target]; client != nil {
		return client
	}
	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, target.Network, target.Address)
			if err != nil {
				return nil, &processLinkDialError{err: err}
			}
			return conn, nil
		},
		DisableCompression:  true,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}}
	processLinkState.clients[target] = client
	return client
}

func invokeProcessLinkedBindingJSON(ctx context.Context, config *processLinkConfig, address, callerPackage string, invocation runtimeapi.Invocation, input []byte) ([]byte, error) {
	call, err := captureProcessLinkedCall(ctx)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("internal binding %s: %w", address, err))
	}
	metadata := processLinkInvocation{
		ID: invocation.ID(), Principal: invocation.Principal(), TenantID: invocation.TenantID(), TraceID: invocation.TraceID(),
		CallerBinding: invocation.CallerBinding(), ExecutionID: invocation.ExecutionID(), Deployment: invocation.Deployment(), Locale: invocation.Locale(),
	}
	if deadline, ok := invocation.Deadline(); ok {
		metadata.Deadline = &deadline
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	body, err := json.Marshal(processLinkEnvelope{Address: address, CallerPackage: callerPackage, Invocation: metadata, Call: call, Input: json.RawMessage(input)})
	if err != nil {
		return nil, fmt.Errorf("invalid_argument: encode process link request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scenery-process"+processLinkBindingPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(processLinkBindingHeader, address)
	if call != nil && call.Generation != 0 {
		request.Header.Set(processGenerationHeader, strconv.FormatUint(call.Generation, 10))
	}
	response, err := processLinkClient(config.Dispatch).Do(request)
	if err != nil {
		return nil, processLinkCallFailure(ctx, address, err)
	}
	defer func() { _ = response.Body.Close() }()
	var decoded processLinkResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, processLinkMaxBody)).Decode(&decoded); err != nil {
		return nil, processLinkCallFailure(ctx, address, fmt.Errorf("decode response (HTTP %d): %w", response.StatusCode, err))
	}
	if decoded.Error != nil {
		return nil, decoded.Error.err()
	}
	if response.StatusCode != http.StatusOK {
		return nil, ContractSystemError(fmt.Errorf("internal binding %s owner returned HTTP %d", address, response.StatusCode))
	}
	return decoded.Output, nil
}

// processLinkCallFailure classifies a call without a decoded owner answer. The
// caller's cancellation or deadline keeps its identity, as it would when an
// in-process handler returns it. Otherwise the owner is unavailable: either the
// request was never delivered, or the connection was lost after delivery and
// the outcome is unknown. Neither case is retried here.
func processLinkCallFailure(ctx context.Context, address string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ContractSystemError(fmt.Errorf("internal binding %s: %w", address, ctxErr))
	}
	delivery := "unknown"
	if _, notSent := errors.AsType[*processLinkDialError](err); notSent {
		delivery = "not_sent"
	}
	return &errs.Error{
		Code: errs.Unavailable, Message: fmt.Sprintf("internal binding %s owner is unavailable", address),
		Meta: errs.Metadata{"delivery": delivery}, Cause: err,
	}
}

func processLinkConfigured() bool {
	config, err := currentProcessLink()
	return err == nil && config != nil
}

func (s *server) registerProcessLinkRoutes() {
	registerRoute(s.public, processLinkBindingPath, []string{http.MethodPost}, s.handleProcessLinkedBinding)
}

func (s *server) handleProcessLinkedBinding(w http.ResponseWriter, req *http.Request, _ routeParams) {
	config, err := currentProcessLink()
	if err != nil || config == nil {
		http.NotFound(w, req)
		return
	}
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(token), []byte(config.Token)) != 1 {
		writeProcessLinkResponse(w, http.StatusUnauthorized, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "permission_denied: process link token rejected"}})
		return
	}
	setProcessIdentityHeaders(w.Header())
	var body processLinkEnvelope
	decoder := json.NewDecoder(io.LimitReader(req.Body, processLinkMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Address) == "" || strings.TrimSpace(body.Invocation.ID) == "" ||
		(req.Header.Get(processLinkBindingHeader) != "" && req.Header.Get(processLinkBindingHeader) != body.Address) {
		writeProcessLinkResponse(w, http.StatusBadRequest, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: malformed process link request"}})
		return
	}
	global.mu.RLock()
	registration := global.contractBindings[body.Address]
	global.mu.RUnlock()
	if registration.Invoke == nil {
		writeProcessLinkResponse(w, http.StatusNotFound, processLinkResponse{Error: &processLinkError{Kind: "error", Message: fmt.Sprintf("contract internal binding %s is not registered in this process", body.Address)}})
		return
	}
	metadata := runtimeapi.InvocationMetadata{
		ID: body.Invocation.ID, Principal: body.Invocation.Principal, TenantID: body.Invocation.TenantID, TraceID: body.Invocation.TraceID,
		CallerBinding: body.Invocation.CallerBinding, ExecutionID: body.Invocation.ExecutionID, Deployment: body.Invocation.Deployment, Locale: body.Invocation.Locale,
	}
	ctx := req.Context()
	if body.Invocation.Deadline != nil {
		metadata.Deadline = *body.Invocation.Deadline
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *body.Invocation.Deadline)
		defer cancel()
	}
	ctx, restore, err := enterProcessLinkedCall(ctx, metadata, body.Call)
	defer restore()
	if err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(ContractSystemError(err))})
		return
	}
	output, err := invokeRegisteredContractBindingJSON(ctx, registration, body.Address, body.CallerPackage, body.Input)
	if err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: output})
}

// setProcessIdentityHeaders reports the linked runtime identity of the
// answering process with the meanings of the development response identity
// headers, so a dispatcher can prove which generation answered.
func setProcessIdentityHeaders(headers http.Header) {
	bundle := CurrentLinkedContractBundle()
	headers.Set(processIdentityContractHdr, bundle.ContractRevision)
	headers.Set(processIdentityImplHeader, bundle.ImplementationRevision)
	headers.Set(processIdentityBuildHeader, bundle.BuildInputDigest)
	headers.Set(processIdentityTargetHeader, bundle.GoTarget)
	headers.Set(processIdentityPIDHeader, strconv.Itoa(os.Getpid()))
}

func writeProcessLinkResponse(w http.ResponseWriter, status int, response processLinkResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

// InvokeContractBindingCodec invokes an internal binding with typed values when
// this process registers it, and otherwise through the process link. The
// callee contract's codecs carry the typed input and outcome across processes.
func InvokeContractBindingCodec(ctx context.Context, address, callerPackage string, invocation, input any, encodeInput func(any) ([]byte, error), decodeOutput func([]byte) (any, error)) (any, error) {
	global.mu.RLock()
	registered := global.contractBindings[address].Invoke != nil
	global.mu.RUnlock()
	if registered {
		return InvokeContractBindingFrom(ctx, address, callerPackage, invocation, input)
	}
	config, err := currentProcessLink()
	if err != nil {
		return nil, ContractSystemError(err)
	}
	if config == nil {
		return nil, fmt.Errorf("contract internal binding %s is not registered", address)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token, ok := invocation.(runtimeapi.Invocation)
	current, currentOK := runtimeapi.InvocationFromContext(ctx)
	if !ok || !token.Valid() || !currentOK || !runtimeapi.SameInvocation(token, current) {
		return nil, fmt.Errorf("permission_denied: internal binding requires the current runtime invocation")
	}
	if encodeInput == nil || decodeOutput == nil {
		return nil, fmt.Errorf("capability_unavailable: internal binding %s has no cross-process codec", address)
	}
	encoded, err := encodeInput(input)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("encode internal binding %s input: %w", address, err))
	}
	output, err := invokeProcessLinkedBindingJSON(ctx, config, address, callerPackage, token, encoded)
	if err != nil {
		return nil, err
	}
	value, err := decodeOutput(output)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("decode internal binding %s output: %w", address, err))
	}
	return value, nil
}
