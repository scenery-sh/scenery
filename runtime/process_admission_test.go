package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
)

// processAdmissionTestBus delivers messages synchronously to its subscriber.
type processAdmissionTestBus struct {
	handle func(context.Context, ContractEventMessage) error
}

func (bus *processAdmissionTestBus) Publish(context.Context, ContractEventMessage) error { return nil }

func (bus *processAdmissionTestBus) Subscribe(_ context.Context, subscription ContractEventSubscription) (func(context.Context) error, error) {
	bus.handle = subscription.Handle
	return nil, nil
}

// An event delivery attempt is admitted to the newest generation that includes
// its process when the attempt starts. Its internal calls stay in that
// generation although a replacement is published while it runs, the admission
// holds the generation until the attempt ends, and an instance no published
// generation includes runs no attempt.
func TestEventDeliveryAttemptKeepsTheGenerationItWasAdmittedTo(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	echoOne := startProcessHostTestBackend(t, "echo_echo", 401, "sha256:echo-1")
	echoTwo := startProcessHostTestBackend(t, "echo_echo", 402, "sha256:echo-2")
	self := processGenerationInstance{PID: os.Getpid(), Identity: processInstanceIdentity(CurrentLinkedContractBundle())}
	target := serveProcessLinkForTest(t, http.NotFoundHandler())
	self.Network, self.Address = target.Network, target.Address
	host, err := newProcessHost(ProcessHostConfig{Name: "events"}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	control := serveProcessLinkForTest(t, http.HandlerFunc(host.serveControl))
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: control}
	useProcessLinkForTest(t, config)
	publish := func(number uint64, worker processGenerationInstance, echo *processHostTestBackend) {
		t.Helper()
		if err := host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"worker_worker": worker, "echo_echo": echo.instance}}); err != nil {
			t.Fatal(err)
		}
	}
	retireWhenReleased := func(number uint64) int {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			status, _ := host.retire(number, false)
			if status != http.StatusConflict || time.Now().After(deadline) {
				return status
			}
			time.Sleep(time.Millisecond)
		}
	}
	callEcho := func(ctx context.Context) string {
		invocation, _ := runtimeapi.InvocationFromContext(ctx)
		output, err := invokeProcessLinkedBindingJSON(ctx, config, "echo/binding/echo_internal", "worker", invocation, []byte(`{}`))
		if err != nil {
			return err.Error()
		}
		var answer string
		_ = json.Unmarshal(output, &answer)
		return answer
	}
	bus := &processAdmissionTestBus{}
	paused, resume, answers := make(chan struct{}, 1), make(chan struct{}, 1), make(chan []string, 1)
	invoked := 0
	for _, register := range []error{
		RegisterContractEventBus("app/event_bus/orders", bus),
		RegisterContractEventConsumer(ContractEventConsumerRegistration{
			Address: "worker/consumer/placed", BusAddress: "app/event_bus/orders", Channel: "orders", ContractAddress: "orders/contract/placed", ContractVersion: 1,
			Guarantee: "at_least_once", Identity: "worker", Attempts: 1, Backoff: "none",
			Invoke: func(ctx context.Context, _ []byte) error {
				invoked++
				first := callEcho(ctx)
				paused <- struct{}{}
				<-resume
				answers <- []string{first, callEcho(ctx)}
				return nil
			},
		}),
	} {
		if register != nil {
			t.Fatal(register)
		}
	}
	events, err := StartContractEventRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = events.Stop(context.Background()) }()
	deliver := func() error {
		return bus.handle(context.Background(), ContractEventMessage{ID: "message-1", BusAddress: "app/event_bus/orders", Channel: "orders", ContractAddress: "orders/contract/placed", ContractVersion: 1, Payload: []byte(`{}`)})
	}

	publish(1, self, echoOne)
	delivered := make(chan error, 1)
	go func() { delivered <- deliver() }()
	<-paused
	publish(2, self, echoTwo)
	if status, _ := host.retire(1, false); status != http.StatusConflict {
		t.Fatalf("retiring the generation of a running delivery attempt = %d", status)
	}
	resume <- struct{}{}
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	if got := <-answers; got[0] != "echo_echo:sha256:echo-1" || got[1] != "echo_echo:sha256:echo-1" {
		t.Fatalf("a delivery attempt admitted to generation 1 answered from %v", got)
	}
	if status := retireWhenReleased(1); status != http.StatusNoContent {
		t.Fatalf("retiring the generation after its delivery attempt ended = %d", status)
	}

	resume <- struct{}{}
	if err := deliver(); err != nil {
		t.Fatal(err)
	}
	<-paused
	if got := <-answers; got[0] != "echo_echo:sha256:echo-2" || got[1] != "echo_echo:sha256:echo-2" {
		t.Fatalf("a delivery attempt admitted after the replacement answered from %v", got)
	}

	// A scheduled run is admitted the same way.
	scheduled := uint64(0)
	job := &CronJob{ID: "reconcile", Invoke: func(ctx context.Context) error {
		scheduled = stateFromContext(ctx).processGeneration
		return nil
	}}
	if err := invokeAdmittedCronJob(withCronInvocation(context.Background(), time.Now(), "reconcile-1"), job); err != nil || scheduled != 2 {
		t.Fatalf("scheduled run admitted to generation %d, %v", scheduled, err)
	}

	replaced := self
	replaced.PID++
	publish(3, replaced, echoTwo)
	if status := retireWhenReleased(2); status != http.StatusNoContent {
		t.Fatalf("retiring generation 2 = %d", status)
	}
	before := invoked
	err = deliver()
	if typed, ok := errs.As(err); !ok || typed.Code != errs.Unavailable || invoked != before {
		t.Fatalf("delivery to an instance no generation includes = %v (invoked %d times)", err, invoked-before)
	}
	if status := host.status(); status.Generations[0].InFlight != 0 {
		t.Fatalf("a refused admission holds generation %d", status.Generations[0].Generation)
	}
}

// A consumer whose bus no provider registered makes background activation fail
// as capability_unavailable instead of running without its deliveries, and a
// process-linked runtime refuses its activation as unavailable rather than as a
// failure worth repeating.
func TestEventConsumerWithoutARegisteredBusIsUnavailable(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	if err := RegisterContractEventConsumer(ContractEventConsumerRegistration{
		Address: "worker/consumer/placed", BusAddress: "app/event_bus/orders", Channel: "orders", ContractAddress: "orders/contract/placed", ContractVersion: 1,
		Guarantee: "at_least_once", Identity: "worker", Attempts: 1, Backoff: "none", Invoke: func(context.Context, []byte) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	background := &runtimeBackground{ctx: context.Background(), durable: &durableRuntime{ctx: context.Background()}}
	setProcessBackground(background)
	t.Cleanup(func() { setProcessBackground(nil) })
	request := httptest.NewRequest(http.MethodPost, processActivatePath, nil)
	request.Header.Set("Authorization", "Bearer "+processLinkTestToken)
	recorder := httptest.NewRecorder()
	(&server{}).handleProcessActivate(recorder, request, nil)
	if recorder.Code != http.StatusServiceUnavailable || !strings.HasPrefix(recorder.Body.String(), "capability_unavailable: event bus app/event_bus/orders for worker/consumer/placed is not registered") {
		t.Fatalf("activation without the consumer's bus = %d %q", recorder.Code, recorder.Body.String())
	}
}
