package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestDevProcessPreparationServesOnlyItsOwnLinkAndIdentity(t *testing.T) {
	link, other := &devProcessLink{epoch: 1}, &devProcessLink{epoch: 2}
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
	instance, err := preparation.instance(link, process)
	if err != nil || instance == nil || instance.request != &request {
		t.Fatalf("prepared instance = %#v, %v", instance, err)
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
	if len(session) != 2 || session["service:echo_echo"].PID != 302 || session["service:greeter_greeter"].PID != 301 {
		t.Fatalf("session service processes = %#v", session)
	}
	if statuses := (&devSupervisor{}).serviceProcessStatuses(); statuses != nil {
		t.Fatalf("single application model reported service processes: %#v", statuses)
	}
}
