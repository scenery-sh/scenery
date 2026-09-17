package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

func TestDevProcessInstancesStopOnlyWhenNoRetainedGenerationNamesThem(t *testing.T) {
	instance := func(name string) *devProcessInstance {
		return &devProcessInstance{process: build.DevelopmentProcess{Name: name}}
	}
	greeterOne, echoOne, greeterTwo, echoTwo := instance("greeter_greeter"), instance("echo_echo"), instance("greeter_greeter"), instance("echo_echo")
	// Generation 1 still has a greet request in flight; generation 2 replaced
	// greeter and generation 3 replaced echo.
	model := &devProcessModel{
		services: map[string]*devProcessInstance{"greeter_greeter": greeterTwo, "echo_echo": echoTwo},
		retained: map[uint64]map[string]*devProcessInstance{
			1: {"greeter_greeter": greeterOne, "echo_echo": echoOne},
			2: {"greeter_greeter": greeterTwo, "echo_echo": echoOne},
			3: {"greeter_greeter": greeterTwo, "echo_echo": echoTwo},
		},
	}
	retire := func(generation uint64) []*devProcessInstance {
		named := model.retained[generation]
		delete(model.retained, generation)
		return model.unreferenced(mapValues(named))
	}
	if stale := retire(2); len(stale) != 0 {
		t.Fatalf("retiring generation 2 stopped %d instances while generation 1 still names echo", len(stale))
	}
	stale := retire(1)
	if len(stale) != 2 || !slices.Contains(stale, greeterOne) || !slices.Contains(stale, echoOne) {
		t.Fatalf("retiring generation 1 stopped %#v, want its greeter and echo", stale)
	}
	state := devProcessState{services: model.services, retained: model.retained}
	if instances := state.instances(); len(instances) != 2 {
		t.Fatalf("current state names %d instances, want 2", len(instances))
	}
}

func TestDevProcessReplacementRestoresThePreviousHostOnlyAfterCandidatesStop(t *testing.T) {
	for _, scenario := range []string{"committed", "services-failed", "stop-unconfirmed", "host-failed", "candidate-shutdown-unconfirmed", "context-cancelled", "restore-failed", "nothing-to-restore"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []string
			replacement := devProcessReplacement{
				startServices: func(context.Context) error {
					events = append(events, "start-services")
					if scenario == "services-failed" {
						return errors.New("constructor failed")
					}
					return nil
				},
				stopPrevious: func() error {
					events = append(events, "stop-previous-host")
					if scenario == "stop-unconfirmed" {
						return errors.New("stop failed")
					}
					return nil
				},
				startHost: func(context.Context) error {
					events = append(events, "start-host")
					if scenario == "context-cancelled" {
						cancel()
					}
					if scenario == "committed" {
						return nil
					}
					return errors.New("host failed")
				},
				abandon: func() error {
					events = append(events, "abandon-candidates")
					if scenario == "candidate-shutdown-unconfirmed" {
						return errors.New("candidate stop failed")
					}
					return nil
				},
				restore: func(context.Context) (bool, error) {
					events = append(events, "restore-previous-host")
					switch scenario {
					case "restore-failed":
						return false, errors.New("restore failed")
					case "nothing-to-restore":
						return false, nil
					}
					return true, nil
				},
				commit: func(context.Context) { events = append(events, "commit") },
			}
			restored, err := replacement.run(ctx)
			want := map[string][]string{
				"committed":                      {"start-services", "stop-previous-host", "start-host", "commit"},
				"services-failed":                {"start-services"},
				"stop-unconfirmed":               {"start-services", "stop-previous-host", "abandon-candidates"},
				"host-failed":                    {"start-services", "stop-previous-host", "start-host", "abandon-candidates", "restore-previous-host"},
				"candidate-shutdown-unconfirmed": {"start-services", "stop-previous-host", "start-host", "abandon-candidates"},
				"context-cancelled":              {"start-services", "stop-previous-host", "start-host", "abandon-candidates"},
				"restore-failed":                 {"start-services", "stop-previous-host", "start-host", "abandon-candidates", "restore-previous-host"},
				"nothing-to-restore":             {"start-services", "stop-previous-host", "start-host", "abandon-candidates", "restore-previous-host"},
			}[scenario]
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			if (err == nil) != (scenario == "committed") || restored != (scenario == "host-failed") {
				t.Fatalf("restored = %t, err = %v", restored, err)
			}
			if scenario == "host-failed" && !strings.Contains(err.Error(), "restored the previous generation") {
				t.Fatalf("restored replacement error = %v", err)
			}
		})
	}
}

