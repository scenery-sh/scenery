package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// WorkerValueCodec validates and canonicalizes a value against the manifest's
// type expression. The same codec protects ingress, internal calls and replies.
type WorkerValueCodec func(expression string, value []byte) ([]byte, error)

type workerHost struct {
	host     *processHost
	services map[string]WorkerServicePlan
	scopes   workerabi.Scopes
	codec    WorkerValueCodec
	config   map[string]*ConfigSnapshot
	storage  WorkerStorageHandler
}

func (h *processHost) registerWorkerServices(plans []WorkerServicePlan, codec WorkerValueCodec, snapshots map[string]*ConfigSnapshot, storage WorkerStorageHandler) error {
	if len(plans) == 0 {
		return nil
	}
	if codec == nil {
		return fmt.Errorf("worker host requires the manifest value codec")
	}
	workers := &workerHost{host: h, services: make(map[string]WorkerServicePlan, len(plans)), codec: codec, config: snapshots, storage: storage}
	for _, plan := range plans {
		if plan.Address == "" || plan.Process == "" || plan.Module == "" || plan.Name == "" {
			return fmt.Errorf("worker host contains an incomplete service plan")
		}
		if _, exists := workers.services[plan.Address]; exists {
			return fmt.Errorf("worker host repeats service %s", plan.Address)
		}
		workers.services[plan.Address] = plan
		h.required = append(h.required, plan.Process)
		for _, execution := range plan.Durable {
			if err := workers.registerDurable(plan, execution); err != nil {
				return err
			}
		}
		for _, schedule := range plan.Schedules {
			if err := workers.registerSchedule(plan, schedule); err != nil {
				return err
			}
		}
		for _, binding := range plan.Bindings {
			if err := workers.registerBinding(plan, binding); err != nil {
				return err
			}
		}
		for _, tool := range plan.MCPTools {
			if err := workers.registerMCPTool(plan, tool); err != nil {
				return err
			}
		}
	}
	h.workers = workers
	return nil
}

func (workers *workerHost) registerBinding(plan WorkerServicePlan, binding WorkerBindingPlan) error {
	operation, exists := plan.Operations[binding.Operation]
	if !exists || operation.Input == "" || binding.Address == "" {
		return fmt.Errorf("worker service %s has an incomplete binding %s", plan.Address, binding.Address)
	}
	if binding.Protocol == "mcp" {
		return nil
	}
	normalize := func(input []byte) ([]byte, error) { return workers.codec(operation.Input, input) }
	invoke := func(ctx context.Context, input any) (workerabi.Result, error) {
		raw, ok := input.(json.RawMessage)
		if !ok {
			return workerabi.Result{}, ContractSystemError(fmt.Errorf("worker binding input is not canonical JSON"))
		}
		if binding.Delivery == "enqueue" || binding.Delivery == "wait" {
			return workers.dispatchDurable(ctx, plan, plan.Durable[binding.Execution], binding.Delivery, raw)
		}
		return workers.invoke(ctx, plan, operation, binding.Operation, binding.Address, "direct", raw)
	}
	if httpPlan := binding.HTTP; httpPlan != nil {
		return RegisterEndpointChecked(&Endpoint{
			workerRuntime: plan.Runtime, workerModule: plan.Module,
			Service: plan.Name, Name: binding.Address, Access: httpPlan.Access, Path: httpPlan.Path, Methods: []string{httpPlan.Method},
			ContractPolicy: &binding.Policy, ContractPathTail: httpPlan.PathTail,
			DecodeContractRequest: func(request *http.Request, values map[string]string) (ContractDecodedRequest, error) {
				return decodeWorkerRequest(request, values, httpPlan.Request, normalize)
			},
			Invoke: func(ctx context.Context, _ []any, input any) (any, error) {
				result, err := invoke(ctx, input)
				if err != nil {
					switch outcome := ContractDurableFailureOutcome(err); outcome {
					case "dispatch.rejected":
						status := http.StatusServiceUnavailable
						if configured := binding.Policy.TransportStatuses[outcome]; configured != 0 {
							status = configured
						}
						return nil, &ContractTransportError{Outcome: outcome, Status: status, Message: "durable dispatch rejected", Cause: err}
					case "dispatch.wait_timeout":
						status := http.StatusGatewayTimeout
						if configured := binding.Policy.TransportStatuses[outcome]; configured != 0 {
							status = configured
						}
						return nil, &ContractTransportError{Outcome: outcome, Status: status, Message: "durable wait timed out", Cause: err}
					default:
						return nil, err
					}
				}
				return result, nil
			},
			EncodeContractOutcome: func(request *http.Request, output any) (ContractHTTPResponse, error) {
				result, ok := output.(workerabi.Result)
				if !ok {
					return ContractHTTPResponse{}, ContractSystemError(fmt.Errorf("worker binding has no outcome"))
				}
				response, err := encodeWorkerResponse(request, result, httpPlan.Responses)
				if err == nil && result.ProcessID != 0 && workers.host != nil && workers.host.developmentCall {
					if response.Headers == nil {
						response.Headers = make(http.Header)
					}
					response.Headers.Set("X-App-Worker-Pid", strconv.Itoa(result.ProcessID))
					response.Headers.Set("X-App-Worker-Revision", result.ImplementationRevision)
				}
				return response, err
			},
		})
	}
	return RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		workerRuntime: plan.Runtime, Address: binding.Address, Visibility: binding.Visibility, Package: plan.Module, Policy: &binding.Policy,
		DecodeInput: func(data []byte) (any, error) {
			canonical, err := normalize(data)
			return json.RawMessage(canonical), err
		},
		Invoke: func(ctx context.Context, _, input any) (any, error) {
			result, err := invoke(ctx, input)
			if err != nil {
				return nil, err
			}
			if result.Stream != nil {
				return nil, ContractSystemError(closeWorkerResult(result, fmt.Errorf("internal worker binding returned a stream")))
			}
			if binding.Delivery == "enqueue" {
				_, _, receipt, err := contract.DecodeContractOutcomeEnvelope(result.Outcome)
				return receipt, err
			}
			return result.Outcome, nil
		},
		EncodeOutput: func(value any) ([]byte, error) {
			if output, ok := value.(json.RawMessage); ok {
				return output, nil
			}
			return nil, fmt.Errorf("worker binding has no canonical outcome")
		},
	})
}

