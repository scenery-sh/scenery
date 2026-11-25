package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/devtelemetry"
)

type workerWatcher struct {
	dev        *workerDevelopment
	devControl *workerDevControl
	app        *devtelemetry.App
	supervisor *workerSupervisor
	bundle     workerBundle
	status     workerSupervisorStatus
	lastFault  uint64
}

func (watch *workerWatcher) run(ctx context.Context, hostDone <-chan error) error {
	events, watchErrors := watch.dev.watch.fs.Events, watch.dev.watch.fs.Errors
	reconciliation := time.NewTicker(5 * time.Second)
	defer reconciliation.Stop()
	monitor := time.NewTicker(500 * time.Millisecond)
	defer monitor.Stop()
	seen := watch.dev.applied
	debounce := time.NewTimer(time.Hour)
	defer debounce.Stop()
	debounce.Stop()
	pending := make(map[string]struct{})
	var detected time.Time
	var reconcile, retryCandidate bool
	schedule := func() {
		if detected.IsZero() {
			detected = time.Now()
		}
		debounce.Reset(workerEditDebounce)
	}
	for {
		select {
		case err := <-hostDone:
			return err
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if !watch.dev.watch.skip(event.Name) {
				pending[event.Name] = struct{}{}
				schedule()
			}
		case _, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
			}
			reconcile = true
			schedule()
		case <-reconciliation.C:
			reconcile = true
			schedule()
		case <-debounce.C:
			current, err := watch.dev.watch.capture(seen, pending, reconcile, true)
			observed := detected
			clear(pending)
			reconcile, detected = false, time.Time{}
			if err != nil {
				fmt.Fprintf(os.Stderr, "application inputs could not be read: %v\n", err)
				continue
			}
			if maps.Equal(current, seen) && !retryCandidate {
				continue
			}
			seen, retryCandidate = current, false
			if err := watch.refresh(ctx, current, observed); err != nil {
				fmt.Fprintf(os.Stderr, "application update failed: %v\n", err)
				if errors.Is(err, errWorkerInputsChanged) {
					// The next candidate must include edits even if their
					// notifications were dropped or have not arrived yet.
					reconcile, retryCandidate = true, true
					schedule()
				}
			}
		case <-monitor.C:
			watch.observeSupervisor(ctx)
		}
	}
}

func (watch *workerWatcher) observeSupervisor(ctx context.Context) {
	probe, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	status, err := watch.supervisor.status(probe)
	if err != nil {
		return
	}
	watch.recordFaults(status)
	if !status.matches(watch.bundle) {
		return
	}
	if status.Current != watch.status.Current {
		watch.devControl.publish(watch.dev.applied, watch.bundle, status.Current, nil)
	}
	watch.status = status
}

func (watch *workerWatcher) recordFaults(status workerSupervisorStatus) {
	for _, fault := range status.Faults {
		if fault.Sequence <= watch.lastFault {
			continue
		}
		watch.lastFault = fault.Sequence
		module, _, _ := strings.Cut(fault.Service, "/service/")
		exitCode := -1
		if fault.ExitCode != nil {
			exitCode = *fault.ExitCode
		}
		recordDevWorkerExit(watch.app, fault.Generation, module, fault.Runtime, exitCode, fault.Signal)
	}
}

var errWorkerInputsChanged = errors.New("application changed while workers were compiling")

func (dev *workerDevelopment) verify(expected workerInputs) error {
	actual, err := dev.watch.capture(nil, nil, true, false)
	if err != nil {
		return err
	}
	if !maps.Equal(actual, expected) {
		return errWorkerInputsChanged
	}
	return nil
}

