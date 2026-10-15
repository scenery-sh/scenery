package main

import (
	"context"
	"sync"

	"scenery.sh/auth"
	"scenery.sh/errs"
	"scenery.sh/runtime"
)

type connectionEventBus struct {
	mu     sync.Mutex
	handle func(context.Context, runtime.ContractEventMessage) error
}

func (b *connectionEventBus) Subscribe(_ context.Context, subscription runtime.ContractEventSubscription) (func(context.Context) error, error) {
	b.mu.Lock()
	b.handle = subscription.Handle
	b.mu.Unlock()
	return func(context.Context) error { b.mu.Lock(); b.handle = nil; b.mu.Unlock(); return nil }, nil
}
func (b *connectionEventBus) Publish(ctx context.Context, message runtime.ContractEventMessage) error {
	b.mu.Lock()
	handle := b.handle
	b.mu.Unlock()
	return handle(ctx, message)
}

type connectionWorkloads struct {
	bus               *connectionEventBus
	owners            chan string
	events, schedules chan connectionAdmissionResult
}

func registerConnectionWorkloads() *connectionWorkloads {
	w := &connectionWorkloads{bus: &connectionEventBus{}, owners: make(chan string, 1), events: make(chan connectionAdmissionResult, 1), schedules: make(chan connectionAdmissionResult, 1)}
	token := func(ctx context.Context, owner string) connectionAdmissionResult {
		value, err := auth.ConnectionTokenForUser(ctx, owner, "independent", "files.read")
		return connectionAdmissionResult{Code: errs.Code(err), Issued: value == "independent-access"}
	}
	must(runtime.RegisterContractEventBus("probe/event_bus/connections", w.bus))
	must(runtime.RegisterContractEventConsumer(runtime.ContractEventConsumerRegistration{
		Address: "probe/binding/connection_worker", BusAddress: "probe/event_bus/connections", Channel: "connections", ContractAddress: "probe/event/connection", ContractVersion: 1,
		Guarantee: "at_most_once", Identity: "std.workload_identity.connection_worker", Attempts: 1, Backoff: "none",
		Invoke: func(ctx context.Context, payload []byte) error { w.events <- token(ctx, string(payload)); return nil },
	}))
	must(runtime.RegisterContractSchedule(runtime.ContractScheduleRegistration{
		Address: "probe/schedule/connections", Name: "connections", TriggerKind: "every", TriggerValue: "1s",
		Identity:             runtime.ContractWorkloadIdentity{Address: "std.workload_identity.connection_schedule", Issuer: "std.identity_issuer.runtime", PrincipalType: "std.type.workload_principal"},
		AuthorizationAddress: "std.authorization.scheduled", PipelineAddress: "std.pipeline.empty",
		Invoke: func(ctx context.Context) error {
			select {
			case owner := <-w.owners:
				w.schedules <- token(ctx, owner)
			default:
			}
			return nil
		},
	}))
	return w
}
