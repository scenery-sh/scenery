package runtime

import (
	"context"
	"errors"
	"testing"
)

type fakeContractEventBus struct {
	subscriptions []ContractEventSubscription
	published     []ContractEventMessage
}

func (bus *fakeContractEventBus) Publish(_ context.Context, message ContractEventMessage) error {
	bus.published = append(bus.published, message)
	return nil
}

func (bus *fakeContractEventBus) Subscribe(_ context.Context, subscription ContractEventSubscription) (func(context.Context) error, error) {
	bus.subscriptions = append(bus.subscriptions, subscription)
	return func(context.Context) error { return nil }, nil
}

func TestContractEventsSubscribeWithTypedIdentityAndPublishMatchedOutcomes(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	bus := &fakeContractEventBus{}
	if err := RegisterContractEventBus("app/event_bus/events", bus); err != nil {
		t.Fatal(err)
	}
	var consumed string
	if err := RegisterContractEventConsumer(ContractEventConsumerRegistration{
		Address: "house/binding/consume", BusAddress: "app/event_bus/events", Channel: "house.scene-registered",
		ContractAddress: "house/event/scene_registered", ContractVersion: 1, Guarantee: "at_least_once",
		Identity: "std.workload_identity.event_consumer", Attempts: 5, Backoff: "exponential", DeadLetterChannel: "house.scene-registered.dead",
		Invoke: func(ctx context.Context, payload []byte) error {
			consumed = string(payload)
			if auth := CurrentAuth(); auth == nil || auth.UID != "std.workload_identity.event_consumer" {
				return errors.New("event identity was not minted")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	running, err := StartContractEventRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer running.Stop(context.Background())
	if len(bus.subscriptions) != 1 {
		t.Fatalf("subscriptions = %#v", bus.subscriptions)
	}
	message := ContractEventMessage{BusAddress: "app/event_bus/events", Channel: "house.scene-registered", ContractAddress: "house/event/scene_registered", ContractVersion: 1, Payload: []byte(`{"scene_id":"one"}`)}
	if err := bus.subscriptions[0].Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if consumed != `{"scene_id":"one"}` {
		t.Fatalf("consumed = %q", consumed)
	}

	if err := RegisterContractEventEmission(ContractEventEmissionRegistration{
		Address: "house/event_emission/registered", OperationAddress: "house/operation/register", BusAddress: "app/event_bus/events",
		Channel: "house.scene-registered", ContractAddress: "house/event/scene_registered", ContractVersion: 1, Guarantee: "at_least_once",
		Attempts: 3, Backoff: "exponential", DeadLetterChannel: "house.scene-registered.dead",
		OrderingKey: func(any) (string, error) { return "tenant-one", nil }, DeduplicationKey: func(any) (string, error) { return "scene-one", nil },
		Encode: func(outcome any) ([]byte, bool, error) {
			if outcome != "registered" {
				return nil, false, nil
			}
			return []byte(`{"scene_id":"one"}`), true, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := PublishContractOperationOutcome(context.Background(), "house/operation/register", "registered"); err != nil {
		t.Fatal(err)
	}
	if len(bus.published) != 1 || string(bus.published[0].Payload) != `{"scene_id":"one"}` || bus.published[0].OrderingKey != "tenant-one" || bus.published[0].DeduplicationKey != "scene-one" || bus.published[0].Attempts != 3 || bus.published[0].Backoff != "exponential" || bus.published[0].DeadLetterChannel != "house.scene-registered.dead" {
		t.Fatalf("published = %#v", bus.published)
	}
}

func TestContractEventRuntimeRejectsMissingBusAndContradictoryMessage(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterContractEventConsumer(ContractEventConsumerRegistration{
		Address: "house/binding/consume", BusAddress: "app/event_bus/missing", Channel: "events", ContractAddress: "house/event/event", ContractVersion: 1,
		Guarantee: "at_most_once", Identity: "std.workload_identity.event_consumer", Attempts: 1, Backoff: "none", Invoke: func(context.Context, []byte) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := StartContractEventRuntime(context.Background()); err == nil {
		t.Fatal("expected missing event bus failure")
	}

	restoreSecond := replaceGlobalRegistryForTest()
	defer restoreSecond()
	bus := &fakeContractEventBus{}
	if err := RegisterContractEventBus("app/event_bus/events", bus); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContractEventConsumer(ContractEventConsumerRegistration{
		Address: "house/binding/consume", BusAddress: "app/event_bus/events", Channel: "events", ContractAddress: "house/event/event", ContractVersion: 1,
		Guarantee: "at_most_once", Identity: "std.workload_identity.event_consumer", Attempts: 1, Backoff: "none", Invoke: func(context.Context, []byte) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	running, err := StartContractEventRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer running.Stop(context.Background())
	if err := bus.subscriptions[0].Handle(context.Background(), ContractEventMessage{BusAddress: "app/event_bus/events", Channel: "other", ContractAddress: "house/event/event", ContractVersion: 1}); err == nil {
		t.Fatal("expected contradictory message rejection")
	}
}
