package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
)

// serveWorkerDevCall is only reachable on the authenticated private process
// link of a development worker Host.
func (h *processHost) serveWorkerDevCall(w http.ResponseWriter, req *http.Request) {
	req.Header.Del("Authorization")
	if bearer := req.Header.Get("X-App-Invocation-Authorization"); bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	req.Header.Del("X-App-Invocation-Authorization")
	query := req.URL.Query()
	if value := query.Get("generation"); value != "" {
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil || number == 0 {
			http.Error(w, "invalid application generation", http.StatusBadRequest)
			return
		}
		generation := h.acquire(number)
		if generation == nil {
			http.Error(w, "application generation was retired", http.StatusConflict)
			return
		}
		defer generation.inFlight.Add(-1)
		req = req.WithContext(context.WithValue(req.Context(), processHostGenerationKey{}, generation))
	}
	if address := query.Get("binding"); address != "" {
		h.serveWorkerDevBinding(w, req, address, query.Get("module"))
		return
	}
	if h.ingressDisabled {
		errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msg("HTTP ingress is disabled in worker role").Err(), http.StatusServiceUnavailable)
		return
	}
	method := query.Get("method")
	route, err := url.ParseRequestURI(query.Get("path"))
	if err != nil || route.IsAbs() || route.Host != "" || !strings.HasPrefix(route.Path, "/") {
		http.Error(w, "invalid application route", http.StatusBadRequest)
		return
	}
	req.Method, req.URL, req.RequestURI = method, route, route.RequestURI()
	neutralWorkerIngress(http.HandlerFunc(h.serveIngress), true).ServeHTTP(w, req)
}

func (h *processHost) serveWorkerDevBinding(w http.ResponseWriter, req *http.Request, address, module string) {
	generation := h.acquireIngress(req)
	if generation == nil {
		http.Error(w, "application generation unavailable", http.StatusServiceUnavailable)
		return
	}
	defer generation.inFlight.Add(-1)
	global.mu.RLock()
	registration := global.contractBindings[address]
	global.mu.RUnlock()
	if registration.Invoke != nil {
		module = registration.Package
	} else if generation.bindings[address] == "" || module == "" {
		http.NotFound(w, req)
		return
	}
	ctx := context.WithValue(req.Context(), processHostGenerationKey{}, generation)
	ctx = context.WithValue(ctx, processGenerationKey{}, generation.number)
	endpoint := &Endpoint{Service: module, Name: address, Access: Private, ContractPolicy: registration.Policy, workerRuntime: registration.workerRuntime, workerModule: registration.Package}
	if req.Header.Get("Authorization") != "" {
		endpoint.Access = Auth
	}
	if registration.Policy != nil && registration.Policy.TotalInvocationTimeoutNanos > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(registration.Policy.TotalInvocationTimeoutNanos))
		defer cancel()
	}
	state := newExternalState(endpoint, req.WithContext(ctx), nil, nil, AuthInfo{})
	ctx = withState(ctx, state)
	restore := enterState(state)
	defer restore()
	startRequestTrace(state)
	applyTraceIDHeader(w.Header(), state)
	w.Header().Set("X-App-Generation", strconv.FormatUint(generation.number, 10))
	w.Header().Set("X-App-Host-Pid", strconv.Itoa(os.Getpid()))
	info, err := authenticateRequest(req.WithContext(ctx), endpoint)
	if err != nil {
		writeAuthenticationFailure(w, state, endpoint, err)
		return
	}
	state.auth = info
	ctx = withRuntimeInvocation(ctx, state)
	logRequestStart(state)
	status := http.StatusOK
	defer func() { finishRequestTrace(state, status, err) }()
	input, err := io.ReadAll(http.MaxBytesReader(w, req.Body, workerabi.MaxMetadataBytes))
	if err != nil {
		status = http.StatusBadRequest
		http.Error(w, "invalid binding input", status)
		return
	}
	if !json.Valid(input) {
		err = errors.New("invalid binding input")
		status = http.StatusBadRequest
		http.Error(w, "invalid binding input", status)
		return
	}
	encoded, err := InvokeContractBindingJSON(ctx, address, module, input)
	if err == nil {
		if kind, name, _, decodeErr := contract.DecodeContractOutcomeEnvelope(encoded); decodeErr == nil && kind == "error" {
			err = traceCodedError(name)
		}
		if pid := h.devBindingWorkerPID(generation, address, module); pid != 0 {
			w.Header().Set("X-App-Worker-Pid", strconv.Itoa(pid))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
		return
	}
	err = classifyContractFailure(err)
	status = errs.HTTPStatus(err)
	if transportStatus, ok := contractTransportHTTPStatus(err); ok {
		status = transportStatus
	}
	if !writeContractTransportError(w, err) {
		errs.HTTPErrorWithCode(w, err, status)
	}
}

func (h *processHost) devBindingWorkerPID(generation *processHostGeneration, address, module string) int {
	if owner := generation.instances[generation.bindings[address]]; owner != nil {
		return owner.spec.PID
	}
	if h.workers != nil {
		for _, instance := range generation.instances {
			if plan := h.workers.services[instance.spec.WorkerService]; plan.Module == module {
				return instance.spec.PID
			}
		}
	}
	return 0
}