// A prepared instance's start environment names its link file, so it serves any
// host incarnation of that link and no other link.
func TestDevProcessPreparationServesOnlyItsOwnLinkAndIdentity(t *testing.T) {
	link, other := &devProcessLink{epoch: 1, path: "/tmp/process-link.json"}, &devProcessLink{epoch: 2, path: "/tmp/other/process-link.json"}
	nextIncarnation := &devProcessLink{epoch: 3, path: link.path}
	process := build.DevelopmentProcess{Name: "echo_echo", Binary: "/tmp/echo-1", Identity: build.DevelopmentProcessIdentity{ImplementationRevision: "sha256:one"}}
	request := devProcessStartRequest{Command: "/tmp/session/echo-1"}
	preparation := &devProcessPreparation{link: link, instances: map[string]*devProcessInstance{
		"echo_echo": {process: process, socket: "/tmp/s1.sock", request: &request},
	}, done: make(chan struct{})}
	close(preparation.done)
	if instance, err := preparation.instance(other, process); instance != nil || err != nil {
		t.Fatalf("instance of another host incarnation = %#v, %v", instance, err)
	}
	replaced := process
	replaced.Identity.ImplementationRevision = "sha256:two"
	if instance, err := preparation.instance(link, replaced); instance != nil || err != nil {
		t.Fatalf("instance of another identity = %#v, %v", instance, err)
	}
	for _, incarnation := range []*devProcessLink{link, nextIncarnation} {
		instance, err := preparation.instance(incarnation, process)
		if err != nil || instance == nil || instance.request != &request {
			t.Fatalf("prepared instance for incarnation %d = %#v, %v", incarnation.epoch, instance, err)
		}
	}
	// A preparation that failed refuses every instance.
	failed := &devProcessPreparation{link: link, err: errors.New("preflight failed"), done: make(chan struct{})}
	close(failed.done)
	if _, err := failed.instance(link, process); err == nil {
		t.Fatal("a failed preparation served an instance")
	}
	var none *devProcessPreparation
	if instance, err := none.instance(link, process); instance != nil || err != nil {
		t.Fatalf("absent preparation = %#v, %v", instance, err)
	}
	none.release(&devSupervisor{})
}

func TestDevProcessRestartBudgetDegradesAServiceThatKeepsCrashing(t *testing.T) {
	model := &devProcessModel{restarts: map[string][]time.Time{}, degraded: map[string]string{}}
	now := time.Now()
	for attempt := range devProcessRestartBudget {
		if !model.allowRestart("echo_echo", now.Add(time.Duration(attempt)*time.Second)) {
			t.Fatalf("restart %d was refused inside the budget", attempt)
		}
	}
	if model.allowRestart("echo_echo", now.Add(time.Duration(devProcessRestartBudget)*time.Second)) {
		t.Fatal("a service restarted beyond its budget")
	}
	// Another service keeps its own budget, and crashes older than the window
	// no longer count.
	if !model.allowRestart("greeter_greeter", now) {
		t.Fatal("an unrelated service was refused")
	}
	if !model.allowRestart("echo_echo", now.Add(devProcessRestartWindow+time.Second)) {
		t.Fatal("a service was refused after its window passed")
	}
	// A new build of the service forgets its crash history and degraded state.
	model.degraded["echo_echo"] = "restart budget exhausted"
	clearDevProcessRecovery(model, []*devProcessInstance{{process: build.DevelopmentProcess{Name: "echo_echo"}}})
	if len(model.restarts["echo_echo"]) != 0 || model.degraded["echo_echo"] != "" {
		t.Fatalf("a rebuilt service kept %d crashes and %q", len(model.restarts["echo_echo"]), model.degraded["echo_echo"])
	}
}

