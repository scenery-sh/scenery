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
	"scenery.sh/internal/devtelemetry"
	workerruntime "scenery.sh/runtime"
)

type workerWatcher struct {
	directory, capabilities, token string
	dev                            *workerDevelopment
	devControl                     *workerDevControl
	app                            *devtelemetry.App
	faults                         map[*workerProcess]struct{}
	exits                          chan struct{}
	client                         *http.Client
	bundle                         workerBundle
	active                         map[string]*workerProcess
	all                            []*workerProcess
	generation, sequence           uint64
	retirements                    sync.WaitGroup
	generations                    workerGenerations
}

func (watch *workerWatcher) run(ctx context.Context, hostDone <-chan error) error {
	defer func() {
		watch.retirements.Wait()
		stopWorkers(watch.all)
		if watch.dev != nil {
			pruneJSWorkersByPaths(watch.dev.watch.root, nil)
		}
	}()
	watch.generations.add(watch.generation, watch.active)
	watch.exits = make(chan struct{}, 1)
	watch.faults = make(map[*workerProcess]struct{})
	for _, process := range watch.active {
		watch.observeExit(ctx, process)
	}
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	var reconciliation <-chan time.Time
	var configTicks <-chan time.Time
	var configPath string
	var lastConfigInfo os.FileInfo
	var stopConfigTicker func()
	defer func() {
		if stopConfigTicker != nil {
			stopConfigTicker()
		}
	}()
	var seen workerInputs
	if watch.dev != nil {
		events, watchErrors = watch.dev.watch.fs.Events, watch.dev.watch.fs.Errors
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		reconciliation, seen = ticker.C, watch.dev.applied
		if watch.bundle.config != nil {
			if path, ok := workerDevConfigFile(watch.dev.watch.root, watch.dev.envName); ok {
				configPath = path
				lastConfigInfo, _ = os.Stat(configPath)
				configTicker := time.NewTicker(devConfigPollInterval)
				stopConfigTicker = configTicker.Stop
				configTicks = configTicker.C
			}
		}
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
			if watch.dev.watch.eventRelevant(event.Name) {
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
		case <-configTicks:
			info, err := os.Stat(configPath)
			if err != nil {
				info = nil
			}
			if sameConfigFile(lastConfigInfo, info) {
				continue
			}
			lastConfigInfo = info
			watch.applyConfigurationChange(ctx)
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
			if applied, err := watch.refresh(ctx, current, observed); err != nil {
				fmt.Fprintf(os.Stderr, "application update failed: %v\n", err)
				if errors.Is(err, errWorkerInputsChanged) {
					// The next candidate must include edits even if their
					// notifications were dropped or have not arrived yet.
					reconcile, retryCandidate = true, true
					schedule()
				}
			} else if applied != nil {
				seen = applied
				if configTicks == nil && watch.bundle.config != nil {
					if path, ok := workerDevConfigFile(watch.dev.watch.root, watch.dev.envName); ok {
						configPath = path
						lastConfigInfo, _ = os.Stat(configPath)
						configTicker := time.NewTicker(devConfigPollInterval)
						stopConfigTicker = configTicker.Stop
						configTicks = configTicker.C
					}
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
	if watch.noteStoppedWorkers() {
		if err := watch.replace(ctx, watch.bundle, nil); err != nil {
			fmt.Fprintf(os.Stderr, "worker restart failed: %v\n", err)
			retry.Reset(time.Second)
		} else if watch.devControl != nil {
			watch.devControl.publish(watch.dev.applied, watch.bundle, watch.generation, nil)
		}
	}
}

func (watch *workerWatcher) noteStoppedWorkers() bool {
	stopped := false
	for _, process := range watch.active {
		select {
		case <-process.done:
			stopped = true
			if _, reported := watch.faults[process]; watch.app != nil && !reported {
				watch.faults[process] = struct{}{}
				module, _, _ := strings.Cut(process.program.Service, "/service/")
				recordDevWorkerFault(watch.app, watch.generation, module, process.program.Runtime, process.cmd.ProcessState)
			}
		default:
		}
	}
	return stopped
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

func (watch *workerWatcher) refresh(ctx context.Context, current workerInputs, detected time.Time) (applied workerInputs, returnErr error) {
	paths := changedWorkerInputs(watch.dev.applied, current)
	if len(paths) == 0 {
		return current, nil
	}
	started := time.Now()
	edit := watch.dev.classify(paths)
	prevDeps := watch.dev.jsDependencies()
	nextDeps := prevDeps
	appliedCurrent := current
	timing := workerRebuildTiming{
		Declarations: edit.declarations,
		DetectMS:     workerMilliseconds(started.Sub(detected)), Rebuilt: []string{},
		RebuiltModules: []string{}, SkippedModules: []string{},
	}
	defer func() {
		if returnErr != nil && !slices.Equal(prevDeps, nextDeps) {
			watch.dev.watch.setExplicit(prevDeps, false)
		}
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
			watch.devControl.publish(appliedCurrent, watch.bundle, watch.generation, &timing)
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
	candidate.js = slices.Clone(candidate.js)
	candidate.rust.artifacts = maps.Clone(candidate.rust.artifacts)
	bundle, err := candidate.rebuild(ctx, watch.bundle, current, paths, &timing)
	timing.CompileMS = workerMilliseconds(time.Since(started))
	if err != nil {
		return nil, err
	}
	nextDeps = candidate.jsDependencies()
	if !slices.Equal(prevDeps, nextDeps) {
		appliedCurrent, err = reconcileExplicitJSDependencies(watch.dev.watch, current, prevDeps, nextDeps)
		if err != nil {
			return nil, err
		}
	}
	dbFingerprint, err := candidate.verifyDatabaseSetup(ctx)
	if err != nil {
		return nil, err
	}
	candidate.dbSetupFingerprint = dbFingerprint
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
	prevHostConfig := watch.bundle.hostConfigSnapshot()
	prevWorkerConfig := watch.bundle.Host.WorkerConfig
	if bundle.config != nil {
		workerruntime.UpdateWorkerDevelopmentConfig(bundle.hostConfigSnapshot(), bundle.Host.WorkerConfig)
	}
	err = watch.replace(ctx, bundle, func() error { return candidate.verify(appliedCurrent) })
	timing.ReadyMS = workerMilliseconds(time.Since(readyStarted))
	if err != nil {
		if bundle.config != nil {
			workerruntime.UpdateWorkerDevelopmentConfig(prevHostConfig, prevWorkerConfig)
		}
		return nil, err
	}
	candidate.applied = appliedCurrent
	candidate.ensureAssistantCompiled().Store(candidate.compiled)
	*watch.dev = candidate
	return appliedCurrent, nil
}

func (watch *workerWatcher) applyConfigurationChange(ctx context.Context) {
	if watch.dev == nil || watch.bundle.config == nil {
		return
	}
	_, cfg, err := discoverConfiguredApp(watch.dev.watch.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration reload failed: %v\n", err)
		return
	}
	nextBundle := watch.bundle
	nextBundle.Programs = slices.Clone(watch.bundle.Programs)
	if err := nextBundle.resolveDevelopmentConfig(ctx, watch.dev.compiled, cfg, watch.dev.envName); err != nil {
		fmt.Fprintf(os.Stderr, "configuration reload failed: %v\n", err)
		return
	}
	nextBundle.setWorkerConfig()
	if watch.bundle.config != nil && nextBundle.config != nil && nextBundle.config.Revision == watch.bundle.config.Revision {
		return
	}
	prevHostConfig := watch.bundle.hostConfigSnapshot()
	prevWorkerConfig := watch.bundle.Host.WorkerConfig
	workerruntime.UpdateWorkerDevelopmentConfig(nextBundle.hostConfigSnapshot(), nextBundle.Host.WorkerConfig)
	if err := watch.replace(ctx, nextBundle, nil); err != nil {
		workerruntime.UpdateWorkerDevelopmentConfig(prevHostConfig, prevWorkerConfig)
		fmt.Fprintf(os.Stderr, "configuration apply failed: %v\n", err)
		return
	}
	if watch.devControl != nil {
		watch.devControl.publish(watch.dev.applied, watch.bundle, watch.generation, nil)
	}
}

func (watch *workerWatcher) replace(ctx context.Context, bundle workerBundle, verify func() error) error {
	if ctx.Err() == nil {
		watch.noteStoppedWorkers()
	}
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
	watch.all = slices.DeleteFunc(watch.all, func(process *workerProcess) bool {
		if next[process.program.Process] == process {
			return false
		}
		select {
		case <-process.done:
			return true
		default:
			return false
		}
	})
	for name, process := range watch.active {
		if next[name] != process {
			delete(watch.faults, process)
		}
	}
	watch.generations.add(watch.sequence, next)
	if watch.dev != nil {
		pruneJSWorkersByPaths(watch.dev.watch.root, watch.generations.binaries())
	}
	previous := watch.generation
	watch.generation, watch.active, watch.bundle = watch.sequence, next, bundle
	for _, process := range started {
		watch.observeExit(ctx, process)
	}
	watch.retirements.Add(1)
	go func() {
		defer watch.retirements.Done()
		watch.retire(ctx, previous)
	}()
	fmt.Fprintf(os.Stderr, "application generation %d ready (%d worker replacements)\n", watch.generation, len(started))
	return nil
}

func reconcileExplicitJSDependencies(watch *workerInputWatch, current workerInputs, prevDeps, nextDeps []string) (workerInputs, error) {
	updated := maps.Clone(current)
	watch.setExplicit(nextDeps, true)
	for _, dep := range prevDeps {
		if !slices.Contains(nextDeps, dep) && watch.skippedByWalk(dep) {
			delete(updated, dep)
		}
	}
	for _, dep := range nextDeps {
		if !slices.Contains(prevDeps, dep) {
			if _, exists := updated[dep]; !exists {
				if err := watch.scanExplicitFile(dep, updated, false); err != nil {
					return nil, err
				}
			}
		}
	}
	return updated, nil
}

func workerStillMatches(process *workerProcess, program workerProgram) bool {
	if process == nil || process.program.Digest != program.Digest || process.program.Identity != program.Identity || process.program.Runtime != program.Runtime {
		return false
	}
	if workerConfigIdentity(process.program.config) != workerConfigIdentity(program.config) {
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

func workerControlRetireGeneration(ctx context.Context, client *http.Client, token, path string) error {
	err := workerControlRequest(ctx, client, token, http.MethodDelete, path, nil, http.StatusNoContent)
	if err == nil || strings.HasPrefix(err.Error(), "worker Host HTTP 404:") {
		return nil
	}
	return err
}

func (watch *workerWatcher) retire(ctx context.Context, generation uint64) {
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	drain, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := workerControlRetireGeneration(drain, watch.client, watch.token, path); err == nil {
			stopWorkers(watch.generations.remove(generation))
			return
		}
		select {
		case <-drain.Done():
			force, cancelForce := context.WithTimeout(ctx, time.Second)
			defer cancelForce()
			if err := workerControlRetireGeneration(force, watch.client, watch.token, path+"?force=true"); err == nil {
				stopWorkers(watch.generations.remove(generation))
			}
			return
		case <-ticker.C:
		}
	}
}

// workerGenerations owns the references to processes in published generations,
// including generations still draining on the Host.
type workerGenerations struct {
	mu        sync.Mutex
	instances map[uint64]map[string]*workerProcess
	refs      map[*workerProcess]int
}

func (g *workerGenerations) add(number uint64, processes map[string]*workerProcess) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.instances == nil {
		g.instances = make(map[uint64]map[string]*workerProcess)
		g.refs = make(map[*workerProcess]int)
	}
	g.instances[number] = processes
	for _, process := range processes {
		g.refs[process]++
	}
}

func (g *workerGenerations) remove(number uint64) []*workerProcess {
	g.mu.Lock()
	defer g.mu.Unlock()
	var stopped []*workerProcess
	for _, process := range g.instances[number] {
		g.refs[process]--
		if g.refs[process] == 0 {
			delete(g.refs, process)
			stopped = append(stopped, process)
		}
	}
	delete(g.instances, number)
	return stopped
}

func (g *workerGenerations) binaries() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	binaries := make([]string, 0, len(g.refs))
	for process := range g.refs {
		if process.program.Binary != "" {
			binaries = append(binaries, process.program.Binary)
		}
	}
	return binaries
}

func stopWorkers(processes []*workerProcess) {
	for _, process := range processes {
		process.stop()
	}
}