func (workers *workerHost) invoke(ctx context.Context, plan WorkerServicePlan, operation WorkerOperationPlan, address, binding, mode string, input json.RawMessage) (output workerabi.Result, returnErr error) {
	generation, _ := ctx.Value(processHostGenerationKey{}).(*processHostGeneration)
	releaseGeneration := func() {}
	if generation == nil {
		generation = workers.host.acquire(0)
		if generation != nil {
			releaseGeneration = func() { generation.inFlight.Add(-1) }
			ctx = context.WithValue(ctx, processHostGenerationKey{}, generation)
		}
	}
	if state := stateFromContext(ctx); state != nil {
		cloned := *state
		cloned.workerRuntime, cloned.workerModule = plan.Runtime, plan.Module
		if generation != nil {
			cloned.processGeneration = generation.number
		}
		ctx = withState(ctx, &cloned)
	}
	ctx, end := beginOperationTrace(ctx, "WORKER", plan.Name, address, map[string]any{"binding_address": binding, "mode": mode})
	var outcomeErr error
	defer func() {
		if value := recover(); value != nil {
			end(errors.New("worker dispatch panicked"))
			panic(value)
		}
		if output.Stream == nil {
			traceErr := returnErr
			if traceErr == nil {
				traceErr = outcomeErr
			}
			end(traceErr)
		}
	}()
	if generation == nil {
		return workerabi.Result{}, workerUnavailable(workerabi.NotSent, fmt.Errorf("worker generation is not published"))
	}
	ctx, cancelInvocation := context.WithCancel(ctx)
	stopRetirement := context.AfterFunc(generation.lifetime, cancelInvocation)
	releaseInvocation := func() {
		stopRetirement()
		cancelInvocation()
		releaseGeneration()
	}
	instance := generation.instances[plan.Process]
	if instance == nil || instance.worker == nil {
		releaseInvocation()
		return workerabi.Result{}, workerUnavailable(workerabi.NotSent, fmt.Errorf("worker instance is unavailable"))
	}
	current, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok {
		releaseInvocation()
		return workerabi.Result{}, ContractSystemError(fmt.Errorf("worker call has no runtime invocation"))
	}
	metadata := workerabi.InvocationContext{
		InvocationID: current.ID(), Generation: generation.number, ContractRevision: instance.spec.Identity.ContractRevision,
		Principal: current.Principal(), TenantID: current.TenantID(), Locale: current.Locale(), ExecutionID: current.ExecutionID(),
	}
	if deadline, ok := ctx.Deadline(); ok {
		metadata.Deadline = &deadline
	}
	extra := workerInvocationMetadata{Caller: current.CallerBinding(), Deployment: current.Deployment()}
	if state := stateFromContext(ctx); state != nil {
		auth, err := encodeProcessLinkAuth(state.auth)
		if err != nil {
			releaseInvocation()
			return workerabi.Result{}, ContractSystemError(err)
		}
		extra.AuthDataKind, extra.AuthData = auth.DataKind, auth.Data
		extra.Started, extra.Type = state.started, state.request.Type
		extra.LogsEnabled, extra.Module, extra.ServiceName = state.logsEnabled, plan.Module, plan.Name
		extra.Method, extra.Path = state.request.Method, state.request.Path
		extra.PathParams, extra.Headers = workerPathParams(state.request.PathParams), workerRequestHeaders(state.request.Headers)
		if state.request.API != nil {
			extra.API = &workerAPIDesc{Exposed: state.request.API.Exposed, AuthRequired: state.request.API.AuthRequired}
		}
		extra.CronIdempotencyKey = state.request.CronIdempotencyKey
		if trace := state.trace; trace != nil {
			metadata.Traceparent = traceParentForContext(ctx)
			extra.ParentSpanID = trace.parentSpanID
		}
	}
	if extra.API == nil {
		extra.API = &workerAPIDesc{Exposed: false, AuthRequired: current.Principal() != ""}
	}
	if metadata.Traceparent == "" {
		extra.TraceID = current.TraceID()
	}
	var err error
	metadata.Metadata, err = json.Marshal(extra)
	if err != nil {
		releaseInvocation()
		return workerabi.Result{}, ContractSystemError(err)
	}
	scope, revoke := workers.scopes.Open(ctx, plan.Address, instance.spec.WorkerToken)
	metadata.Scope = scope
	release := func() {
		releaseInvocation()
		revoke()
	}
	result, err := instance.worker.Invoke(ctx, workerabi.Invocation{
		Service: plan.Address, Operation: address, Binding: binding, Mode: mode, Context: metadata, Input: input,
	})
	if err != nil {
		release()
		if delivery, ok := errors.AsType[*workerabi.DeliveryError](err); ok {
			return workerabi.Result{}, workerUnavailable(delivery.Delivery, err)
		}
		if errors.Is(err, workerabi.ErrImplementation) {
			return workerabi.Result{}, ContractSystemError(err)
		}
		return workerabi.Result{}, err
	}
	kind, name, err := workers.validateOutcome(operation, &result)
	if err != nil {
		release()
		return workerabi.Result{}, ContractSystemError(closeWorkerResult(result, err))
	}
	if kind == "error" {
		outcomeErr = traceCodedError(name)
	}
	if result.Stream == nil {
		release()
	} else {
		body := appsdk.ObserveReadCloser(ctx, result.Stream, func(_ int64, err error) {
			end(errors.Join(outcomeErr, err))
		})
		result.Stream = &workerStream{ReadCloser: body, release: release}
	}
	result.ProcessID = instance.spec.PID
	result.ImplementationRevision = instance.spec.Identity.ImplementationRevision
	return result, nil
}

