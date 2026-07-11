package runtime

import (
	"context"
	"fmt"
	"strings"

	"scenery.sh/internal/runtimeapi"
)

type ContractInternalInvoke func(context.Context, any, any) (any, error)

type ContractInternalBindingRegistration struct {
	Address      string
	Visibility   string
	Package      string
	Policy       *ContractHTTPPolicy
	DecodeInput  func([]byte) (any, error)
	EncodeOutput func(any) ([]byte, error)
	Invoke       ContractInternalInvoke
}

func RegisterContractInternalBinding(address string, invoke ContractInternalInvoke) error {
	return RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{Address: address, Visibility: "application", Invoke: invoke})
}

func InvokeContractBindingJSON(ctx context.Context, address, callerPackage string, input []byte) ([]byte, error) {
	global.mu.RLock()
	registration := global.contractBindings[address]
	global.mu.RUnlock()
	if registration.Invoke == nil {
		return nil, fmt.Errorf("contract internal binding %s is not registered", address)
	}
	if registration.DecodeInput == nil || registration.EncodeOutput == nil {
		return nil, fmt.Errorf("capability_unavailable: contract internal binding %s has no JSON codec", address)
	}
	typed, err := registration.DecodeInput(input)
	if err != nil {
		return nil, fmt.Errorf("invalid_argument: decode internal binding input: %w", err)
	}
	invocation, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok || !invocation.Valid() {
		return nil, fmt.Errorf("permission_denied: internal binding requires the current runtime invocation")
	}
	value, err := InvokeContractBindingFrom(ctx, address, callerPackage, invocation, typed)
	if err != nil {
		return nil, err
	}
	encoded, err := registration.EncodeOutput(value)
	if err != nil {
		return nil, ContractSystemError(fmt.Errorf("encode internal binding output: %w", err))
	}
	return encoded, nil
}

func RegisterContractInternalBindingWithPolicy(registration ContractInternalBindingRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.Visibility = strings.TrimSpace(registration.Visibility)
	registration.Package = strings.TrimSpace(registration.Package)
	if registration.Address == "" || registration.Invoke == nil {
		return fmt.Errorf("contract internal binding requires an address and invoke function")
	}
	if registration.Visibility == "" {
		registration.Visibility = "application"
	}
	if registration.Visibility != "application" && registration.Visibility != "package" {
		return fmt.Errorf("contract internal binding %s has unsupported visibility %q", registration.Address, registration.Visibility)
	}
	if registration.Visibility == "package" && registration.Package == "" {
		return fmt.Errorf("package-visible contract internal binding %s requires a package identity", registration.Address)
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("contract internal binding %s policy: %w", registration.Address, err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractBindings == nil {
		global.contractBindings = make(map[string]ContractInternalBindingRegistration)
	}
	if _, exists := global.contractBindings[registration.Address]; exists {
		return fmt.Errorf("duplicate contract internal binding %s", registration.Address)
	}
	global.contractBindings[registration.Address] = registration
	return nil
}

func InvokeContractBinding(ctx context.Context, address string, invocation, input any) (any, error) {
	return InvokeContractBindingFrom(ctx, address, "", invocation, input)
}

func InvokeContractBindingFrom(ctx context.Context, address, callerPackage string, invocation, input any) (any, error) {
	global.mu.RLock()
	registration := global.contractBindings[address]
	global.mu.RUnlock()
	if registration.Invoke == nil {
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
	if registration.Visibility == "package" && strings.TrimSpace(callerPackage) != registration.Package {
		return nil, fmt.Errorf("permission_denied: internal binding %s is visible only to package %s", address, registration.Package)
	}
	return InvokeContractPolicy(ctx, registration.Policy, input, func(callCtx context.Context) (any, error) {
		return registration.Invoke(callCtx, invocation, input)
	})
}
