package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/workerabi"
	"scenery.sh/storage"
)

type workerCapabilitiesKey struct{}

type workerCapabilities struct {
	scope     string
	token     string
	transport *http.Transport
}

func capabilitiesFromContext(ctx context.Context) *workerCapabilities {
	if ctx == nil {
		return nil
	}
	capabilities, _ := ctx.Value(workerCapabilitiesKey{}).(*workerCapabilities)
	return capabilities
}

func (workerCapabilitiesKey) RoundTrip(request *http.Request) (*http.Response, error) {
	capabilities := capabilitiesFromContext(request.Context())
	if capabilities == nil {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("worker capability requires an active invocation").Err()
	}
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+capabilities.token)
	request.Header.Set(workerabi.ScopeHeader, capabilities.scope)
	request.GetBody = nil
	return capabilities.transport.RoundTrip(request)
}

// InvokeWorkerBinding is the generated worker client's entry point. The Host
// resolves the local client name and the caller's authority from its scope.
func InvokeWorkerBinding(ctx context.Context, invocation any, client string, enqueue bool, input []byte) ([]byte, error) {
	current, currentOK := runtimeapi.InvocationFromContext(ctx)
	token, ok := invocation.(runtimeapi.Invocation)
	if !ok || !token.Valid() || !currentOK || !runtimeapi.SameInvocation(token, current) {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("internal binding requires the current runtime invocation").Err()
	}
	path := "binding/invoke"
	if enqueue {
		path = "durable/enqueue"
	}
	data, err := callWorkerCapability(ctx, http.MethodPost, path, struct {
		Client string          `json:"client"`
		Input  json.RawMessage `json:"input"`
	}{Client: client, Input: input})
	if err != nil {
		return nil, err
	}
	var response processLinkResponse
	if err := json.Unmarshal(data, &response); err != nil || (response.Error == nil) == (len(response.Output) == 0) {
		return nil, workerUnavailable(workerabi.Unknown, fmt.Errorf("invalid binding capability response"))
	}
	if response.Error != nil {
		return nil, response.Error.err()
	}
	return response.Output, nil
}

func callWorkerCapability(ctx context.Context, method, path string, input any) ([]byte, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		if len(encoded) > workerabi.MaxMetadataBytes {
			return nil, workerUnavailable(workerabi.NotSent, fmt.Errorf("capability exceeds metadata limit"))
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://host"+workerabi.CapabilityPrefix+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (workerCapabilitiesKey{}).RoundTrip(request)
	if err != nil {
		if _, ok := errs.As(err); ok {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if workerabi.WasNotSent(err) {
			return nil, workerUnavailable(workerabi.NotSent, err)
		}
		return nil, workerUnavailable(workerabi.Unknown, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		delivery := workerabi.Unknown
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			delivery = workerabi.NotSent
		}
		return nil, workerUnavailable(delivery, fmt.Errorf("host capability %s returned HTTP %d", path, response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, workerabi.MaxMetadataBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, workerUnavailable(workerabi.Unknown, err)
	}
	if len(data) > workerabi.MaxMetadataBytes {
		return nil, workerUnavailable(workerabi.Unknown, fmt.Errorf("capability response exceeds metadata limit"))
	}
	return data, nil
}

// WorkerStorage supplies a generated dependency backed by an invocation-scoped
// Host capability. A store handle can be retained by the service constructor.
func WorkerStorage(name string) storage.Store {
	return storage.NewWorkerStore(name, workerCapabilitiesKey{})
}

// WorkerConfigSnapshot reads this worker's scoped deployment configuration.
// The private startup snapshot also supports constructors before invocations.
func WorkerConfigSnapshot(ctx context.Context) (*ConfigSnapshot, error) {
	data, err := callWorkerCapability(ctx, http.MethodGet, "config/snapshot", nil)
	if err != nil {
		return nil, err
	}
	return DecodeConfigSnapshot(data)
}

// ReportWorkerTelemetry submits one batch correlated by the Host's scope.
func ReportWorkerTelemetry(ctx context.Context, records []WorkerTelemetryRecord) error {
	_, err := callWorkerCapability(ctx, http.MethodPost, "telemetry", records)
	return err
}

func workerConnectionToken(ctx context.Context, provider string, scopes []string) (string, error) {
	data, err := callWorkerCapability(ctx, http.MethodPost, "connections/token", struct {
		Provider string   `json:"provider"`
		Scopes   []string `json:"scopes"`
	}{Provider: provider, Scopes: scopes})
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", err
	}
	return response.Token, nil
}
