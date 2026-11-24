package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/durable/store"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/workerabi"
)

type devDurableRequest struct {
	Target    string          `json:"target"`
	Input     json.RawMessage `json:"input,omitempty"`
	Wait      bool            `json:"wait"`
	TimeoutNS time.Duration   `json:"timeout_ns"`
}

type devDurableResult struct {
	JobID    string           `json:"job_id"`
	Target   string           `json:"target"`
	Module   string           `json:"module"`
	Kind     string           `json:"execution_kind"`
	State    string           `json:"state"`
	Attempts int              `json:"attempts"`
	Input    json.RawMessage  `json:"input"`
	Output   json.RawMessage  `json:"output,omitempty"`
	TraceID  string           `json:"trace_id"`
	TimedOut bool             `json:"timed_out"`
	Error    *devDurableError `json:"error,omitempty"`
}

type devDurableError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	ReportToken string `json:"report_token,omitempty"`
}

type devDurableTarget struct {
	address, kind string
	service       WorkerServicePlan
	execution     WorkerDurablePlan
	schedule      WorkerSchedulePlan
}

func devDurableName(address string) string {
	return address[strings.LastIndexByte(address, '/')+1:]
}

func (workers *workerHost) resolveDevDurableTarget(name string) (devDurableTarget, error) {
	var matches []devDurableTarget
	var valid []string
	for _, service := range workers.services {
		for address, execution := range service.Durable {
			alias := service.Module + "." + devDurableName(execution.Operation)
			external := execution.Registration.ExternalName
			valid = append(valid, fmt.Sprintf("%s (%s, %s)", address, alias, external))
			if name == address || name == alias || name == external || name == service.Module+"."+devDurableName(address) {
				matches = append(matches, devDurableTarget{address: address, kind: "durable", service: service, execution: execution})
			}
		}
		for _, schedule := range service.Schedules {
			alias := service.Module + "." + devDurableName(schedule.Address)
			valid = append(valid, fmt.Sprintf("%s (%s)", schedule.Address, alias))
			if name == schedule.Address || name == alias || name == devDurableName(schedule.Address) {
				matches = append(matches, devDurableTarget{address: schedule.Address, kind: "schedule", service: service, execution: service.Durable[schedule.Execution], schedule: schedule})
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		valid = valid[:0]
		for _, target := range matches {
			valid = append(valid, target.address)
		}
	}
	slices.Sort(valid)
	choices := strings.Join(valid, ", ")
	if choices == "" {
		choices = "(none)"
	}
	if len(matches) > 1 {
		return devDurableTarget{}, fmt.Errorf("ambiguous target %q; use a canonical address: %s", name, choices)
	}
	return devDurableTarget{}, fmt.Errorf("unknown target %q; valid targets: %s", name, choices)
}

func (h *processHost) serveWorkerDevDurable(w http.ResponseWriter, req *http.Request) {
	var call devDurableRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, workerabi.MaxMetadataBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || call.Target == "" || call.TimeoutNS <= 0 || call.TimeoutNS > time.Hour {
		http.Error(w, "invalid durable run request", http.StatusBadRequest)
		return
	}
	generation := h.acquire(0)
	if generation == nil {
		http.Error(w, "application generation unavailable", http.StatusServiceUnavailable)
		return
	}
	defer generation.inFlight.Add(-1)
	target, err := h.workers.resolveDevDurableTarget(call.Target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	input := call.Input
	operation := target.execution.Operation
	if target.kind == "schedule" {
		operation = target.schedule.Operation
		if input == nil {
			input = target.schedule.Input
		}
	}
	if input == nil {
		input = json.RawMessage("{}")
	}
	normalized, err := h.workers.codec(target.service.Operations[operation].Input, input)
	if err != nil {
		http.Error(w, "input does not match the declared operation: "+err.Error(), http.StatusBadRequest)
		return
	}
	result := devDurableResult{
		Target: target.address, Module: target.service.Module, Kind: target.kind,
		Input: normalized, TraceID: newTraceID(),
	}
	if target.execution.Operation == "" {
		h.workers.runDevDirectSchedule(w, req, call, target, generation, result)
		return
	}
	if err := runDevDurable(req.Context(), target, call, &result); err != nil {
		if result.JobID == "" {
			_ = writeContractTransportError(w, ContractSystemError(err))
			return
		}
		result.reportFailure("could not read the dispatched job: " + err.Error())
	}
	writeProcessHostJSON(w, http.StatusOK, result)
}

func runDevDurable(ctx context.Context, target devDurableTarget, call devDurableRequest, result *devDurableResult) error {
	ctx = withCronInvocation(ctx, time.Now().UTC(), newSpanID())
	state := stateFromContext(ctx)
	state.request.TraceID = result.TraceID
	if err := authorizeDevSchedule(target, state); err != nil {
		return err
	}
	ctx = withNewRuntimeInvocation(ctx, state)
	restore := enterState(state)
	defer restore()
	options, err := workerDurableOptions(target.execution, result.Input)
	if err != nil {
		return err
	}
	reg := target.execution.Registration
	value, err := invokeDevSchedulePolicy(ctx, target, result.Input, func(ctx context.Context) (any, error) {
		return StartDurableTask(ctx, DurableStartRequest{
			Service: reg.Service, TaskName: reg.ExternalName, Input: result.Input,
			DedupeKey: options.DedupeKey, ConcurrencyKey: options.ConcurrencyKey,
		})
	})
	if err != nil {
		return err
	}
	run, ok := value.(DurableRun)
	if !ok {
		return fmt.Errorf("durable dispatch returned an invalid receipt")
	}
	result.JobID, result.State = run.ID, run.State
	if !call.Wait {
		return nil
	}
	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[run.Service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("durable service %q is inactive", run.Service)
	}
	waitCtx, cancel := context.WithTimeout(ctx, call.TimeoutNS)
	defer cancel()
	return observeDevDurable(waitCtx, db, result)
}

func observeDevDurable(ctx context.Context, db *store.Store, result *devDurableResult) error {
	for ctx.Err() == nil {
		wake, release := db.JobWake(result.JobID)
		job, found, err := db.GetJob(ctx, result.JobID)
		if err == nil && found {
			result.applyJob(job)
			if job.State == "succeeded" || job.State == "failed" || job.State == "canceled" {
				release()
				return nil
			}
			awaitDurableWake(ctx, wake, durableWaitPoll)
		}
		release()
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("durable job %q is unavailable", result.JobID)
		}
	}
	result.timeOut()
	return nil
}

func (result *devDurableResult) applyJob(job store.JobDetail) {
	result.State, result.Attempts = job.State, job.Attempt
	if json.Valid(job.InputBlob) {
		result.Input = job.InputBlob
	}
	result.TraceID = ""
	if metadata := durableInvocationMetadataFromJSON(job.MemoJSON); metadata != nil {
		result.TraceID = metadata.TraceID
	}
	switch job.State {
	case "succeeded":
		if json.Valid(job.ResultBlob) {
			result.Output = job.ResultBlob
		}
	case "failed":
		result.reportFailure(string(job.ErrorBlob))
	case "canceled":
		result.Error = &devDurableError{Code: "SCN8003", Message: "durable execution was canceled"}
	}
}

func (result *devDurableResult) reportFailure(cause string) {
	token := "rpt_" + strings.ToLower(rand.Text())
	machine.ReportInternalFailure(token, "SCN9000", cause)
	result.Error = &devDurableError{Code: "SCN9000", Message: "execution failed", ReportToken: token}
}

func (result *devDurableResult) timeOut() {
	result.TimedOut = true
	result.Error = &devDurableError{Code: "SCN8003", Message: "timed out waiting for execution"}
}

func authorizeDevSchedule(target devDurableTarget, state *requestState) error {
	if target.kind != "schedule" {
		return nil
	}
	identity := ContractWorkloadIdentity{
		Address: "std.workload_identity.scheduler", Issuer: "std.identity_issuer.runtime",
		PrincipalType: "std.type.workload_principal", ClaimsJSON: `{"workload":"scheduler"}`,
	}
	auth, err := identity.Mint()
	if err != nil {
		return err
	}
	claims := auth.Data.(map[string]any)
	claims["authorization"], claims["pipeline"] = target.schedule.Authorization, target.schedule.Pipeline
	state.auth = auth
	return nil
}

func invokeDevSchedulePolicy(ctx context.Context, target devDurableTarget, input json.RawMessage, invoke func(context.Context) (any, error)) (any, error) {
	if target.kind != "schedule" {
		return invoke(ctx)
	}
	return InvokeContractPolicy(ctx, &target.schedule.Policy, input, invoke)
}

func (workers *workerHost) runDevDirectSchedule(w http.ResponseWriter, req *http.Request, call devDurableRequest, target devDurableTarget, generation *processHostGeneration, result devDurableResult) {
	result.JobID = "run_" + strings.ToLower(rand.Text())
	result.State = "running"
	finished := make(chan devDurableResult, 1)
	generation.inFlight.Add(1)
	go func(result devDurableResult) {
		defer generation.inFlight.Add(-1)
		ctx, cancel := context.WithTimeout(generation.lifetime, target.schedule.Timeout)
		defer cancel()
		result.Attempts = 1
		result.Output, _ = workers.executeDevSchedule(ctx, target, generation, &result)
		finished <- result
	}(result)
	if call.Wait {
		ctx, cancel := context.WithTimeout(req.Context(), call.TimeoutNS)
		defer cancel()
		select {
		case result = <-finished:
		case <-ctx.Done():
			result.timeOut()
		}
	}
	writeProcessHostJSON(w, http.StatusOK, result)
}

func (workers *workerHost) executeDevSchedule(ctx context.Context, target devDurableTarget, generation *processHostGeneration, result *devDurableResult) (output json.RawMessage, err error) {
	ctx = context.WithValue(ctx, processHostGenerationKey{}, generation)
	ctx = withCronInvocation(ctx, time.Now().UTC(), result.JobID)
	state := stateFromContext(ctx)
	state.request.TraceID = result.TraceID
	state.processGeneration = generation.number
	if err := authorizeDevSchedule(target, state); err != nil {
		result.State = "failed"
		result.reportFailure(err.Error())
		return nil, err
	}
	ctx, end := beginOperationTrace(ctx, "SCHEDULE", target.service.Name, target.address, nil)
	defer func() { end(err) }()
	state = stateFromContext(ctx)
	ctx = withNewRuntimeInvocation(ctx, state)
	restore := enterState(state)
	defer restore()
	logTrace(ctx, "starting scheduled operation")
	encoded, err := invokeDevSchedulePolicy(ctx, target, result.Input, func(ctx context.Context) (any, error) {
		operation := target.schedule.Operation
		value, err := workers.invoke(ctx, target.service, target.service.Operations[operation], operation, "", "direct", result.Input)
		if value.Stream != nil {
			return nil, closeWorkerResult(value, fmt.Errorf("scheduled operation returned a stream"))
		}
		return json.RawMessage(value.Outcome), err
	})
	if err != nil {
		result.State = "failed"
		result.reportFailure(contractDiagnosticError(err).Error())
		return nil, err
	}
	result.State = "succeeded"
	return encoded.(json.RawMessage), nil
}
