package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// RegisterWorkerOperation installs a generated, typed operation adapter in
// the worker. The Host owns its bindings and execution policy.
func RegisterWorkerOperation(address string, invoke func(context.Context, []byte) ([]byte, shared.ByteStream, error)) error {
	global.mu.Lock()
	defer global.mu.Unlock()
	if address == "" || invoke == nil || global.workerOperations[address] != nil {
		return fmt.Errorf("invalid or duplicate worker operation %q", address)
	}
	if global.workerOperations == nil {
		global.workerOperations = make(map[string]func(context.Context, []byte) ([]byte, shared.ByteStream, error))
	}
	global.workerOperations[address] = invoke
	return nil
}

// MainGoWorker runs the service on its private Worker ABI v1 listener.
func MainGoWorker(cfg AppConfig, service string) (err error) {
	launch, err := workerabi.ReadLaunch(os.Args[1:])
	if err != nil {
		return err
	}
	bundle := CurrentLinkedContractBundle()
	if launch.Identity.Service != service || launch.Identity.ContractRevision != bundle.ContractRevision ||
		launch.Identity.ImplementationRevision != bundle.ImplementationRevision {
		return fmt.Errorf("worker launch does not match linked service identity")
	}
	SetAppConfig(cfg)
	stopReporting := startDevelopmentReporting(cfg)
	defer stopReporting()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer startSupervisorParentMonitor(cancel)()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), processHostShutdownGrace)
		defer cancel()
		err = errors.Join(err, ShutdownServices(shutdown))
	}()
	if err := InitializeServices(); err != nil {
		return err
	}

	capabilities := workerabi.NewTransport(launch.HostSocket)
	defer capabilities.CloseIdleConnections()
	server := &http.Server{Protocols: workerabi.Protocols(), Handler: launch.Handler(func(ctx context.Context, invocation workerabi.Invocation) (workerabi.Result, error) {
		ctx = context.WithValue(ctx, workerCapabilitiesKey{}, &workerCapabilities{scope: invocation.Context.Scope, token: launch.Token, transport: capabilities})
		return invokeGoWorker(ctx, invocation)
	})}
	listener, err := listenRuntime("unix", launch.WorkerSocket)
	if err != nil {
		return err
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-runCtx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), processHostShutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(err, server.Close())
		}
		return nil
	}
}

// workerInvocationMetadata carries request information shared by workers in
// the ABI's optional metadata field. Authentication credentials stay at ingress.
type workerInvocationMetadata struct {
	LogsEnabled  bool               `json:"logs_enabled,omitempty"`
	ParentSpanID string             `json:"parent_span_id,omitempty"`
	Module       string             `json:"module,omitempty"`
	ServiceName  string             `json:"service_name,omitempty"`
	AuthDataKind string             `json:"auth_data_kind,omitempty"`
	AuthData     json.RawMessage    `json:"auth_data,omitempty"`
	Caller       string             `json:"caller_binding,omitempty"`
	Deployment   string             `json:"deployment,omitempty"`
	TraceID      string             `json:"trace_id,omitempty"`
	Started      time.Time          `json:"started,omitzero"`
	Type         shared.RequestType `json:"request_type,omitempty"`
	// The HTTP request admitted at ingress, as CurrentRequest reports it.
	Method     string            `json:"method,omitempty"`
	Path       string            `json:"path,omitempty"`
	PathParams []workerPathParam `json:"path_params,omitempty"`
	Headers    http.Header       `json:"headers,omitempty"`
}

// workerPathParam keeps the route order of path parameters on the wire.
type workerPathParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func invokeGoWorker(ctx context.Context, invocation workerabi.Invocation) (workerabi.Result, error) {
	global.mu.RLock()
	operation := global.workerOperations[invocation.Operation]
	global.mu.RUnlock()
	if operation == nil {
		return workerabi.Result{}, fmt.Errorf("worker operation is not registered")
	}
	ctx, state, err := goWorkerContext(ctx, invocation)
	if err != nil {
		return workerabi.Result{}, err
	}
	defer enterState(state)()
	outcome, stream, err := operation(ctx, invocation.Input)
	return workerabi.Result{Outcome: outcome, Stream: stream.Reader, StreamSize: stream.Size}, err
}

func goWorkerContext(ctx context.Context, invocation workerabi.Invocation) (context.Context, *requestState, error) {
	wire := invocation.Context
	var metadata workerInvocationMetadata
	if len(wire.Metadata) != 0 {
		if err := json.Unmarshal(wire.Metadata, &metadata); err != nil {
			return ctx, nil, err
		}
	}
	auth, err := decodeProcessLinkAuth(processLinkAuth{UID: wire.Principal, DataKind: metadata.AuthDataKind, Data: metadata.AuthData})
	if err != nil {
		return ctx, nil, err
	}
	traceID, spanID, hasTrace := parseTraceParent(wire.Traceparent)
	if !hasTrace {
		traceID = metadata.TraceID
	}
	deadline, _ := ctx.Deadline()
	if metadata.Started.IsZero() {
		metadata.Started = time.Now()
	}
	if metadata.Type == "" {
		metadata.Type = shared.InternalCall
		if invocation.Mode == "durable" {
			metadata.Type = shared.DurableCall
		}
	}
	token := runtimeapi.NewInvocationWithMetadata(runtimeapi.InvocationMetadata{
		ID: wire.InvocationID, Principal: wire.Principal, TenantID: wire.TenantID, TraceID: traceID, Deadline: deadline,
		CallerBinding: metadata.Caller, ExecutionID: wire.ExecutionID, Locale: wire.Locale, Deployment: metadata.Deployment,
	})
	state := &requestState{started: metadata.Started, auth: auth, invocation: token, processGeneration: wire.Generation, traceEnabled: hasTrace, logsEnabled: metadata.LogsEnabled,
		workerRuntime: "go", workerModule: metadata.Module}
	// Header names are case-insensitive on the wire; Go reads canonical keys.
	headers := make(http.Header, len(metadata.Headers))
	for name, values := range metadata.Headers {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	var pathParams shared.PathParams
	for _, param := range metadata.PathParams {
		pathParams = append(pathParams, shared.PathParam{Name: param.Name, Value: param.Value})
	}
	state.request = shared.Request{
		Type: metadata.Type, Started: metadata.Started, InvocationID: wire.InvocationID, TraceID: traceID, Headers: headers,
		CallerBinding: metadata.Caller, ExecutionID: wire.ExecutionID, Locale: wire.Locale, Deployment: metadata.Deployment,
		Deadline: deadline, Service: metadata.ServiceName, Endpoint: invocation.Operation,
		Method: metadata.Method, Path: metadata.Path, PathParams: pathParams,
	}
	if state.request.Service == "" {
		state.request.Service = invocation.Service
	}
	if hasTrace {
		state.trace = &traceSpan{traceID: traceID, spanID: spanID, parentSpanID: metadata.ParentSpanID, service: state.request.Service, endpoint: invocation.Operation,
			started: metadata.Started, requestType: metadata.Type, generation: wire.Generation}
	}
	return withState(runtimeapi.WithInvocation(ctx, token), state), state, nil
}