func TestServiceProcessStatusesReportEveryServiceAndItsState(t *testing.T) {
	instance := func(name, revision, pid string) *devProcessInstance {
		return &devProcessInstance{
			process: build.DevelopmentProcess{Name: name, Identity: build.DevelopmentProcessIdentity{ImplementationRevision: revision}},
			app:     &runningApp{pid: pid},
		}
	}
	crashed := instance("maps_maps", "sha256:maps", "303")
	crashed.stopped = true
	supervisor := &devSupervisor{processes: &devProcessModel{
		generation: 7,
		services: map[string]*devProcessInstance{
			"greeter_greeter": instance("greeter_greeter", "sha256:greeter", "301"),
			"echo_echo":       instance("echo_echo", "sha256:echo", "302"),
			"maps_maps":       crashed,
		},
		degraded: map[string]string{"maps_maps": "restart budget exhausted"},
		restarts: map[string][]time.Time{},
	}}
	supervisor.processes.publishStatus()
	statuses := supervisor.serviceProcessStatuses()
	if len(statuses) != 3 || statuses[0].Name != "echo_echo" || statuses[1].Name != "greeter_greeter" || statuses[2].Name != "maps_maps" {
		t.Fatalf("service process statuses = %#v", statuses)
	}
	if statuses[0].State != "running" || statuses[0].PID != "302" || statuses[0].Generation != 7 || statuses[0].ImplementationRevision != "sha256:echo" {
		t.Fatalf("running service = %#v", statuses[0])
	}
	if statuses[2].State != "degraded" || statuses[2].Reason != "restart budget exhausted" {
		t.Fatalf("degraded service = %#v", statuses[2])
	}
	session := supervisor.sessionServiceProcesses()
	if len(session) != 2 || session["service-echo-echo"].PID != 302 || session["service-greeter-greeter"].PID != 301 {
		t.Fatalf("session service processes = %#v", session)
	}
	if statuses := (&devSupervisor{}).serviceProcessStatuses(); statuses != nil {
		t.Fatalf("single application model reported service processes: %#v", statuses)
	}
}

