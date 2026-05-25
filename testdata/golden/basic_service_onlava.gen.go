package service

import (
	"context"
	"encoding/json"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
	onlavatemporal "github.com/pbrazdil/onlava/temporal"
	"net/http"
	"sync"
	"time"
)

var onlavaInternalServiceService struct {
	once sync.Once
	svc  *Service
	err  error
}

func onlavaInternalGetService() (*Service, error) {
	if mock, ok, err := onlavaruntime.LookupServiceMock(onlavaruntime.TypeOf[*Service]()); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if mock == nil {
			return (*Service)(nil), nil
		}
		return mock.(*Service), nil
	}
	onlavaInternalServiceService.once.Do(func() {
		started := time.Now()
		onlavaInternalServiceService.svc, onlavaInternalServiceService.err = initService()
		onlavaruntime.RecordServiceInit("service", time.Since(started), onlavaInternalServiceService.err)
	})
	return onlavaInternalServiceService.svc, onlavaInternalServiceService.err
}

func onlavaInternalCallAuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	resp, err := onlavaruntime.CallEndpoint(ctx, "service", "AuthEcho", nil, nil)
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
	return onlavaInternalCallAuthEcho(ctx)
}

func (s *Service) AuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	return onlavaInternalCallAuthEcho(ctx)
}

func onlavaInternalCallCallPrivate(ctx context.Context) (*EchoResponse, error) {
	resp, err := onlavaruntime.CallEndpoint(ctx, "service", "CallPrivate", nil, nil)
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
	return onlavaInternalCallCallPrivate(ctx)
}

func (s *Service) CallPrivate(ctx context.Context) (*EchoResponse, error) {
	return onlavaInternalCallCallPrivate(ctx)
}

func onlavaInternalCallCustomStatus(ctx context.Context) (*StatusResponse, error) {
	resp, err := onlavaruntime.CallEndpoint(ctx, "service", "CustomStatus", nil, nil)
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
	return onlavaInternalCallCustomStatus(ctx)
}

func (s *Service) CustomStatus(ctx context.Context) (*StatusResponse, error) {
	return onlavaInternalCallCustomStatus(ctx)
}

func onlavaInternalCallEcho(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	resp, err := onlavaruntime.CallEndpoint(ctx, "service", "Echo", []any{name}, req)
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
	return onlavaInternalCallEcho(ctx, name, req)
}

func (s *Service) Echo(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	return onlavaInternalCallEcho(ctx, name, req)
}

func Raw(w http.ResponseWriter, req *http.Request) {
	svc, err := onlavaInternalGetService()
	if err != nil {
		panic(err)
	}
	svc.onlavaInternalImplRaw(w, req)
}

func (s *Service) Raw(w http.ResponseWriter, req *http.Request) {
	s.onlavaInternalImplRaw(w, req)
}

func onlavaInternalCallSecret(ctx context.Context) (*EchoResponse, error) {
	resp, err := onlavaruntime.CallEndpoint(ctx, "service", "Secret", nil, nil)
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
	return onlavaInternalCallSecret(ctx)
}

func (s *Service) Secret(ctx context.Context) (*EchoResponse, error) {
	return onlavaInternalCallSecret(ctx)
}

