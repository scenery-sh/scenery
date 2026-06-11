package runtime

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
)

type runtimeMocks struct {
	mu           sync.RWMutex
	endpointRefs map[uintptr]string
	endpointMock map[string]any
	serviceMock  map[reflect.Type]func() (any, error)
}

var mocks = &runtimeMocks{
	endpointRefs: make(map[uintptr]string),
	endpointMock: make(map[string]any),
	serviceMock:  make(map[reflect.Type]func() (any, error)),
}

func RegisterEndpointFunc(fn any, service, name string) {
	rv := reflect.ValueOf(fn)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		panic(fmt.Sprintf("runtime: endpoint ref for %s.%s must be a function", service, name))
	}
	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	mocks.endpointRefs[rv.Pointer()] = endpointKey(service, name)
}

func SetEndpointMock(ref, mock any) (func(), error) {
	key, err := lookupEndpointRef(ref)
	if err != nil {
		return nil, err
	}
	mocks.mu.Lock()
	prev, hadPrev := mocks.endpointMock[key]
	mocks.endpointMock[key] = mock
	mocks.mu.Unlock()
	return func() {
		mocks.mu.Lock()
		defer mocks.mu.Unlock()
		if hadPrev {
			mocks.endpointMock[key] = prev
			return
		}
		delete(mocks.endpointMock, key)
	}, nil
}

func SetServiceMock(mock any) (func(), error) {
	typ, factory, err := compileServiceMock(mock)
	if err != nil {
		return nil, err
	}
	mocks.mu.Lock()
	prev, hadPrev := mocks.serviceMock[typ]
	mocks.serviceMock[typ] = factory
	mocks.mu.Unlock()
	return func() {
		mocks.mu.Lock()
		defer mocks.mu.Unlock()
		if hadPrev {
			mocks.serviceMock[typ] = prev
			return
		}
		delete(mocks.serviceMock, typ)
	}, nil
}

func ClearMocks() {
	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	mocks.endpointMock = make(map[string]any)
	mocks.serviceMock = make(map[reflect.Type]func() (any, error))
}

func LookupServiceMock(typ reflect.Type) (any, bool, error) {
	mocks.mu.RLock()
	factory, ok := mocks.serviceMock[typ]
	mocks.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	value, err := factory()
	if err != nil {
		return nil, true, err
	}
	if value == nil {
		return nil, true, nil
	}
	rv := reflect.ValueOf(value)
	if !rv.Type().AssignableTo(typ) {
		return nil, true, fmt.Errorf("runtime: service mock returned %T, want %s", value, typ)
	}
	return value, true, nil
}

func invokeTypedEndpointMock(ep *Endpoint, ctx context.Context, pathArgs []any, payload any) (any, bool, error) {
	mock, ok := lookupEndpointMock(ep)
	if !ok {
		return nil, false, nil
	}
	resp, err := callTypedMock(mock, ep, ctx, pathArgs, payload)
	return resp, true, err
}

func invokeRawEndpointMock(ep *Endpoint, w http.ResponseWriter, req *http.Request) (bool, error) {
	mock, ok := lookupEndpointMock(ep)
	if !ok {
		return false, nil
	}
	return true, callRawMock(mock, w, req)
}

func lookupEndpointMock(ep *Endpoint) (any, bool) {
	mocks.mu.RLock()
	defer mocks.mu.RUnlock()
	mock, ok := mocks.endpointMock[endpointKey(ep.Service, ep.Name)]
	return mock, ok
}

func lookupEndpointRef(ref any) (string, error) {
	rv := reflect.ValueOf(ref)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return "", fmt.Errorf("runtime: endpoint ref must be a function")
	}
	mocks.mu.RLock()
	key, ok := mocks.endpointRefs[rv.Pointer()]
	mocks.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("runtime: endpoint ref %T is not registered", ref)
	}
	return key, nil
}

func compileServiceMock(mock any) (reflect.Type, func() (any, error), error) {
	rv := reflect.ValueOf(mock)
	if !rv.IsValid() {
		return nil, nil, fmt.Errorf("runtime: service mock must not be nil")
	}
	if rv.Kind() != reflect.Func {
		return rv.Type(), func() (any, error) { return mock, nil }, nil
	}
	rt := rv.Type()
	if rt.IsVariadic() || rt.NumIn() != 0 || rt.NumOut() < 1 || rt.NumOut() > 2 {
		return nil, nil, fmt.Errorf("runtime: service mock factory must have signature func() T or func() (T, error)")
	}
	if rt.NumOut() == 2 && !rt.Out(1).Implements(reflect.TypeFor[error]()) {
		return nil, nil, fmt.Errorf("runtime: service mock factory second return must be error")
	}
	typ := rt.Out(0)
	return typ, func() (any, error) {
		out := rv.Call(nil)
		if len(out) == 2 && !out[1].IsNil() {
			err, _ := reflect.TypeAssert[error](out[1])
			return nil, err
		}
		if isNilableValue(out[0]) && out[0].IsNil() {
			return nil, nil
		}
		return out[0].Interface(), nil
	}, nil
}