// An assistant helper that starts during a complete activation reports its
// process, which registers the session and names the service processes while
// the activation still holds model.mu. Reading them must not wait for it.
func TestSessionServiceProcessesDoNotWaitForAnActivation(t *testing.T) {
	model := &devProcessModel{
		generation: 3,
		services: map[string]*devProcessInstance{"echo_echo": {
			process: build.DevelopmentProcess{Name: "echo_echo"}, app: &runningApp{pid: "302"},
		}},
		degraded: map[string]string{},
	}
	supervisor := &devSupervisor{processes: model}
	model.mu.Lock()
	model.publishStatus()
	read := make(chan map[string]localagent.Process, 1)
	go func() { read <- supervisor.sessionProcessesFor(&localagent.Session{}, "301") }()
	select {
	case processes := <-read:
		if processes["service-echo-echo"].PID != 302 || processes[localagent.RouteAPI].PID != 301 {
			t.Fatalf("session processes = %#v", processes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("registering the session waited for the activation that holds model.mu")
	}
	model.unlock()
}

// devProcessControlServer answers the background control requests of one
// instance socket with the next status of statuses, repeating the last one.
func devProcessControlServer(t *testing.T, statuses ...int) (string, *atomic.Int32) {
	t.Helper()
	directory, err := os.MkdirTemp("", "scp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "s.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(requests.Add(1)) - 1
		status := statuses[min(index, len(statuses)-1)]
		if status == http.StatusServiceUnavailable {
			http.Error(w, "capability_unavailable: event bus app/event_bus/orders for orders/consumer/placed is not registered", status)
			return
		}
		w.WriteHeader(status)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socket, &requests
}

func TestUnconfirmedActivationIsDegradedUntilReconciled(t *testing.T) {
	socket, requests := devProcessControlServer(t, http.StatusInternalServerError, http.StatusNoContent)
	instance := &devProcessInstance{process: build.DevelopmentProcess{Name: "echo_echo"}, socket: socket, app: &runningApp{pid: "302"}}
	model := &devProcessModel{
		token: "token", activationBackoff: time.Millisecond, degraded: map[string]string{},
		services: map[string]*devProcessInstance{"echo_echo": instance},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := &devSupervisor{ctx: ctx, processes: model}
	model.mu.Lock()
	supervisor.activateDevProcessInstances(ctx, model, []*devProcessInstance{instance})
	// The reconciler cannot confirm the activation while mu is held.
	model.publishStatus()
	if statuses := supervisor.serviceProcessStatuses(); len(statuses) != 1 || statuses[0].State != "degraded" || !strings.Contains(statuses[0].Reason, "activation unconfirmed") {
		t.Fatalf("unconfirmed activation reported %#v", statuses)
	}
	if processes := supervisor.sessionServiceProcesses(); processes["service-echo-echo"].PID != 302 {
		t.Fatalf("a serving instance with unconfirmed background work left the session: %#v", processes)
	}
	model.unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		statuses := supervisor.serviceProcessStatuses()
		if len(statuses) == 1 && statuses[0].State == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("activation was not reconciled: %#v after %d requests", statuses, requests.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("activation requests = %d, want 2", got)
	}
}

// A build whose background work lacks a capability, such as an event bus no
// provider registered, refuses activation. The instance keeps serving, reports
// that background work is unavailable and why, and the refusal is not repeated
// by the reconciler, whether it answers the first activation or a repetition.
func TestRefusedActivationReportsUnavailableBackgroundWork(t *testing.T) {
	for name, statuses := range map[string][]int{"first": {http.StatusServiceUnavailable}, "repeated": {http.StatusInternalServerError, http.StatusServiceUnavailable}} {
		t.Run(name, func(t *testing.T) {
			socket, requests := devProcessControlServer(t, statuses...)
			instance := &devProcessInstance{process: build.DevelopmentProcess{Name: "orders_orders"}, socket: socket, app: &runningApp{pid: "302"}}
			model := &devProcessModel{
				token: "token", activationBackoff: time.Millisecond, degraded: map[string]string{},
				services: map[string]*devProcessInstance{"orders_orders": instance},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			supervisor := &devSupervisor{ctx: ctx, processes: model}
			model.mu.Lock()
			supervisor.activateDevProcessInstances(ctx, model, []*devProcessInstance{instance})
			model.unlock()
			want := "background work unavailable: capability_unavailable: event bus app/event_bus/orders for orders/consumer/placed is not registered"
			deadline := time.Now().Add(2 * time.Second)
			for {
				statuses := supervisor.serviceProcessStatuses()
				model.mu.Lock()
				reconciling := instance.reconciling
				model.mu.Unlock()
				if len(statuses) == 1 && statuses[0].State == "degraded" && statuses[0].Reason == want && !reconciling {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("refused activation reported %#v (reconciling %t)", statuses, reconciling)
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(5 * time.Millisecond)
			if got := requests.Load(); got != int32(len(statuses)) {
				t.Fatalf("activation requests = %d, want %d", got, len(statuses))
			}
			if processes := supervisor.sessionServiceProcesses(); processes["service-orders-orders"].PID != 302 {
				t.Fatalf("an instance without background work left the session: %#v", processes)
			}
		})
	}
}

func TestActivationReconcilerStopsForAReplacedInstance(t *testing.T) {
	socket, requests := devProcessControlServer(t, http.StatusInternalServerError)
	instance := &devProcessInstance{process: build.DevelopmentProcess{Name: "echo_echo"}, socket: socket, app: &runningApp{pid: "302"}}
	model := &devProcessModel{
		token: "token", activationBackoff: time.Millisecond, degraded: map[string]string{},
		services: map[string]*devProcessInstance{"echo_echo": instance},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := &devSupervisor{ctx: ctx, processes: model}
	model.mu.Lock()
	supervisor.activateDevProcessInstances(ctx, model, []*devProcessInstance{instance})
	// A newer generation replaces the instance before its activation confirms.
	model.services = map[string]*devProcessInstance{"echo_echo": {process: build.DevelopmentProcess{Name: "echo_echo"}, app: &runningApp{pid: "303"}}}
	model.unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		model.mu.Lock()
		reconciling := instance.reconciling
		model.mu.Unlock()
		if !reconciling {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reconciler kept activating a replaced instance")
		}
		time.Sleep(time.Millisecond)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("a replaced instance received %d activation requests, want 1", got)
	}
}

// An unresponsive service delays control of itself only: while its activation
// is being repeated, the model stays available to replace other services, and
// once it is drained it is never activated again.
func TestActivationReconcilerDoesNotHoldTheModelAndNeverFollowsADrain(t *testing.T) {
	directory, err := os.MkdirTemp("", "scp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "s.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	answering, release := make(chan struct{}, 8), make(chan struct{})
	var mu sync.Mutex
	var requests []string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, filepath.Base(r.URL.Path))
		first := len(requests) == 1
		mu.Unlock()
		if first {
			answering <- struct{}{}
			<-release
		}
		if strings.HasSuffix(r.URL.Path, "/drain") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	slow := &devProcessInstance{process: build.DevelopmentProcess{Name: "echo_echo"}, socket: socket, app: &runningApp{pid: "302"}, activation: "unconfirmed", reconciling: true}
	model := &devProcessModel{
		token: "token", activationBackoff: time.Millisecond, degraded: map[string]string{},
		services: map[string]*devProcessInstance{"echo_echo": slow},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := &devSupervisor{ctx: ctx, processes: model}
	done := make(chan struct{})
	go func() {
		defer close(done)
		supervisor.reconcileDevProcessActivation(model, slow)
	}()
	<-answering
	locked := make(chan struct{})
	go func() {
		model.mu.Lock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("an unanswered activation held the process model")
	}
	// A newer generation replaces the instance and drains it while its
	// activation is still unanswered; the drain waits for that request only.
	model.services = map[string]*devProcessInstance{"echo_echo": {process: build.DevelopmentProcess{Name: "echo_echo"}, app: &runningApp{pid: "303"}}}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		supervisor.drainDevProcessInstances(ctx, model, []*devProcessInstance{slow})
	}()
	close(release)
	<-drained
	model.unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the reconciler kept running for a drained instance")
	}
	if err := slow.control(ctx, runtimeProcessActivatePath, "token"); !errors.Is(err, errDevProcessDrained) {
		t.Fatalf("a drained instance accepted an activation: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(requests, []string{"activate", "drain"}) {
		t.Fatalf("control requests = %v, want one activation followed by the drain", requests)
	}
}

// The agent stores session record keys as labels. A record read back from the
// agent must have its service entries replaced by the current instances, not
// kept beside them, and a service that no longer runs must leave the record.
func TestSessionRecordReplacesStoredServiceProcesses(t *testing.T) {
	model := &devProcessModel{
		services: map[string]*devProcessInstance{"echo_echo": {process: build.DevelopmentProcess{Name: "echo_echo"}, app: &runningApp{pid: "302"}}},
		degraded: map[string]string{},
	}
	model.publishStatus()
	supervisor := &devSupervisor{processes: model}
	stored := &localagent.Session{Processes: map[string]localagent.Process{
		"service-echo-echo": {PID: 101}, "service-maps-maps": {PID: 102}, "frontend-web": {PID: 103},
	}}
	processes := supervisor.sessionProcessesFor(stored, "301")
	if processes["service-echo-echo"].PID != 302 || processes["frontend-web"].PID != 103 || processes[localagent.RouteAPI].PID != 301 {
		t.Fatalf("session processes = %#v", processes)
	}
	if _, stale := processes["service-maps-maps"]; stale {
		t.Fatalf("a service that no longer runs stayed in the session record: %#v", processes)
	}
	for key := range processes {
		if localagentLabel(key) != key {
			t.Fatalf("session record key %q is not a label the agent keeps", key)
		}
	}
}

// A generation's processes keep the environment they started with, so the
// identity must follow what a started process receives: a moved database
// endpoint or a different winning duplicate changes it, while assembly order
// of distinct names and an overridden value do not.
func TestDevProcessEnvironmentIdentityFollowsTheEffectiveEnvironment(t *testing.T) {
	identity := devProcessEnvironmentIdentity
	base := []string{"SCENERY_APP_ID=app", "DATABASE_URL=postgres://127.0.0.1:5432/app"}
	if identity(base) != identity([]string{base[1], base[0]}) {
		t.Fatal("reordering distinct names changed the identity")
	}
	if identity(base) == identity([]string{"SCENERY_APP_ID=app", "DATABASE_URL=postgres://127.0.0.1:6543/app"}) {
		t.Fatal("a moved database endpoint kept the identity")
	}
	if identity([]string{"DATABASE_URL=endpoint-a", "DATABASE_URL=endpoint-b"}) == identity([]string{"DATABASE_URL=endpoint-b", "DATABASE_URL=endpoint-a"}) {
		t.Fatal("environments whose last duplicate differs share an identity")
	}
	if identity([]string{"DATABASE_URL=ambient-x", "DATABASE_URL=framework"}) != identity([]string{"DATABASE_URL=ambient-y", "DATABASE_URL=framework"}) {
		t.Fatal("a value the framework overrides changed the identity")
	}
}

// A passed preflight covers a restart of the same retained executable with the
// same identity and environment on another listener and link, and nothing else.
func TestDevProcessProofsCoverOnlyTheSameExecutableIdentityAndEnvironment(t *testing.T) {
	process := build.DevelopmentProcess{Name: "echo", ArtifactDigest: "sha256:artifact", Identity: build.DevelopmentProcessIdentity{
		ContractRevision: "sha256:service-contract", ImplementationRevision: "sha256:implementation", BuildInputDigest: "sha256:inputs", GoTarget: "development",
	}}
	env := func(socket, link, database string) []string {
		return []string{"DATABASE_URL=" + database, "SCENERY_LISTEN_NETWORK=unix", "SCENERY_LISTEN_ADDR=" + socket, "SCENERY_PROCESS_LINK=" + link}
	}
	var proofs devProcessProofs
	proven := devProcessProofKey(process, "/run/app/scenery-app-artifact", env("s1.sock", "link-1.json", "postgres://one"))
	proofs.remember(proven)
	if !proofs.proven(devProcessProofKey(process, "/run/app/scenery-app-artifact", env("s7.sock", "link-2.json", "postgres://one"))) {
		t.Fatal("a restart on another listener and link was not covered")
	}
	changedIdentity := process
	changedIdentity.Identity.ImplementationRevision = "sha256:other"
	unverified := process
	unverified.ArtifactDigest = ""
	for name, key := range map[string]string{
		"environment": devProcessProofKey(process, "/run/app/scenery-app-artifact", env("s1.sock", "link-1.json", "postgres://two")),
		"executable":  devProcessProofKey(process, "/run/app/scenery-app-other", env("s1.sock", "link-1.json", "postgres://one")),
		"identity":    devProcessProofKey(changedIdentity, "/run/app/scenery-app-artifact", env("s1.sock", "link-1.json", "postgres://one")),
		"unverified":  devProcessProofKey(unverified, "/run/app/scenery-app-artifact", env("s1.sock", "link-1.json", "postgres://one")),
	} {
		if proofs.proven(key) {
			t.Errorf("a changed %s was covered by an earlier preflight", name)
		}
	}
	for index := range devProcessProofLimit + 1 {
		proofs.remember(proven + "-" + strings.Repeat("x", index))
	}
	if proofs.Lock(); len(proofs.keys) > devProcessProofLimit {
		t.Fatalf("remembered %d proofs, limit %d", len(proofs.keys), devProcessProofLimit)
	}
	proofs.Unlock()
}

// A new host takes over the running instances whose identity is unchanged and
// starts every other service.
func TestDevProcessTakeoverKeepsOnlyRunningUnchangedInstances(t *testing.T) {
	identity := func(value string) build.DevelopmentProcessIdentity {
		return build.DevelopmentProcessIdentity{ContractRevision: "sha256:contract-" + value, ImplementationRevision: "sha256:" + value}
	}
	process := func(name, value string) build.DevelopmentProcess {
		return build.DevelopmentProcess{Name: name, Identity: identity(value)}
	}
	echo := &devProcessInstance{process: process("echo_echo", "echo")}
	greeter := &devProcessInstance{process: process("greeter_greeter", "greeter"), stopped: true}
	house := &devProcessInstance{process: process("house_house", "house")}
	kept, starting := devProcessTakeover(map[string]*devProcessInstance{"echo_echo": echo, "greeter_greeter": greeter, "house_house": house},
		[]build.DevelopmentProcess{process("echo_echo", "echo"), process("greeter_greeter", "greeter"), process("house_house", "house-changed"), process("garden_garden", "garden")})
	if len(kept) != 1 || kept["echo_echo"] != echo {
		t.Fatalf("kept instances = %#v", kept)
	}
	var names []string
	for _, process := range starting {
		names = append(names, process.Name)
	}
	if !slices.Equal(names, []string{"greeter_greeter", "house_house", "garden_garden"}) {
		t.Fatalf("starting services = %v", names)
	}
}