func workerPathParams(params shared.PathParams) []workerPathParam {
	wire := make([]workerPathParam, 0, len(params))
	for _, param := range params {
		wire = append(wire, workerPathParam{Name: param.Name, Value: param.Value})
	}
	return wire
}

// workerRequestHeaders returns the ingress headers a worker may observe. The
// Host has already consumed the credentials; they do not reach a worker.
func workerRequestHeaders(headers http.Header) http.Header {
	forwarded := make(http.Header, len(headers))
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		switch {
		case canonical == "Authorization" || canonical == "Proxy-Authorization" || canonical == "Cookie":
		case strings.HasPrefix(strings.ToLower(name), "x-scenery-"):
		default:
			forwarded[name] = values
		}
	}
	return forwarded
}

func (workers *workerHost) validateOutcome(operation WorkerOperationPlan, result *workerabi.Result) (string, string, error) {
	kind, name, raw, err := contract.DecodeContractOutcomeEnvelope(result.Outcome)
	if err != nil {
		return "", "", err
	}
	expression, ok := operation.Outcomes[kind+"."+name]
	if !ok {
		return "", "", fmt.Errorf("worker returned an undeclared outcome")
	}
	payload, err := workers.codec(expression, raw)
	if err != nil {
		return "", "", err
	}
	result.Outcome, err = contract.EncodeManifestOutcome(kind, name, payload)
	return kind, name, err
}