func callTypedMock(mock any, ep *Endpoint, ctx context.Context, pathArgs []any, payload any) (any, error) {
	rv := reflect.ValueOf(mock)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s must be a function", ep.Service, ep.Name)
	}
	rt := rv.Type()
	if rt.IsVariadic() {
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s must not be variadic", ep.Service, ep.Name)
	}
	if rt.NumIn() == 0 {
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s must accept context.Context", ep.Service, ep.Name)
	}
	errorType := reflect.TypeFor[error]()
	switch {
	case ep.ResponseType == nil && (rt.NumOut() != 1 || !rt.Out(0).Implements(errorType)):
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s must return error", ep.Service, ep.Name)
	case ep.ResponseType != nil && (rt.NumOut() != 2 || !rt.Out(1).Implements(errorType)):
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s must return (%s, error)", ep.Service, ep.Name, ep.ResponseType)
	}

	args := make([]reflect.Value, 0, 1+len(pathArgs)+1)
	ctxValue, err := adaptMockArg(ctx, rt.In(0))
	if err != nil {
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s context arg: %w", ep.Service, ep.Name, err)
	}
	args = append(args, ctxValue)
	for i, pathArg := range pathArgs {
		if i+1 >= rt.NumIn() {
			return nil, fmt.Errorf("runtime: endpoint mock for %s.%s has too few parameters", ep.Service, ep.Name)
		}
		arg, err := adaptMockArg(pathArg, rt.In(i+1))
		if err != nil {
			return nil, fmt.Errorf("runtime: endpoint mock for %s.%s path arg %d: %w", ep.Service, ep.Name, i, err)
		}
		args = append(args, arg)
	}
	if ep.PayloadType != nil {
		if len(args) >= rt.NumIn() {
			return nil, fmt.Errorf("runtime: endpoint mock for %s.%s has too few parameters", ep.Service, ep.Name)
		}
		arg, err := adaptMockArg(payload, rt.In(len(args)))
		if err != nil {
			return nil, fmt.Errorf("runtime: endpoint mock for %s.%s payload: %w", ep.Service, ep.Name, err)
		}
		args = append(args, arg)
	}
	if len(args) != rt.NumIn() {
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s expects %d args, got %d", ep.Service, ep.Name, rt.NumIn(), len(args))
	}

	out := rv.Call(args)
	switch len(out) {
	case 1:
		if !out[0].IsNil() {
			err, _ := reflect.TypeAssert[error](out[0])
			return nil, err
		}
		return nil, nil
	case 2:
		var err error
		if !out[1].IsNil() {
			err, _ = reflect.TypeAssert[error](out[1])
		}
		if isNilableValue(out[0]) && out[0].IsNil() {
			return nil, err
		}
		return out[0].Interface(), err
	default:
		return nil, fmt.Errorf("runtime: endpoint mock for %s.%s has invalid return signature", ep.Service, ep.Name)
	}
}

func callRawMock(mock any, w http.ResponseWriter, req *http.Request) error {
	rv := reflect.ValueOf(mock)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return fmt.Errorf("runtime: raw endpoint mock must be a function")
	}
	rt := rv.Type()
	if rt.IsVariadic() || rt.NumIn() != 2 || rt.NumOut() != 0 {
		return fmt.Errorf("runtime: raw endpoint mock must have signature func(http.ResponseWriter, *http.Request)")
	}
	writerArg, err := adaptMockArg(w, rt.In(0))
	if err != nil {
		return err
	}
	reqArg, err := adaptMockArg(req, rt.In(1))
	if err != nil {
		return err
	}
	rv.Call([]reflect.Value{writerArg, reqArg})
	return nil
}

func adaptMockArg(value any, typ reflect.Type) (reflect.Value, error) {
	if value == nil {
		return reflect.Zero(typ), nil
	}
	rv := reflect.ValueOf(value)
	if rv.Type().AssignableTo(typ) {
		return rv, nil
	}
	if rv.Type().ConvertibleTo(typ) {
		return rv.Convert(typ), nil
	}
	return reflect.Value{}, fmt.Errorf("cannot use %s as %s", rv.Type(), typ)
}

func isNilableValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