func init() {
	onlavaruntime.RegisterServiceInitializer("service", func() error {
		_, err := onlavaInternalGetService()
		return err
	})
	onlavatemporal.RegisterServiceAccessorFor[*Service](func() (any, error) {
		return onlavaInternalGetService()
	})
	onlavaruntime.RegisterEndpointFunc(AuthEcho, "service", "AuthEcho")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:        "service",
		Name:           "AuthEcho",
		Access:         onlavaruntime.Auth,
		Raw:            false,
		Path:           "/service.AuthEcho",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   onlavaruntime.TypeOf[*AuthEchoResponse](),
		WireID:         "service.AuthEcho",
		WireSchemaHash: "20fd6ec3879a6e2ac2ab2e049730900cee7f2f72ff19daf06e5af85bf4d5fc88",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.onlavaInternalImplAuthEcho(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	onlavaruntime.RegisterEndpointFunc(CallPrivate, "service", "CallPrivate")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:        "service",
		Name:           "CallPrivate",
		Access:         onlavaruntime.Public,
		Raw:            false,
		Path:           "/service.CallPrivate",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   onlavaruntime.TypeOf[*EchoResponse](),
		WireID:         "service.CallPrivate",
		WireSchemaHash: "5af6529089150ef71d5f99a43495bac787fad6686999186bc00501eee1006811",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.onlavaInternalImplCallPrivate(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	onlavaruntime.RegisterEndpointFunc(CustomStatus, "service", "CustomStatus")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:        "service",
		Name:           "CustomStatus",
		Access:         onlavaruntime.Public,
		Raw:            false,
		Path:           "/service.CustomStatus",
		Methods:        []string{"GET", "POST"},
		PathParams:     nil,
		PayloadType:    nil,
		ResponseType:   onlavaruntime.TypeOf[*StatusResponse](),
		WireID:         "service.CustomStatus",
		WireSchemaHash: "e6acef4d2a82ddf5d8722a880ff14fe975050b64bb9048092773cb1a1059a9c1",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.onlavaInternalImplCustomStatus(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	onlavaruntime.RegisterEndpointFunc(Echo, "service", "Echo")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:        "service",
		Name:           "Echo",
		Access:         onlavaruntime.Public,
		Raw:            false,
		Path:           "/echo/:name",
		Methods:        []string{"GET", "POST"},
		PathParams:     []onlavaruntime.ParamSpec{onlavaruntime.ParamSpec{Name: "name", Kind: onlavaruntime.ParamString}},
		PayloadType:    onlavaruntime.TypeOf[*EchoRequest](),
		ResponseType:   onlavaruntime.TypeOf[*EchoResponse](),
		WireID:         "service.Echo",
		WireSchemaHash: "37f11f8e50ad4dc2fb4c6a14a2e4c4d56aeb1702705bed8bdeddc8def8d6fbf7",
		WireAvailable:  true,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.onlavaInternalImplEcho(ctx, pathArgs[0].(string), payload.(*EchoRequest))
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
		WireInvoke: func(ctx context.Context, pathArgs []any, payloadJSON []byte) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			var payload *EchoRequest
			if len(payloadJSON) != 0 {
				if err := json.Unmarshal(payloadJSON, &payload); err != nil {
					return nil, err
				}
			}
			onlavaruntime.SetCurrentRequestPayload(ctx, payload)
			resp, err := svc.onlavaInternalImplEcho(ctx, pathArgs[0].(string), payload)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
		WireInvokeJSON: func(ctx context.Context, pathArgs []any, payloadJSON []byte) ([]byte, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			var payload *EchoRequest
			if len(payloadJSON) != 0 {
				if err := json.Unmarshal(payloadJSON, &payload); err != nil {
					return nil, err
				}
			}
			onlavaruntime.SetCurrentRequestPayload(ctx, payload)
			resp, err := svc.onlavaInternalImplEcho(ctx, pathArgs[0].(string), payload)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		},
	})
	onlavaruntime.RegisterEndpointFunc(Raw, "service", "Raw")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:               "service",
		Name:                  "Raw",
		Access:                onlavaruntime.Public,
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
			svc, err := onlavaInternalGetService()
			if err != nil {
				panic(err)
			}
			svc.onlavaInternalImplRaw(w, req)
		},
	})
	onlavaruntime.RegisterEndpointFunc(Secret, "service", "Secret")
	onlavaruntime.RegisterEndpoint(&onlavaruntime.Endpoint{
		Service:               "service",
		Name:                  "Secret",
		Access:                onlavaruntime.Private,
		Raw:                   false,
		Path:                  "/service.Secret",
		Methods:               []string{"GET", "POST"},
		PathParams:            nil,
		PayloadType:           nil,
		ResponseType:          onlavaruntime.TypeOf[*EchoResponse](),
		WireID:                "service.Secret",
		WireSchemaHash:        "",
		WireAvailable:         false,
		WireUnsupportedReason: "private endpoint",
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := onlavaInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.onlavaInternalImplSecret(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	onlavaruntime.RegisterAuthHandler(&onlavaruntime.AuthHandler{
		Name:         "AuthHandler",
		Service:      "service",
		ParamType:    onlavaruntime.TypeOf[string](),
		AuthDataType: onlavaruntime.TypeOf[*AuthData](),
		Authenticate: func(ctx context.Context, param any) (onlavaruntime.AuthInfo, error) {
			service, err := onlavaInternalGetService()
			if err != nil {
				return onlavaruntime.AuthInfo{}, err
			}
			uid, data, err := service.AuthHandler(ctx, param.(string))
			if err != nil {
				return onlavaruntime.AuthInfo{}, err
			}
			return onlavaruntime.AuthInfo{UID: string(uid), Data: data}, nil
		},
	})
}
