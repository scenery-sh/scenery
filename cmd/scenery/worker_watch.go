package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type workerWatcher struct {
	directory, capabilities, token string
	dev                            *workerDevelopment
	devControl                     *workerDevControl
	exits                          chan struct{}
	client                         *http.Client
	bundle                         workerBundle
	active                         map[string]*workerProcess
	all                            []*workerProcess
	generation, sequence           uint64
	retirements                    sync.WaitGroup
}

func (watch *workerWatcher) run(ctx context.Context, hostDone <-chan error) error {
	defer func() {
		watch.retirements.Wait()
		stopWorkers(watch.all)
	}()
	watch.exits = make(chan struct{}, 1)
	for _, process := range watch.active {
		watch.observeExit(ctx, process)
	}
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	var reconciliation <-chan time.Time
	var seen workerInputs
	if watch.dev != nil {
		events, watchErrors = watch.dev.watch.fs.Events, watch.dev.watch.fs.Errors
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		reconciliation, seen = ticker.C, watch.dev.applied
	}
	debounce := time.NewTimer(time.Hour)
	defer debounce.Stop()
	debounce.Stop()
	retry := time.NewTimer(time.Hour)
	defer retry.Stop()
	retry.Stop()
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
			return <-hostDone
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
		case <-reconciliation:
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
		case <-watch.exits:
			watch.restartStopped(ctx, retry)
		case <-retry.C:
			watch.restartStopped(ctx, retry)
		}
	}
}

func (watch *workerWatcher) observeExit(ctx context.Context, process *workerProcess) {
	go func() {
		select {
		case <-ctx.Done():
		case <-process.done:
			select {
			case watch.exits <- struct{}{}:
			default:
			}
		}
	}()
}

func (watch *workerWatcher) restartStopped(ctx context.Context, retry *time.Timer) {
	if watch.hasStoppedWorker() {
		if err := watch.replace(ctx, watch.bundle, nil); err != nil {
			fmt.Fprintf(os.Stderr, "worker restart failed: %v\n", err)
			retry.Reset(time.Second)
		} else if watch.devControl != nil {
			watch.devControl.publish(watch.dev.applied, watch.bundle, watch.generation, nil)
		}
	}
}

func (watch *workerWatcher) hasStoppedWorker() bool {
	for _, process := range watch.active {
		select {
		case <-process.done:
			return true
		default:
		}
	}
	return false
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
	timing := workerRebuildTiming{
		DetectMS: workerMilliseconds(started.Sub(detected)), Rebuilt: []string{},
		RebuiltModules: []string{}, SkippedModules: []string{},
	}
	defer func() {
		if errors.Is(returnErr, errWorkerInputsChanged) {
			return
		}
		timing.TotalMS = workerMilliseconds(time.Since(detected))
		timing.Generation = watch.generation
		if returnErr != nil {
			g := agentGraph{Result: watch.dev.compiled}
			modules, _ := g.modules()
			failure := describeDevBuildFailure(returnErr, watch.dev.watch.root, modules, paths)
			watch.devControl.failure(current, failure, &timing)
		} else {
			watch.devControl.publish(current, watch.bundle, watch.generation, &timing)
		}
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
	for _, program := range bundle.Programs {
		module, _, _ := strings.Cut(program.Service, "/service/")
		if workerStillMatches(watch.active[program.Process], program) {
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
	readyStarted := time.Now()
	err = watch.replace(ctx, bundle, func() error { return candidate.verify(current) })
	timing.ReadyMS = workerMilliseconds(time.Since(readyStarted))
	if err != nil {
		return err
	}
	candidate.applied = current
	*watch.dev = candidate
	return nil
}

func (watch *workerWatcher) replace(ctx context.Context, bundle workerBundle, verify func() error) error {
	next := make(map[string]*workerProcess, len(bundle.Programs))
	watch.sequence++
	started := make([]*workerProcess, len(bundle.Programs))
	errs := make([]error, len(bundle.Programs))
	var group sync.WaitGroup
	for index, program := range bundle.Programs {
		previous := watch.active[program.Process]
		if workerStillMatches(previous, program) {
			next[program.Process] = previous
			continue
		}
		id := fmt.Sprintf("%x-%x", watch.sequence, index)
		group.Go(func() {
			started[index], errs[index] = startBundleWorker(ctx, watch.directory, watch.capabilities, id, program)
		})
	}
	group.Wait()
	started = slices.DeleteFunc(started, func(process *workerProcess) bool { return process == nil })
	if err := errors.Join(errs...); err != nil {
		stopWorkers(started)
		return err
	}
	if verify != nil {
		if err := verify(); err != nil {
			stopWorkers(started)
			return err
		}
	}
	if len(started) == 0 && len(next) == len(watch.active) {
		watch.bundle = bundle
		return nil
	}
	for _, process := range started {
		next[process.program.Process] = process
	}
	watch.all = append(watch.all, started...)
	if err := publishWorkerGeneration(ctx, watch.client, watch.token, bundle, next, watch.sequence); err != nil {
		current, statusErr := watch.currentGeneration(ctx)
		if statusErr != nil || current != watch.sequence {
			if statusErr == nil && current < watch.sequence {
				stopWorkers(started)
			}
			return errors.Join(err, statusErr)
		}
	}
	var retired []*workerProcess
	for name, process := range watch.active {
		if next[name] != process {
			retired = append(retired, process)
		}
	}
	previous := watch.generation
	watch.generation, watch.active, watch.bundle = watch.sequence, next, bundle
	for _, process := range started {
		watch.observeExit(ctx, process)
	}
	watch.retirements.Add(1)
	go func() {
		defer watch.retirements.Done()
		watch.retire(ctx, previous, retired)
	}()
	fmt.Fprintf(os.Stderr, "application generation %d ready (%d worker replacements)\n", watch.generation, len(started))
	return nil
}

func workerStillMatches(process *workerProcess, program workerProgram) bool {
	if process == nil || process.program.Digest != program.Digest || process.program.Identity != program.Identity || process.program.Runtime != program.Runtime {
		return false
	}
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

func (watch *workerWatcher) currentGeneration(ctx context.Context) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host/__scenery/process/v1/generations", nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+watch.token)
	response, err := watch.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	var status struct{ Current uint64 }
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("host did not confirm the generation")
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&status)
	return status.Current, err
}

func (watch *workerWatcher) retire(ctx context.Context, generation uint64, workers []*workerProcess) {
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	drain, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := workerControlRequest(drain, watch.client, watch.token, http.MethodDelete, path, nil, http.StatusNoContent); err == nil {
			stopWorkers(workers)
			return
		}
		select {
		case <-drain.Done():
			force, cancelForce := context.WithTimeout(ctx, time.Second)
			defer cancelForce()
			if err := workerControlRequest(force, watch.client, watch.token, http.MethodDelete, path+"?force=true", nil, http.StatusNoContent); err == nil {
				stopWorkers(workers)
			}
			return
		case <-ticker.C:
		}
	}
}

func stopWorkers(processes []*workerProcess) {
	for _, process := range processes {
		process.stop()
	}
}
