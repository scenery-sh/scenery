package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
	"scenery.sh/runtime/shared"
)

const (
	ContractCLIRequestKind            = "scenery.contract-cli.request"
	ContractCLIResponseKind           = "scenery.contract-cli.response"
	ContractCLIRequestSchemaRevision  = "sha256:95f956252d5b0a53ba551fb8c7f33ad7a928b1a3f6d858e94f0d39de69e470b1"
	ContractCLIResponseSchemaRevision = "sha256:83fdacc3279a1aec536d13b8efcae59ceb8346126857991d494a857368cf9e01"
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
	Kind           string                    `json:"kind"`
	SchemaRevision string                    `json:"schema_revision"`
	SpecRevision   string                    `json:"spec_revision"`
	Producer       machine.Producer          `json:"producer"`
	Outcome        *ContractCLIOutcome       `json:"outcome,omitempty"`
	Problem        *ContractCLISystemProblem `json:"problem,omitempty"`
	Invocation     *ContractCLIInvocation    `json:"invocation,omitempty"`
}

// ContractCLIInvocation identifies the runtime invocation that served a CLI
// request and the trace it recorded, so its spans and logs can be found.
type ContractCLIInvocation struct {
	InvocationID string `json:"invocation_id"`
	TraceID      string `json:"trace_id,omitempty"`
}

type ContractCLIRequest struct {
	Kind           string           `json:"kind"`
	SchemaRevision string           `json:"schema_revision"`
	SpecRevision   string           `json:"spec_revision"`
	Producer       machine.Producer `json:"producer"`
	Binding        string           `json:"binding"`
	Input          json.RawMessage  `json:"input"`
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
	outcome, _, err := invokeContractCLIBinding(ctx, address, input)
	return outcome, err
}

// invokeContractCLIBinding invokes a registered CLI binding as the local
// developer and reports the invocation and trace identity it ran under.
func invokeContractCLIBinding(ctx context.Context, address string, input []byte) (result ContractCLIOutcome, invocation *ContractCLIInvocation, resultErr error) {
	global.mu.RLock()
	registration := global.contractCLIBindings[address]
	global.mu.RUnlock()
	if registration.Invoke == nil {
		return ContractCLIOutcome{}, nil, &errs.Error{Code: errs.NotFound, Message: fmt.Sprintf("contract CLI binding %s is not registered", address)}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	identity, err := user.Current()
	if err != nil {
		return ContractCLIOutcome{}, nil, fmt.Errorf("resolve local developer identity: %w", err)
	}
	state := &requestState{
		started: time.Now(), request: shared.Request{Type: shared.InternalCall, CallerBinding: address},
		auth:        AuthInfo{UID: "local:" + identity.Uid, Data: map[string]any{"local_developer": true, "username": identity.Username}},
		logsEnabled: true, traceEnabled: true,
	}
	service, attributes := operationTraceIdentity(registration.Policy, "cli", nil)
	ctx, end := beginOperationTrace(withState(ctx, state), "CLI", service, address, attributes)
	defer finishOperation(end, &resultErr)
	state = stateFromContext(ctx)
	ctx = withNewRuntimeInvocation(ctx, state)
	invocation = &ContractCLIInvocation{InvocationID: state.request.InvocationID}
	if state.trace != nil {
		invocation.TraceID = state.trace.traceID
	}
	restore := enterState(state)
	defer restore()
	result, resultErr = registration.Invoke(ctx, input)
	return result, invocation, resultErr
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
	var request ContractCLIRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || validateContractCLIRequest(request) != nil {
		return fmt.Errorf("invalid contract CLI request")
	}
	return json.NewEncoder(output).Encode(serveContractCLIRequest(context.Background(), request))
}

// serveContractCLIRequest invokes a validated CLI request and answers it,
// whether the request came from a request file or a running runtime.
func serveContractCLIRequest(ctx context.Context, request ContractCLIRequest) ContractCLIResponse {
	response := ContractCLIResponse{
		Kind: ContractCLIResponseKind, SchemaRevision: ContractCLIResponseSchemaRevision,
		SpecRevision: request.SpecRevision, Producer: contractCLIRuntimeProducer(),
	}
	outcome, invocation, invokeErr := invokeContractCLIBinding(ctx, request.Binding, request.Input)
	response.Invocation = invocation
	if invokeErr == nil {
		response.Outcome = &outcome
	} else {
		code := string(errs.Code(invokeErr))
		if code == "" {
			code = "internal"
		}
		response.Problem = &ContractCLISystemProblem{Code: code, Message: invokeErr.Error()}
	}
	return response
}

func validateContractCLIRequest(request ContractCLIRequest) error {
	if request.Kind != ContractCLIRequestKind || request.SchemaRevision != ContractCLIRequestSchemaRevision || request.SpecRevision != string(spec.CurrentRevision()) {
		return fmt.Errorf("unexpected contract CLI request identity")
	}
	if request.Binding == "" || len(request.Input) == 0 || request.Producer.Version == "" || request.Producer.Toolchain.GoVersion == "" {
		return fmt.Errorf("incomplete contract CLI request")
	}
	return nil
}

func ValidateContractCLIResponse(response ContractCLIResponse, expectedSpecRevision string) error {
	if response.Kind != ContractCLIResponseKind || response.SchemaRevision != ContractCLIResponseSchemaRevision || response.SpecRevision != expectedSpecRevision {
		return fmt.Errorf("unexpected contract CLI response identity")
	}
	if response.Producer.Version == "" || response.Producer.Toolchain.GoVersion == "" {
		return fmt.Errorf("incomplete contract CLI response producer")
	}
	if (response.Outcome == nil) == (response.Problem == nil) {
		return fmt.Errorf("contract CLI response must contain exactly one outcome or problem")
	}
	return nil
}

func contractCLIRuntimeProducer() machine.Producer {
	return machine.Producer{Version: "scenery-runtime", Toolchain: machine.Toolchain{GoVersion: runtime.Version()}}
}
