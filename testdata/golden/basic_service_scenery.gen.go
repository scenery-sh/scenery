package service

import (
	"context"
	"encoding/json"
	"net/http"
	sceneryruntime "scenery.sh/runtime"
	"sync"
	"time"
)

var sceneryInternalServiceService struct {
	once sync.Once
	svc  *Service
	err  error
}

func sceneryInternalGetService() (*Service, error) {
	if mock, ok, err := sceneryruntime.LookupServiceMock(sceneryruntime.TypeOf[*Service]()); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if mock == nil {
			return (*Service)(nil), nil
		}
		return mock.(*Service), nil
	}
	sceneryInternalServiceService.once.Do(func() {
		started := time.Now()
		sceneryInternalServiceService.svc, sceneryInternalServiceService.err = initService()
		sceneryruntime.RecordServiceInit("service", time.Since(started), sceneryInternalServiceService.err)
	})
	return sceneryInternalServiceService.svc, sceneryInternalServiceService.err
}

func sceneryInternalCallAuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	resp, err := sceneryruntime.CallEndpoint(ctx, "service", "AuthEcho", nil, nil)
	if err != nil {
		var zero *AuthEchoResponse
		return zero, err
	}
	if resp == nil {
		var zero *AuthEchoResponse
		return zero, nil
	}
	return resp.(*AuthEchoResponse), nil
}

func AuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	return sceneryInternalCallAuthEcho(ctx)
}

func (s *Service) AuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	return sceneryInternalCallAuthEcho(ctx)
}

func sceneryInternalCallCallPrivate(ctx context.Context) (*EchoResponse, error) {
	resp, err := sceneryruntime.CallEndpoint(ctx, "service", "CallPrivate", nil, nil)
	if err != nil {
		var zero *EchoResponse
		return zero, err
	}
	if resp == nil {
		var zero *EchoResponse
		return zero, nil
	}
	return resp.(*EchoResponse), nil
}

func CallPrivate(ctx context.Context) (*EchoResponse, error) {
	return sceneryInternalCallCallPrivate(ctx)
}

func (s *Service) CallPrivate(ctx context.Context) (*EchoResponse, error) {
	return sceneryInternalCallCallPrivate(ctx)
}

func sceneryInternalCallCustomStatus(ctx context.Context) (*StatusResponse, error) {
	resp, err := sceneryruntime.CallEndpoint(ctx, "service", "CustomStatus", nil, nil)
	if err != nil {
		var zero *StatusResponse
		return zero, err
	}
	if resp == nil {
		var zero *StatusResponse
		return zero, nil
	}
	return resp.(*StatusResponse), nil
}

func CustomStatus(ctx context.Context) (*StatusResponse, error) {
	return sceneryInternalCallCustomStatus(ctx)
}

func (s *Service) CustomStatus(ctx context.Context) (*StatusResponse, error) {
	return sceneryInternalCallCustomStatus(ctx)
}

func sceneryInternalCallEcho(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	resp, err := sceneryruntime.CallEndpoint(ctx, "service", "Echo", []any{name}, req)
	if err != nil {
		var zero *EchoResponse
		return zero, err
	}
	if resp == nil {
		var zero *EchoResponse
		return zero, nil
	}
	return resp.(*EchoResponse), nil
}

func Echo(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	return sceneryInternalCallEcho(ctx, name, req)
}

func (s *Service) Echo(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	return sceneryInternalCallEcho(ctx, name, req)
}

func Raw(w http.ResponseWriter, req *http.Request) {
	svc, err := sceneryInternalGetService()
	if err != nil {
		panic(err)
	}
	svc.sceneryInternalImplRaw(w, req)
}

func (s *Service) Raw(w http.ResponseWriter, req *http.Request) {
	s.sceneryInternalImplRaw(w, req)
}

func sceneryInternalCallSecret(ctx context.Context) (*EchoResponse, error) {
	resp, err := sceneryruntime.CallEndpoint(ctx, "service", "Secret", nil, nil)
	if err != nil {
		var zero *EchoResponse
		return zero, err
	}
	if resp == nil {
		var zero *EchoResponse
		return zero, nil
	}
	return resp.(*EchoResponse), nil
}

func Secret(ctx context.Context) (*EchoResponse, error) {
	return sceneryInternalCallSecret(ctx)
}

func (s *Service) Secret(ctx context.Context) (*EchoResponse, error) {
	return sceneryInternalCallSecret(ctx)
}

