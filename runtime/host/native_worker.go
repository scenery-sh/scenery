package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"scenery.sh/internal/authbridge"
	"scenery.sh/internal/nativeprotocol"
	"scenery.sh/internal/runtimeapi"
)

// NativeWorkerClient is the explicit unary transport used only by plan 0180's
// generated kernel experiment. It contains no application Go implementation.
type NativeWorkerClient struct {
	endpoint    string
	token       string
	contract    string
	inputDigest string
	client      *http.Client
}

func NewNativeWorkerClient(endpoint, token, contract, inputDigest string) (*NativeWorkerClient, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("native worker requires an owned loopback HTTP endpoint")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() || parsed.Port() == "" || len(token) < 32 || contract == "" || inputDigest == "" {
		return nil, fmt.Errorf("native worker requires loopback address, private token and identities")
	}
	parsed.Path = "/invoke"
	return &NativeWorkerClient{endpoint: parsed.String(), token: token, contract: contract, inputDigest: inputDigest,
		client: &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (client *NativeWorkerClient) Close() { client.client.CloseIdleConnections() }

// Invoke exports only trusted runtime metadata and declared input bytes.
// Arbitrary native contexts/auth values are not serializable by this boundary.
func (client *NativeWorkerClient) Invoke(ctx context.Context, operation string, input json.RawMessage) (json.RawMessage, error) {
	state := stateFromContext(ctx)
	invocation, trusted := runtimeapi.InvocationFromContext(ctx)
	if state == nil || !trusted || !invocation.Valid() {
		return nil, ContractSystemError(fmt.Errorf("native worker requires a runtime invocation"))
	}
	metadata := nativeprotocol.RequestContext{
		Started: state.request.Started, Headers: state.request.Headers.Clone(), PathParams: state.request.PathParams, ExecutionID: state.request.ExecutionID, Deployment: state.request.Deployment, Locale: state.request.Locale,
		InvocationID: invocation.ID(), Principal: state.auth.UID, TraceID: state.request.TraceID, CallerBinding: state.request.CallerBinding,
		Deadline: state.request.Deadline, Service: state.request.Service, Endpoint: state.request.Endpoint, Method: state.request.Method, Path: state.request.Path,
	}
	if state.request.API != nil {
		metadata.AuthRequired = state.request.API.AuthRequired
	}
	if data, ok := authbridge.ExportStandardIdentity(state.auth.Data); ok {
		metadata.Auth = data
	} else if state.auth.Data != nil || state.auth.UID != "" {
		return nil, ContractSystemError(fmt.Errorf("native worker experiment supports standard auth data only"))
	}
	body, err := json.Marshal(nativeprotocol.InvocationRequest{Protocol: nativeprotocol.Protocol, ProtocolRevision: nativeprotocol.Revision, ContractRevision: client.contract, InputDigest: client.inputDigest, Operation: operation, Context: metadata, Input: input})
	if err != nil {
		return nil, ContractSystemError(err)
	}
	if len(body) > nativeprotocol.MaxMessageBytes {
		return nil, ContractSystemError(fmt.Errorf("native worker input exceeds the experimental transport limit"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ContractSystemError(err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, ContractSystemError(fmt.Errorf("native worker transport rejected invocation (%d)", response.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, nativeprotocol.MaxMessageBytes+1))
	if err != nil || len(raw) > nativeprotocol.MaxMessageBytes {
		return nil, ContractSystemError(fmt.Errorf("invalid native worker response"))
	}
	var result nativeprotocol.InvocationResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, ContractSystemError(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ContractSystemError(fmt.Errorf("invalid native worker response suffix"))
	}
	if result.Error != "" || len(result.Outcome) == 0 {
		return nil, ContractSystemError(fmt.Errorf("native worker execution failed"))
	}
	return result.Outcome, nil
}
