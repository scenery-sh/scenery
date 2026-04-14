package service

import (
	"context"
	"net/http"
	pulseruntime "pulse.dev/runtime"
	"sync"
	"time"
)

var pulseInternalServiceService struct {
	once sync.Once
	svc  *Service
	err  error
}

func pulseInternalGetService() (*Service, error) {
	pulseInternalServiceService.once.Do(func() {
		started := time.Now()
		pulseInternalServiceService.svc, pulseInternalServiceService.err = initService()
		pulseruntime.RecordServiceInit("service", time.Since(started), pulseInternalServiceService.err)
	})
	return pulseInternalServiceService.svc, pulseInternalServiceService.err
}

func pulseInternalCallAuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	resp, err := pulseruntime.CallEndpoint(ctx, "service", "AuthEcho", nil, nil)
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
	return pulseInternalCallAuthEcho(ctx)
}

func (s *Service) AuthEcho(ctx context.Context) (*AuthEchoResponse, error) {
	return pulseInternalCallAuthEcho(ctx)
}

func pulseInternalCallCallPrivate(ctx context.Context) (*EchoResponse, error) {
	resp, err := pulseruntime.CallEndpoint(ctx, "service", "CallPrivate", nil, nil)
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
	return pulseInternalCallCallPrivate(ctx)
}

func (s *Service) CallPrivate(ctx context.Context) (*EchoResponse, error) {
	return pulseInternalCallCallPrivate(ctx)
}

func pulseInternalCallCustomStatus(ctx context.Context) (*StatusResponse, error) {
	resp, err := pulseruntime.CallEndpoint(ctx, "service", "CustomStatus", nil, nil)
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
	return pulseInternalCallCustomStatus(ctx)
}

func (s *Service) CustomStatus(ctx context.Context) (*StatusResponse, error) {
	return pulseInternalCallCustomStatus(ctx)
}

func pulseInternalCallEcho(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	resp, err := pulseruntime.CallEndpoint(ctx, "service", "Echo", []any{name}, req)
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
	return pulseInternalCallEcho(ctx, name, req)
}

func (s *Service) Echo(ctx context.Context, name string, req *EchoRequest) (*EchoResponse, error) {
	return pulseInternalCallEcho(ctx, name, req)
}

func Raw(w http.ResponseWriter, req *http.Request) {
	svc, err := pulseInternalGetService()
	if err != nil {
		panic(err)
	}
	svc.pulseInternalImplRaw(w, req)
}

func (s *Service) Raw(w http.ResponseWriter, req *http.Request) {
	s.pulseInternalImplRaw(w, req)
}

func pulseInternalCallSecret(ctx context.Context) (*EchoResponse, error) {
	resp, err := pulseruntime.CallEndpoint(ctx, "service", "Secret", nil, nil)
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
	return pulseInternalCallSecret(ctx)
}

func (s *Service) Secret(ctx context.Context) (*EchoResponse, error) {
	return pulseInternalCallSecret(ctx)
}

func init() {
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "AuthEcho",
		Access:       pulseruntime.Auth,
		Raw:          false,
		Path:         "/service.AuthEcho",
		Methods:      []string{"GET", "POST"},
		PathParams:   nil,
		PayloadType:  nil,
		ResponseType: pulseruntime.TypeOf[*AuthEchoResponse](),
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := pulseInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.pulseInternalImplAuthEcho(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "CallPrivate",
		Access:       pulseruntime.Public,
		Raw:          false,
		Path:         "/service.CallPrivate",
		Methods:      []string{"GET", "POST"},
		PathParams:   nil,
		PayloadType:  nil,
		ResponseType: pulseruntime.TypeOf[*EchoResponse](),
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := pulseInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.pulseInternalImplCallPrivate(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "CustomStatus",
		Access:       pulseruntime.Public,
		Raw:          false,
		Path:         "/service.CustomStatus",
		Methods:      []string{"GET", "POST"},
		PathParams:   nil,
		PayloadType:  nil,
		ResponseType: pulseruntime.TypeOf[*StatusResponse](),
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := pulseInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.pulseInternalImplCustomStatus(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "Echo",
		Access:       pulseruntime.Public,
		Raw:          false,
		Path:         "/echo/:name",
		Methods:      []string{"GET", "POST"},
		PathParams:   []pulseruntime.ParamSpec{pulseruntime.ParamSpec{Name: "name", Kind: pulseruntime.ParamString}},
		PayloadType:  pulseruntime.TypeOf[*EchoRequest](),
		ResponseType: pulseruntime.TypeOf[*EchoResponse](),
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := pulseInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.pulseInternalImplEcho(ctx, pathArgs[0].(string), payload.(*EchoRequest))
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "Raw",
		Access:       pulseruntime.Public,
		Raw:          true,
		Path:         "/raw/*rest",
		Methods:      []string{"*"},
		PathParams:   nil,
		PayloadType:  nil,
		ResponseType: nil,
		RawHandler: func(w http.ResponseWriter, req *http.Request) {
			svc, err := pulseInternalGetService()
			if err != nil {
				panic(err)
			}
			svc.pulseInternalImplRaw(w, req)
		},
	})
	pulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{
		Service:      "service",
		Name:         "Secret",
		Access:       pulseruntime.Private,
		Raw:          false,
		Path:         "/service.Secret",
		Methods:      []string{"GET", "POST"},
		PathParams:   nil,
		PayloadType:  nil,
		ResponseType: pulseruntime.TypeOf[*EchoResponse](),
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := pulseInternalGetService()
			if err != nil {
				return nil, err
			}
			resp, err := svc.pulseInternalImplSecret(ctx)
			if err != nil {
				return nil, err
			}
			return resp, nil
		},
	})
	pulseruntime.RegisterAuthHandler(&pulseruntime.AuthHandler{
		Name:         "AuthHandler",
		Service:      "service",
		ParamType:    pulseruntime.TypeOf[string](),
		AuthDataType: pulseruntime.TypeOf[*AuthData](),
		Authenticate: func(ctx context.Context, param any) (pulseruntime.AuthInfo, error) {
			service, err := pulseInternalGetService()
			if err != nil {
				return pulseruntime.AuthInfo{}, err
			}
			uid, data, err := service.AuthHandler(ctx, param.(string))
			if err != nil {
				return pulseruntime.AuthInfo{}, err
			}
			return pulseruntime.AuthInfo{UID: string(uid), Data: data}, nil
		},
	})
}