func workerUnavailable(delivery string, cause error) error {
	return &errs.Error{Code: errs.Unavailable, Message: "service is unavailable", Meta: errs.Metadata{"delivery": delivery}, Cause: cause}
}

type workerStream struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (stream *workerStream) Close() error {
	err := stream.ReadCloser.Close()
	stream.once.Do(stream.release)
	return err
}

func (workers *workerHost) serveCapability(service string, w http.ResponseWriter, request *http.Request) {
	plan := workers.services[service]
	switch request.URL.Path {
	case workerabi.CapabilityPrefix + "binding/invoke":
		workers.serveBindingCapability(plan, w, request, false)
	case workerabi.CapabilityPrefix + "durable/enqueue":
		workers.serveBindingCapability(plan, w, request, true)
	case workerabi.CapabilityPrefix + "config/snapshot":
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		snapshot := workers.config[service]
		if snapshot == nil {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeProcessHostJSON(w, http.StatusOK, snapshot)
	case workerabi.CapabilityPrefix + "storage/read", workerabi.CapabilityPrefix + "storage/write",
		workerabi.CapabilityPrefix + "storage/delete", workerabi.CapabilityPrefix + "storage/list":
		name := request.URL.Query()["store"]
		if workers.storage == nil || len(name) != 1 || plan.Stores[name[0]] == "" {
			http.Error(w, "storage handle is unavailable", http.StatusForbidden)
			return
		}
		workers.storage(plan.Stores[name[0]], w, request)
	case workerabi.CapabilityPrefix + "connections/token":
		workers.serveConnectionToken(w, request)
	case workerabi.CapabilityPrefix + "telemetry":
		serveWorkerTelemetry(plan, w, request)
	default:
		http.NotFound(w, request)
	}
}

func (workers *workerHost) serveConnectionToken(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	input := struct {
		Provider string   `json:"provider"`
		Scopes   []string `json:"scopes"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Provider) == "" || decoder.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "invalid connection request", http.StatusBadRequest)
		return
	}
	invocation, _ := runtimeapi.InvocationFromContext(request.Context())
	if invocation.Principal() == "" {
		http.Error(w, "connection requires an authenticated invocation", http.StatusForbidden)
		return
	}
	token, err := (sdkHost{}).ConnectionToken(request.Context(), invocation.Principal(), input.Provider, input.Scopes)
	if err != nil {
		errs.HTTPError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeProcessHostJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (workers *workerHost) serveBindingCapability(plan WorkerServicePlan, w http.ResponseWriter, request *http.Request, enqueue bool) {
	if request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, workerabi.MaxMetadataBytes))
	if err != nil {
		http.Error(w, "could not read capability request", http.StatusBadRequest)
		return
	}
	fields, err := contract.DecodeJSONObject(body)
	var client string
	if err != nil || len(fields) != 2 || len(fields["input"]) == 0 || json.Unmarshal(fields["client"], &client) != nil {
		http.Error(w, "invalid capability request", http.StatusBadRequest)
		return
	}
	clientPlan := plan.Clients[client]
	if clientPlan.Binding == "" || clientPlan.Enqueue != enqueue {
		http.Error(w, "client is unavailable", http.StatusForbidden)
		return
	}
	global.mu.RLock()
	registration := global.contractBindings[clientPlan.Binding]
	global.mu.RUnlock()
	if registration.Invoke == nil {
		http.Error(w, "client binding is unavailable", http.StatusForbidden)
		return
	}
	if output, err := invokeRegisteredContractBindingJSON(request.Context(), registration, clientPlan.Binding, plan.Module, fields["input"]); err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(err)})
		return
	} else {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: output})
	}
}

func (workers *workerHost) registerMCPTool(plan WorkerServicePlan, tool WorkerMCPToolPlan) error {
	operation, exists := plan.Operations[tool.Operation]
	if !exists || operation.Input == "" || tool.ID == "" {
		return fmt.Errorf("worker service %s has an incomplete MCP tool %s", plan.Address, tool.Name)
	}
	policy := tool.Policy
	return RegisterMCPTool(MCPToolRegistration{
		ID:               tool.ID,
		Name:             tool.Name,
		AssistantAddress: tool.AssistantAddress,
		OperationAddress: tool.Operation,
		ExecutionAddress: tool.Execution,
		Policy:           &policy,
		Limits:           tool.Limits,
		Effect:           tool.Effect,
		Approval:         tool.Approval,
		DecodeInput: func(data []byte) (any, error) {
			canonical, err := workers.codec(operation.Input, data)
			return json.RawMessage(canonical), err
		},
		EncodeOutput: func(value any) ([]byte, error) {
			output, ok := value.(json.RawMessage)
			if !ok {
				return nil, fmt.Errorf("worker MCP tool has no canonical outcome")
			}
			return output, nil
		},
		Invoke: func(ctx context.Context, _ MCPToolCallContext, input any) (any, error) {
			raw, ok := input.(json.RawMessage)
			if !ok {
				return nil, ContractSystemError(fmt.Errorf("worker MCP input is not canonical JSON"))
			}
			if durable, isDurable := plan.Durable[tool.Execution]; isDurable {
				mode := "enqueue"
				if tool.Delivery == "wait" {
					mode = "wait"
				}
				result, err := workers.dispatchDurable(ctx, plan, durable, mode, raw)
				if err != nil {
					return nil, err
				}
				return result.Outcome, nil
			}
			result, err := workers.invoke(ctx, plan, operation, tool.Operation, tool.Binding, "direct", raw)
			if err != nil {
				return nil, err
			}
			if result.Stream != nil {
				return bufferWorkerMCPStream(result, tool)
			}
			return result.Outcome, nil
		},
	})
}

func bufferWorkerMCPStream(result workerabi.Result, tool WorkerMCPToolPlan) (json.RawMessage, error) {
	_, maxResult := normalizeMCPToolLimits(tool.Limits)
	kind, name, payload, err := contract.DecodeContractOutcomeEnvelope(result.Outcome)
	if err != nil {
		return nil, ContractSystemError(closeWorkerResult(result, err))
	}
	path, ok := tool.StreamFields[kind+"."+name]
	if !ok {
		return nil, ContractSystemError(closeWorkerResult(result, fmt.Errorf("worker MCP tool returned an unmapped byte stream")))
	}
	buffered, err := BufferContractByteStream(shared.NewByteStream(result.Stream, result.StreamSize), maxResult)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	updated, err := setWorkerJSONBytesAtPath(payload, path, buffered)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	outcome, err := contract.EncodeManifestOutcome(kind, name, updated)
	if err != nil {
		return nil, ContractSystemError(err)
	}
	return outcome, nil
}

func setWorkerJSONBytesAtPath(payload json.RawMessage, path []string, buffered []byte) (json.RawMessage, error) {
	encoded, err := json.Marshal(buffered)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		if len(payload) != 0 && string(payload) != "null" {
			var existing []byte
			if err := json.Unmarshal(payload, &existing); err != nil || len(existing) != 0 {
				return nil, fmt.Errorf("stream handler returned buffered bytes")
			}
		}
		return encoded, nil
	}
	object, err := contract.DecodeJSONObject(payload)
	if err != nil {
		return nil, err
	}
	key := path[0]
	if len(path) == 1 {
		if raw := object[key]; len(raw) != 0 && string(raw) != "null" {
			var existing []byte
			if err := json.Unmarshal(raw, &existing); err != nil || len(existing) != 0 {
				return nil, fmt.Errorf("stream handler returned buffered bytes")
			}
		}
		object[key] = encoded
	} else {
		updated, err := setWorkerJSONBytesAtPath(object[key], path[1:], buffered)
		if err != nil {
			return nil, err
		}
		object[key] = updated
	}
	return json.Marshal(object)
}
