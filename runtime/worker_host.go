package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/workerabi"
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
}

func (h *processHost) registerWorkerServices(plans []WorkerServicePlan, codec WorkerValueCodec, snapshots map[string]*ConfigSnapshot) error {
	if len(plans) == 0 {
		return nil
	}
	if codec == nil {
		return fmt.Errorf("worker host requires the manifest value codec")
	}
	workers := &workerHost{host: h, services: make(map[string]WorkerServicePlan, len(plans)), codec: codec, config: snapshots}
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
		for _, binding := range plan.Bindings {
			if err := workers.registerBinding(plan, binding); err != nil {
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
	normalize := func(input []byte) ([]byte, error) { return workers.codec(operation.Input, input) }
	invoke := func(ctx context.Context, input any) (workerabi.Result, error) {
		raw, ok := input.(json.RawMessage)
		if !ok {
			return workerabi.Result{}, ContractSystemError(fmt.Errorf("worker binding input is not canonical JSON"))
		}
		if binding.Delivery == "enqueue" || binding.Delivery == "wait" {
			return workers.dispatchDurable(ctx, plan.Durable[binding.Execution], binding.Delivery, raw)
		}
		return workers.invoke(ctx, plan, operation, binding.Operation, binding.Address, "direct", raw)
	}
	if httpPlan := binding.HTTP; httpPlan != nil {
		return RegisterEndpointChecked(&Endpoint{
			Service: plan.Name, Name: binding.Address, Access: httpPlan.Access, Path: httpPlan.Path, Methods: []string{httpPlan.Method},
			ContractPolicy: &binding.Policy, ContractPathTail: httpPlan.PathTail,
			DecodeContractRequest: func(request *http.Request, values map[string]string) (ContractDecodedRequest, error) {
				return decodeWorkerRequest(request, values, httpPlan.Request, normalize)
			},
			Invoke: func(ctx context.Context, _ []any, input any) (any, error) { return invoke(ctx, input) },
			EncodeContractOutcome: func(request *http.Request, output any) (ContractHTTPResponse, error) {
				result, ok := output.(workerabi.Result)
				if !ok {
					return ContractHTTPResponse{}, ContractSystemError(fmt.Errorf("worker binding has no outcome"))
				}
				response, err := encodeWorkerResponse(request, result, httpPlan.Responses)
				if err == nil && result.ProcessID != 0 {
					if response.Headers == nil {
						response.Headers = make(http.Header)
					}
					response.Headers.Set("X-App-Worker-Pid", strconv.Itoa(result.ProcessID))
				}
				return response, err
			},
		})
	}
	return RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: binding.Address, Visibility: binding.Visibility, Package: plan.Module, Policy: &binding.Policy,
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

func (workers *workerHost) invoke(ctx context.Context, plan WorkerServicePlan, operation WorkerOperationPlan, address, binding, mode string, input json.RawMessage) (workerabi.Result, error) {
	generation, _ := ctx.Value(processHostGenerationKey{}).(*processHostGeneration)
	releaseGeneration := func() {}
	if generation == nil {
		generation = workers.host.acquire(0)
		if generation == nil {
			return workerabi.Result{}, workerUnavailable(workerabi.NotSent, fmt.Errorf("worker generation is not published"))
		}
		releaseGeneration = func() { generation.inFlight.Add(-1) }
		ctx = context.WithValue(ctx, processHostGenerationKey{}, generation)
	}
	ctx, cancelInvocation := context.WithCancel(ctx)
	stopRetirement := context.AfterFunc(generation.lifetime, cancelInvocation)
	instance := generation.instances[plan.Process]
	if instance == nil || instance.worker == nil {
		stopRetirement()
		cancelInvocation()
		releaseGeneration()
		return workerabi.Result{}, workerUnavailable(workerabi.NotSent, fmt.Errorf("worker instance is unavailable"))
	}
	current, ok := runtimeapi.InvocationFromContext(ctx)
	if !ok {
		stopRetirement()
		cancelInvocation()
		releaseGeneration()
		return workerabi.Result{}, ContractSystemError(fmt.Errorf("worker call has no runtime invocation"))
	}
	metadata := workerabi.InvocationContext{
		InvocationID: current.ID(), Generation: generation.number, ContractRevision: instance.spec.Identity.ContractRevision,
		Principal: current.Principal(), TenantID: current.TenantID(), Locale: current.Locale(), ExecutionID: current.ExecutionID(),
	}
	if deadline, ok := ctx.Deadline(); ok {
		metadata.Deadline = &deadline
	}
	if state := stateFromContext(ctx); state != nil {
		cloned := *state
		cloned.processGeneration = generation.number
		ctx = withState(ctx, &cloned)
		if trace := state.trace; trace != nil {
			metadata.Traceparent = fmt.Sprintf("00-%s-%s-01", trace.traceID, trace.spanID)
		}
	}
	scope, revoke := workers.scopes.Open(ctx, plan.Address, instance.spec.WorkerToken)
	metadata.Scope = scope
	release := func() {
		stopRetirement()
		cancelInvocation()
		revoke()
		releaseGeneration()
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
	if err := workers.validateOutcome(operation, &result); err != nil {
		release()
		return workerabi.Result{}, ContractSystemError(closeWorkerResult(result, err))
	}
	if result.Stream == nil {
		release()
	} else {
		result.Stream = &workerStream{ReadCloser: result.Stream, release: release}
	}
	result.ProcessID = instance.spec.PID
	return result, nil
}

func (workers *workerHost) validateOutcome(operation WorkerOperationPlan, result *workerabi.Result) error {
	kind, name, raw, err := contract.DecodeContractOutcomeEnvelope(result.Outcome)
	if err != nil {
		return err
	}
	expression, ok := operation.Outcomes[kind+"."+name]
	if !ok {
		return fmt.Errorf("worker returned an undeclared outcome")
	}
	payload, err := workers.codec(expression, raw)
	if err != nil {
		return err
	}
	result.Outcome, err = contract.EncodeManifestOutcome(kind, name, payload)
	return err
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
		if request.Method != http.MethodPost {
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
	default:
		http.NotFound(w, request)
	}
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
	output, err := InvokeContractBindingJSON(request.Context(), clientPlan.Binding, plan.Module, fields["input"])
	if err != nil {
		writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Output: output})
}
