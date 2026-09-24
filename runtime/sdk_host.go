package runtime

import (
	"context"
	"net/http"

	"scenery.sh/internal/appsdk"
)

// The runtime serves the application-facing packages through appsdk.Host, so
// those packages depend on the small request-context layer instead of on this
// package's implementation closure.
func init() {
	appsdk.RegisterHost(sdkHost{})
}

type sdkHost struct{}

func (sdkHost) CurrentAuth() (appsdk.Auth, bool) {
	info := CurrentAuth()
	if info == nil {
		return appsdk.Auth{}, false
	}
	return appsdk.Auth{UID: info.UID, Data: info.Data}, true
}

func (sdkHost) WithAuth(ctx context.Context, auth appsdk.Auth) context.Context {
	return WithAuthContext(ctx, AuthInfo{UID: auth.UID, Data: auth.Data})
}

func (sdkHost) RegisterAuthHandler(handler appsdk.AuthHandler) {
	authenticate := handler.Authenticate
	RegisterAuthHandler(&AuthHandler{
		Service: handler.Service,
		Name:    handler.Name,
		Authenticate: func(ctx context.Context, token string) (AuthInfo, error) {
			auth, err := authenticate(ctx, token)
			return AuthInfo{UID: auth.UID, Data: auth.Data}, err
		},
	})
}

func (sdkHost) RegisterEndpoint(endpoint appsdk.Endpoint) {
	registered := &Endpoint{
		Service: endpoint.Service,
		Name:    endpoint.Name,
		Access:  endpoint.Access,
		Path:    endpoint.Path,
		Methods: endpoint.Methods,
	}
	if endpoint.RawHandler != nil {
		registered.Raw = true
		registered.RawHandler = endpoint.RawHandler
		RegisterEndpoint(registered)
		return
	}
	decode, encode := endpoint.Decode, endpoint.Encode
	registered.Invoke = endpoint.Invoke
	registered.DecodeContractRequest = func(request *http.Request, values map[string]string) (ContractDecodedRequest, error) {
		payload, pathArgs, err := decode(request, values)
		return ContractDecodedRequest{Payload: payload, PathArgs: pathArgs}, err
	}
	registered.EncodeContractOutcome = func(request *http.Request, outcome any) (ContractHTTPResponse, error) {
		response, err := encode(request, outcome)
		return ContractHTTPResponse{Status: response.Status, Headers: response.Headers, Body: response.Body}, err
	}
	RegisterEndpoint(registered)
}

func (sdkHost) MarkServiceInitialized(service string, shutdown func(context.Context)) {
	MarkServiceInitialized(service, shutdown)
}

func (sdkHost) DecodeJSON(request *http.Request, target any) error {
	return decodeContractInputInto(request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json"}}, target)
}

func (sdkHost) EncodeJSON(status int, value any) (appsdk.Response, error) {
	response, err := EncodeContractJSON(status, value)
	return appsdk.Response{Status: response.Status, Headers: response.Headers, Body: response.Body}, err
}

func (sdkHost) DurableSignal(ctx context.Context, service, jobID, name, dedupeKey string, payload []byte) error {
	return DurableSignal(ctx, service, jobID, name, dedupeKey, payload)
}

func (sdkHost) DurableStep(ctx context.Context, key string, run func(context.Context) ([]byte, error)) ([]byte, error) {
	return DurableStep(ctx, key, run)
}
