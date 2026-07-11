package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

type ContractCLIInvoke func(context.Context, []byte) (ContractCLIOutcome, error)

type ContractCLIBindingRegistration struct {
	Address string
	Command []string
	Policy  *ContractHTTPPolicy
	Invoke  ContractCLIInvoke
}

type ContractCLIOutcome struct {
	Kind    string          `json:"kind"`
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

type ContractCLISystemProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ContractCLIResponse struct {
	APIVersion string                    `json:"api_version"`
	Outcome    *ContractCLIOutcome       `json:"outcome,omitempty"`
	Problem    *ContractCLISystemProblem `json:"problem,omitempty"`
}

type contractCLIRequest struct {
	APIVersion string          `json:"api_version"`
	Binding    string          `json:"binding"`
	Input      json.RawMessage `json:"input"`
}

func RegisterContractCLIBinding(registration ContractCLIBindingRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	if registration.Address == "" || len(registration.Command) == 0 || registration.Invoke == nil {
		return fmt.Errorf("contract CLI binding requires an address, command, and invoke function")
	}
	for _, segment := range registration.Command {
		if strings.TrimSpace(segment) == "" {
			return fmt.Errorf("contract CLI binding %s has an empty command segment", registration.Address)
		}
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("contract CLI binding %s policy: %w", registration.Address, err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractCLIBindings == nil {
		global.contractCLIBindings = map[string]ContractCLIBindingRegistration{}
	}
	if _, exists := global.contractCLIBindings[registration.Address]; exists {
		return fmt.Errorf("duplicate contract CLI binding %s", registration.Address)
	}
	global.contractCLIBindings[registration.Address] = registration
	return nil
}

func InvokeContractCLIBinding(ctx context.Context, address string, input []byte) (ContractCLIOutcome, error) {
	global.mu.RLock()
	registration := global.contractCLIBindings[address]
	global.mu.RUnlock()
	if registration.Invoke == nil {
		return ContractCLIOutcome{}, fmt.Errorf("contract CLI binding %s is not registered", address)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := user.Current()
	if err != nil {
		return ContractCLIOutcome{}, fmt.Errorf("resolve local developer identity: %w", err)
	}
	state := &requestState{
		started: time.Now(), request: shared.Request{Type: shared.InternalCall, CallerBinding: address},
		auth:        AuthInfo{UID: "local:" + identity.Uid, Data: map[string]any{"local_developer": true, "username": identity.Username}},
		logsEnabled: true, traceEnabled: true,
	}
	ctx = withRuntimeInvocation(withState(ctx, state), state)
	restore := enterState(state)
	defer restore()
	return registration.Invoke(ctx, input)
}

func ContractCLIInvalidInput(err error) error {
	if err == nil {
		return errs.B().Code(errs.InvalidArgument).Msg("invalid CLI input").Err()
	}
	return errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
}

func ExecuteContractCLIRequest(path string, output io.Writer) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return fmt.Errorf("invalid contract CLI request file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var request contractCLIRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.APIVersion != "scenery.contract-cli-request/v1" || request.Binding == "" || len(request.Input) == 0 {
		return fmt.Errorf("invalid contract CLI request")
	}
	response := ContractCLIResponse{APIVersion: "scenery.contract-cli-response/v1"}
	outcome, invokeErr := InvokeContractCLIBinding(context.Background(), request.Binding, request.Input)
	if invokeErr == nil {
		response.Outcome = &outcome
	} else {
		code := string(errs.Code(invokeErr))
		if code == "" {
			code = "internal"
		}
		response.Problem = &ContractCLISystemProblem{Code: code, Message: invokeErr.Error()}
	}
	return json.NewEncoder(output).Encode(response)
}
