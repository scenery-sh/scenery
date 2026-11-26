package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
)

func TestWorkerHostRunsDeclaredSchedule(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	const process = "pricing"
	const operation = "pricing/operation/refresh"
	identity := workerabi.Identity{ABI: workerabi.Version, Service: "pricing/service/api", ContractRevision: "contract", ImplementationRevision: "build"}
	service := WorkerServicePlan{
		Address: identity.Service, Module: "pricing", Name: "api", Process: process,
		Operations: map[string]WorkerOperationPlan{
			operation: {Input: "std.type.unit", Outcomes: map[string]string{"result.ok": "std.type.unit"}},
		},
		Schedules: []WorkerSchedulePlan{{
			Address: "pricing/schedule/refresh", Operation: operation, Input: json.RawMessage("{}"),
			Every: time.Second, Overlap: "queue", CatchupMaximumAge: time.Minute,
			Authorization: "std.authorization.scheduled", Pipeline: "std.pipeline.empty",
			Policy: ContractHTTPPolicy{AuthorizationStrategy: "public"},
		}},
	}
	host := &processHost{}
	if err := host.registerWorkerServices([]WorkerServicePlan{service}, contract.ManifestTypes(nil).Normalize, nil, nil); err != nil {
		t.Fatal(err)
	}
	jobs := listCronJobs()
	if len(jobs) != 1 || jobs[0].Every != time.Second || jobs[0].OverlapPolicy != "buffer_all" || jobs[0].CatchupWindow != time.Minute {
		t.Fatalf("unexpected registered schedules: %+v", jobs)
	}

	directory, err := os.MkdirTemp("", "abi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "w")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	calls := make(chan workerabi.Invocation, 1)
	launch := workerabi.Launch{Identity: identity, Token: "test-token"}
	server := &http.Server{
		Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second,
		Handler: launch.Handler(func(_ context.Context, call workerabi.Invocation) (workerabi.Result, error) {
			calls <- call
			return workerabi.Result{Outcome: json.RawMessage(`{"kind":"result","name":"ok","value":{}}`)}, nil
		}),
	}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	client := workerabi.NewClient(socket, launch.Token, identity)
	defer client.Close()
	host.current = &processHostGeneration{
		number: 1, lifetime: t.Context(),
		instances: map[string]*processHostInstance{
			process: {worker: client, spec: processGenerationInstance{
				Identity: processInstanceIdentity{ContractRevision: identity.ContractRevision},
			}},
		},
	}
	setActiveProcessHost(host)
	defer setActiveProcessHost(nil)

	// Let the production scheduler run a single due tick without waiting for
	// the next wall-clock second.
	jobs[0].plan = singleWorkerScheduleTick{at: time.Now()}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	scheduler := startInProcessCronScheduler(ctx, jobs)
	defer func() { _ = scheduler.Stop(ctx) }()
	select {
	case call := <-calls:
		if call.Operation != operation || call.Mode != "direct" || string(call.Input) != "{}" ||
			call.Context.Generation != 1 || call.Context.Principal != "workload:std.workload_identity.scheduler" {
			t.Fatalf("unexpected Worker ABI invocation: %+v", call)
		}
	case <-ctx.Done():
		t.Fatal("scheduler did not invoke the worker")
	}
}

type singleWorkerScheduleTick struct{ at time.Time }

func (tick singleWorkerScheduleTick) Next(after time.Time) time.Time {
	if after.Equal(tick.at) {
		return time.Time{}
	}
	return tick.at
}
