package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
)

// A scheduled run is admitted to the newest generation that includes
// its process when the attempt starts. Its internal calls stay in that
// generation although a replacement is published while it runs, the admission
// holds the generation until the attempt ends, and an instance no published
// generation includes runs no attempt.
func TestScheduledRunKeepsTheGenerationItWasAdmittedTo(t *testing.T) {
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
		if err := host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
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
	paused, resume, answers := make(chan struct{}, 1), make(chan struct{}, 1), make(chan []string, 1)
	invoked, admitted := 0, uint64(0)
	job := &CronJob{ID: "placed", Invoke: func(ctx context.Context) error {
		invoked++
		admitted = stateFromContext(ctx).processGeneration
		first := callEcho(ctx)
		paused <- struct{}{}
		<-resume
		answers <- []string{first, callEcho(ctx)}
		return nil
	}}
	deliver := func() error {
		return invokeAdmittedCronJob(withCronInvocation(context.Background(), time.Now(), "placed-1"), job)
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
	if admitted != 2 {
		t.Fatalf("scheduled run admitted to generation %d", admitted)
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

// A background attempt whose admission ends before the attempt does is
// interrupted rather than left running with a generation no host dispatches or
// moved to a newer one: when its host is replaced while its process is kept, and
// when the supervisor forces the retirement of its generation.
func TestBackgroundAttemptIsInterruptedWhenItsAdmissionEnds(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	echoOne := startProcessHostTestBackend(t, "echo_echo", 411, "sha256:echo-1")
	echoTwo := startProcessHostTestBackend(t, "echo_echo", 412, "sha256:echo-2")
	self := processGenerationInstance{PID: os.Getpid(), Identity: processInstanceIdentity(CurrentLinkedContractBundle())}
	target := serveProcessLinkForTest(t, http.NotFoundHandler())
	self.Network, self.Address = target.Network, target.Address
	// Every host incarnation serves the same dispatch socket.
	var serving atomic.Pointer[processHost]
	control := serveProcessLinkForTest(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { serving.Load().serveControl(w, req) }))
	config := &processLinkConfig{Token: processLinkTestToken, Dispatch: control}
	useProcessLinkForTest(t, config)
	startHost := func(number uint64, echo *processHostTestBackend) *processHost {
		t.Helper()
		host, err := newProcessHost(ProcessHostConfig{Name: "events"}, processLinkTestToken, processHostTestContract)
		if err != nil {
			t.Fatal(err)
		}
		if err := host.publish(processGenerationManifest{Generation: number, ContractRevision: processHostTestContract, Identity: processHostTestBuild(number), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
			Processes: map[string]processGenerationInstance{"worker_worker": self, "echo_echo": echo.instance}}); err != nil {
			t.Fatal(err)
		}
		serving.Store(host)
		return host
	}
	paused, resume := make(chan struct{}, 1), make(chan struct{}, 1)
	type observed struct {
		interrupted bool
		second      error
	}
	outcomes := make(chan observed, 1)
	job := &CronJob{ID: "placed", Invoke: func(ctx context.Context) error {
		invocation, _ := runtimeapi.InvocationFromContext(ctx)
		if _, err := invokeProcessLinkedBindingJSON(ctx, config, "echo/binding/echo_internal", "worker", invocation, []byte(`{}`)); err != nil {
			return err
		}
		paused <- struct{}{}
		<-resume
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		_, second := invokeProcessLinkedBindingJSON(context.WithoutCancel(ctx), config, "echo/binding/echo_internal", "worker", invocation, []byte(`{}`))
		outcomes <- observed{interrupted: errors.Is(context.Cause(ctx), errProcessAdmissionLost), second: second}
		return nil
	}}
	deliver := func() chan error {
		delivered := make(chan error, 1)
		go func() {
			delivered <- invokeAdmittedCronJob(withCronInvocation(context.Background(), time.Now(), "placed-1"), job)
		}()
		return delivered
	}
	check := func(scenario string, delivered chan error, notDispatchable uint64) {
		t.Helper()
		resume <- struct{}{}
		got := <-outcomes
		if !got.interrupted {
			t.Fatalf("%s: the attempt was not interrupted", scenario)
		}
		if typed, ok := errs.As(got.second); !ok || typed.Code != errs.Unavailable || !strings.Contains(typed.Message, "generation "+strconv.FormatUint(notDispatchable, 10)+" is not dispatchable") {
			t.Fatalf("%s: internal call after the admission ended = %v", scenario, got.second)
		}
		err := <-delivered
		// The attempt noticed the interruption only after acting and then
		// reported success; its effects are unknown, never a completed attempt.
		if typed, ok := errs.As(err); !ok || typed.Code != errs.Unavailable || typed.Meta["delivery"] != "unknown" || !errors.Is(err, errProcessAdmissionLost) {
			t.Fatalf("%s: delivery outcome = %v", scenario, err)
		}
	}

	previous := startHost(1, echoOne)
	delivered := deliver()
	<-paused
	close(previous.closing)
	replacement := startHost(2, echoTwo)
	check("host replaced", delivered, 1)

	delivered = deliver()
	<-paused
	if err := replacement.publish(processGenerationManifest{Generation: 3, ContractRevision: processHostTestContract, Identity: processHostTestBuild(3), Bindings: map[string]string{"echo/binding/echo_internal": "echo_echo"},
		Processes: map[string]processGenerationInstance{"worker_worker": self, "echo_echo": echoOne.instance}}); err != nil {
		t.Fatal(err)
	}
	if status, _ := replacement.retire(2, true); status != http.StatusNoContent {
		t.Fatalf("forced retirement = %d", status)
	}
	check("generation retirement forced", delivered, 2)
}
