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
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeapi"
)

// A process link connects generated service processes of one development
// session. The supervisor writes a private file naming the session token, the
// process that owns each internal binding and each service process's listener;
// a binding absent from this process's registry is invoked in its owner with the
// caller's invocation metadata.
const processLinkBindingPath = "/__scenery/process/v1/bindings/invoke"

const processLinkMaxBody = 32 << 20

type processLinkTarget struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type processLinkConfig struct {
	Token     string                       `json:"token"`
	Bindings  map[string]processLinkTarget `json:"bindings"`
	Processes map[string]processLinkTarget `json:"processes,omitempty"`
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

type processLinkRequest struct {
	Address       string                `json:"address"`
	CallerPackage string                `json:"caller_package,omitempty"`
	Invocation    processLinkInvocation `json:"invocation"`
	Input         json.RawMessage       `json:"input"`
}

type processLinkError struct {
	Kind    string        `json:"kind"`
	Outcome string        `json:"outcome,omitempty"`
	Status  int           `json:"status,omitempty"`
	Code    errs.ErrCode  `json:"code,omitempty"`
	Message string        `json:"message"`
	Meta    errs.Metadata `json:"meta,omitempty"`
	Cause   string        `json:"cause,omitempty"`
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
	for _, targets := range []map[string]processLinkTarget{config.Bindings, config.Processes} {
		for name, target := range targets {
			if strings.TrimSpace(name) == "" || (target.Network != "unix" && target.Network != "tcp") || strings.TrimSpace(target.Address) == "" {
				return nil, fmt.Errorf("runtime: process link target for %q is invalid", name)
			}
		}
	}
	return &config, nil
}

func processLinkedBinding(address string) (*processLinkConfig, processLinkTarget, bool, error) {
	config, err := currentProcessLink()
	if err != nil || config == nil {
		return nil, processLinkTarget{}, false, err
	}
	target, ok := config.Bindings[address]
	return config, target, ok, nil
}

func processLinkClient(target processLinkTarget) *http.Client {
	processLinkState.mu.Lock()
	defer processLinkState.mu.Unlock()
	if client := processLinkState.clients[target]; client != nil {
		return client
	}
	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, target.Network, target.Address)
		},
		DisableCompression:  true,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}}
	processLinkState.clients[target] = client
	return client
}

func invokeProcessLinkedBindingJSON(ctx context.Context, config *processLinkConfig, target processLinkTarget, address, callerPackage string, invocation runtimeapi.Invocation, input []byte) ([]byte, error) {
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
	body, err := json.Marshal(processLinkRequest{Address: address, CallerPackage: callerPackage, Invocation: metadata, Input: json.RawMessage(input)})
	if err != nil {
		return nil, fmt.Errorf("invalid_argument: encode process link request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scenery-process"+processLinkBindingPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := processLinkClient(target).Do(request)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("internal binding %s owner is unavailable: %w", address, err))
	}
	defer func() { _ = response.Body.Close() }()
	var decoded processLinkResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, processLinkMaxBody)).Decode(&decoded); err != nil {
		return nil, ContractSystemError(fmt.Errorf("decode internal binding %s response (HTTP %d): %w", address, response.StatusCode, err))
	}
	if decoded.Error != nil {
		return nil, decoded.Error.err()
	}
	if response.StatusCode != http.StatusOK {
		return nil, ContractSystemError(fmt.Errorf("internal binding %s owner returned HTTP %d", address, response.StatusCode))
	}
	return decoded.Output, nil
}

func newProcessLinkError(err error) *processLinkError {
	var transport *ContractTransportError
	if errors.As(err, &transport) {
		encoded := &processLinkError{Kind: "transport", Outcome: transport.Outcome, Status: transport.Status, Message: transport.Message}
		if transport.Cause != nil {
			encoded.Cause = transport.Cause.Error()
		}
		return encoded
	}
	if typed, ok := errs.As(err); ok {
		return &processLinkError{Kind: "errs", Code: typed.Code, Message: typed.Message, Meta: typed.Meta}
	}
	return &processLinkError{Kind: "error", Message: err.Error()}
}

func (encoded *processLinkError) err() error {
	switch encoded.Kind {
	case "transport":
		transport := &ContractTransportError{Outcome: encoded.Outcome, Status: encoded.Status, Message: encoded.Message}
		if encoded.Cause != "" {
			transport.Cause = errors.New(encoded.Cause)
		}
		return transport
	case "errs":
		return &errs.Error{Code: encoded.Code, Message: encoded.Message, Meta: encoded.Meta}
	default:
		return errors.New(encoded.Message)
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
	var body processLinkRequest
	decoder := json.NewDecoder(io.LimitReader(req.Body, processLinkMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Address) == "" || strings.TrimSpace(body.Invocation.ID) == "" {
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
	ctx = runtimeapi.WithInvocation(ctx, runtimeapi.NewInvocationWithMetadata(metadata))
	output, err := invokeRegisteredContractBindingJSON(ctx, registration, body.Address, body.CallerPackage, body.Input)
	if err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: output})
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
	config, target, linked, err := processLinkedBinding(address)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	if !linked {
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
	output, err := invokeProcessLinkedBindingJSON(ctx, config, target, address, callerPackage, token, encoded)
	if err != nil {
		return nil, err
	}
	value, err := decodeOutput(output)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("decode internal binding %s output: %w", address, err))
	}
	return value, nil
}