func init() {
	sceneryruntime.RegisterServiceInitializer("service", func() error {
		_, err := sceneryInternalGetService()
		return err
	})
	sceneryruntime.RegisterEndpointFunc(AuthEcho, "service", "AuthEcho")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:        "service",
		Name:           "AuthEcho",
		Access:         sceneryruntime.Auth,
		Raw:            false,
		Path:           "/service.AuthEcho",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   sceneryruntime.TypeOf[*AuthEchoResponse](),
		WireID:         "service.AuthEcho",
		WireSchemaHash: "20fd6ec3879a6e2ac2ab2e049730900cee7f2f72ff19daf06e5af85bf4d5fc88",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.sceneryInternalImplAuthEcho(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	sceneryruntime.RegisterEndpointFunc(CallPrivate, "service", "CallPrivate")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:        "service",
		Name:           "CallPrivate",
		Access:         sceneryruntime.Public,
		Raw:            false,
		Path:           "/service.CallPrivate",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   sceneryruntime.TypeOf[*EchoResponse](),
		WireID:         "service.CallPrivate",
		WireSchemaHash: "5af6529089150ef71d5f99a43495bac787fad6686999186bc00501eee1006811",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.sceneryInternalImplCallPrivate(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	sceneryruntime.RegisterEndpointFunc(CustomStatus, "service", "CustomStatus")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:        "service",
		Name:           "CustomStatus",
		Access:         sceneryruntime.Public,
		Raw:            false,
		Path:           "/service.CustomStatus",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   sceneryruntime.TypeOf[*StatusResponse](),
		WireID:         "service.CustomStatus",
		WireSchemaHash: "d6103dd46362cd5fae4e9fad30faea5efc48aca4707b82fbc8758debfff2c1c2",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.sceneryInternalImplCustomStatus(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	sceneryruntime.RegisterEndpointFunc(Echo, "service", "Echo")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:        "service",
		Name:           "Echo",
		Access:         sceneryruntime.Public,
		Raw:            false,
		Path:           "/echo/:name",
		Methods:        []string{"GET", "POST"},
		PathParams:     []sceneryruntime.ParamSpec{sceneryruntime.ParamSpec{Name: "name", Kind: sceneryruntime.ParamString}},
		PayloadType:    sceneryruntime.TypeOf[*EchoRequest](),
		ResponseType:   sceneryruntime.TypeOf[*EchoResponse](),
		WireID:         "service.Echo",
		WireSchemaHash: "37f11f8e50ad4dc2fb4c6a14a2e4c4d56aeb1702705bed8bdeddc8def8d6fbf7",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.sceneryInternalImplEcho(ctx, pathArgs[0].(string), payload.(*EchoRequest))
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
		WireInvoke: func(ctx context.Context, pathArgs []any, payloadJSON []byte) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			var payload *EchoRequest
			if len(payloadJSON) != 0 {
				if err := json.Unmarshal(payloadJSON, &payload); err != nil {
					return nil, err
				}
			}
			sceneryruntime.SetCurrentRequestPayload(ctx, payload)
			resp, err := svc.sceneryInternalImplEcho(ctx, pathArgs[0].(string), payload)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
		WireInvokeJSON: func(ctx context.Context, pathArgs []any, payloadJSON []byte) ([]byte, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			var payload *EchoRequest
			if len(payloadJSON) != 0 {
				if err := json.Unmarshal(payloadJSON, &payload); err != nil {
					return nil, err
				}
			}
			sceneryruntime.SetCurrentRequestPayload(ctx, payload)
			resp, err := svc.sceneryInternalImplEcho(ctx, pathArgs[0].(string), payload)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		},
	})
	sceneryruntime.RegisterEndpointFunc(Raw, "service", "Raw")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:               "service",
		Name:                  "Raw",
		Access:                sceneryruntime.Public,
		Raw:                   true,
		Path:                  "/raw/*rest",
		Methods:               []string{"*"},
		PathParams:            nil,
		PayloadType:           nil,
		ResponseType:          nil,
		WireID:                "service.Raw",
		WireSchemaHash:        "",
		WireAvailable:         false,
		WireUnsupportedReason: "raw endpoint",
		RawHandler: func(w http.ResponseWriter, req *http.Request) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				panic(err)
			}
			svc.sceneryInternalImplRaw(w, req)
		},
	})
	sceneryruntime.RegisterEndpointFunc(Secret, "service", "Secret")
	sceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{
		Service:               "service",
		Name:                  "Secret",
		Access:                sceneryruntime.Private,
		Raw:                   false,
		Path:                  "/service.Secret",
		Methods:               []string{"GET", "POST"},
		PathParams:            nil,
		PayloadType:           nil,
		ResponseType:          sceneryruntime.TypeOf[*EchoResponse](),
		WireID:                "service.Secret",
		WireSchemaHash:        "",
		WireAvailable:         false,
		WireUnsupportedReason: "private endpoint",
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := sceneryInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.sceneryInternalImplSecret(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	sceneryruntime.RegisterAuthHandler(&sceneryruntime.AuthHandler{
		Name:         "AuthHandler",
		Service:      "service",
		ParamType:    sceneryruntime.TypeOf[string](),
		AuthDataType: sceneryruntime.TypeOf[*AuthData](),
		Authenticate: func(ctx context.Context, param any) (sceneryruntime.AuthInfo, error) {
			service, err := sceneryInternalGetService()
			if err != nil {
				return sceneryruntime.AuthInfo{}, err
			}
			uid, data, err := service.AuthHandler(ctx, param.(string))
			if err != nil {
				return sceneryruntime.AuthInfo{}, err
			}
			return sceneryruntime.AuthInfo{UID: string(uid), Data: data}, nil
		},
	})
}