type workerRebuildTiming struct {
	Generation     uint64   `json:"generation"`
	Declarations   bool     `json:"declarations"`
	Go             bool     `json:"go"`
	GoLinks        []string `json:"go_links,omitempty"`
	JS             []string `json:"js,omitempty"`
	Cargo          []string `json:"cargo,omitempty"`
	Rebuilt        []string `json:"rebuilt"`
	RebuiltModules []string `json:"rebuilt_modules"`
	SkippedModules []string `json:"skipped_modules"`
	Skipped        int      `json:"skipped"`
	DetectMS       float64  `json:"detect_ms"`
	ContractMS     float64  `json:"contract_ms"`
	GoMS           float64  `json:"go_ms"`
	JSMS           float64  `json:"js_ms"`
	CargoMS        float64  `json:"cargo_ms"`
	LinkMS         float64  `json:"-"`
	CompileMS      float64  `json:"compile_ms"`
	ReadyMS        float64  `json:"ready_ms"`
	TotalMS        float64  `json:"total_ms"`
}

func workerMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func (watch *workerWatcher) refresh(ctx context.Context, current workerInputs, detected time.Time) (returnErr error) {
	paths := changedWorkerInputs(watch.dev.applied, current)
	if len(paths) == 0 {
		return nil
	}
	started := time.Now()
	edit := watch.dev.classify(paths)
	timing := workerRebuildTiming{
		Declarations: edit.declarations,
		DetectMS:     workerMilliseconds(started.Sub(detected)), Rebuilt: []string{},
		RebuiltModules: []string{}, SkippedModules: []string{},
	}
	defer func() {
		if errors.Is(returnErr, errWorkerInputsChanged) {
			return
		}
		timing.TotalMS = workerMilliseconds(time.Since(detected))
		timing.Generation = watch.status.Current
		if returnErr != nil {
			g := agentGraph{Result: watch.dev.compiled}
			modules, _ := g.modules()
			failure := describeDevBuildFailure(returnErr, watch.dev.watch.root, modules, paths)
			watch.devControl.failure(current, failure, &timing)
		} else {
			watch.devControl.publish(current, watch.bundle, watch.status.Current, &timing)
		}
		trigger := "body_only"
		if edit.declarations || edit.cargoGraph {
			trigger = "contract_or_config"
		}
		var code string
		if returnErr != nil {
			if diagnostic, ok := errors.AsType[*cliDiagnosticError](returnErr); ok {
				code = diagnostic.diagnostic.Code
			} else if timing.ReadyMS == 0 {
				code = workerCheckDiagnostic(watch.dev.compiled, returnErr).Code
			} else {
				code = cliErrorDiagnostic(returnErr).Code
			}
		}
		recordDevRebuild(watch.app, timing, trigger, code)
		if data, err := json.Marshal(timing); err == nil {
			fmt.Fprintf(os.Stderr, "worker rebuild %s\n", data)
		}
	}()
	candidate := *watch.dev
	candidate.rust.artifacts = maps.Clone(candidate.rust.artifacts)
	bundle, err := candidate.rebuild(ctx, watch.bundle, current, paths, &timing)
	timing.CompileMS = workerMilliseconds(time.Since(started))
	if err != nil {
		return err
	}
	readyStarted := time.Now()
	status, err := watch.supervisor.replace(ctx, bundle, func() error { return candidate.verify(current) })
	timing.ReadyMS = workerMilliseconds(time.Since(readyStarted))
	if err != nil {
		return err
	}
	for _, program := range bundle.Programs {
		module, _, _ := strings.Cut(program.Service, "/service/")
		if previous, next := watch.status.Processes[program.Process], status.Processes[program.Process]; previous.PID == next.PID && previous.Digest == next.Digest {
			timing.Skipped++
			timing.SkippedModules = append(timing.SkippedModules, module)
		} else {
			timing.Rebuilt = append(timing.Rebuilt, program.Service)
			timing.RebuiltModules = append(timing.RebuiltModules, module)
		}
	}
	slices.Sort(timing.Rebuilt)
	slices.Sort(timing.RebuiltModules)
	slices.Sort(timing.SkippedModules)
	watch.recordFaults(status)
	watch.bundle, watch.status = bundle, status
	candidate.applied = current
	*watch.dev = candidate
	return nil
}
