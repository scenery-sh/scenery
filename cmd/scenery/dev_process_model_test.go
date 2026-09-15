package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

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
