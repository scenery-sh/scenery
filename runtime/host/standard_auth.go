package host

import (
	"context"
	"net/http"

	"scenery.sh/internal/authbridge"
	"scenery.sh/internal/runtimeapp"
)

type standardAuthRuntime struct{}

func init() { authbridge.BindRuntime(standardAuthRuntime{}) }

func (standardAuthRuntime) RegisterAuthenticator(authenticate func(context.Context, string) (runtimeapp.AuthInfo, error)) {
	RegisterAuthHandler(&AuthHandler{Service: "auth", Name: "AuthHandler", Authenticate: authenticate})
}

func (standardAuthRuntime) RegisterEndpoint(endpoint authbridge.Endpoint) {
	registered := &Endpoint{
		Service: endpoint.Service, Name: endpoint.Name, Access: endpoint.Access,
		Path: endpoint.Path, Methods: endpoint.Methods, Raw: endpoint.Raw,
		RawHandler: endpoint.RawHandler, Invoke: endpoint.Invoke,
	}
	if endpoint.Decode != nil {
		registered.DecodeContractRequest = func(request *http.Request, values map[string]string) (ContractDecodedRequest, error) {
			decoded, err := endpoint.Decode(request, values)
			return ContractDecodedRequest{Payload: decoded.Payload, PathArgs: decoded.PathArgs}, err
		}
	}
	if endpoint.Encode != nil {
		registered.EncodeContractOutcome = func(request *http.Request, value any) (ContractHTTPResponse, error) {
			response, err := endpoint.Encode(request, value)
			return ContractHTTPResponse{Status: response.Status, Headers: response.Headers, Body: response.Body}, err
		}
	}
	RegisterEndpoint(registered)
}

func (standardAuthRuntime) MarkServiceInitialized(shutdown func(context.Context)) {
	MarkServiceInitialized("auth", shutdown)
}

func (standardAuthRuntime) DecodeJSON(request *http.Request, target any) error {
	return decodeContractInputInto(request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json"}}, target)
}

func (standardAuthRuntime) EncodeJSON(status int, value any) (authbridge.Response, error) {
	response, err := EncodeContractJSON(status, value)
	return authbridge.Response{Status: response.Status, Headers: response.Headers, Body: response.Body}, err
}
