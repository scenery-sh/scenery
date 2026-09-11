package host

import (
	"context"
	"fmt"
	"scenery.sh/internal/nativecall"
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

func currentInternalBindings() *nativecall.Registry {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.contractBindings
}

func InvokeContractBindingJSON(ctx context.Context, address, callerPackage string, input []byte) ([]byte, error) {
	return currentInternalBindings().InvokeJSON(ctx, address, callerPackage, input)
}

func RegisterContractInternalBindingWithPolicy(registration ContractInternalBindingRegistration) error {
	native, err := nativecall.Normalize(nativecall.Registration{
		Address: registration.Address, Visibility: registration.Visibility, Package: registration.Package,
		DecodeInput: registration.DecodeInput, EncodeOutput: registration.EncodeOutput,
		Invoke: nativecall.Invoke(registration.Invoke), SystemError: func(err error) error { return ContractSystemError(err) },
	})
	if err != nil {
		return err
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("contract internal binding %s policy: %w", native.Address, err)
	}
	native.Invoke = func(ctx context.Context, invocation, input any) (any, error) {
		return InvokeContractPolicy(ctx, registration.Policy, input, func(callCtx context.Context) (any, error) {
			return registration.Invoke(callCtx, invocation, input)
		})
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractBindings == nil {
		global.contractBindings = &nativecall.Registry{}
	}
	return global.contractBindings.Register(native)
}

func InvokeContractBinding(ctx context.Context, address string, invocation, input any) (any, error) {
	return InvokeContractBindingFrom(ctx, address, "", invocation, input)
}

func InvokeContractBindingFrom(ctx context.Context, address, callerPackage string, invocation, input any) (any, error) {
	return currentInternalBindings().InvokeFrom(ctx, address, callerPackage, invocation, input)
}
