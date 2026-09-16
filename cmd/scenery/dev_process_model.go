package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime"
)

// devProcessModelEnv selects the process-per-service development model while
// Plan 0200 rolls it out; the default remains one application executable.
const devProcessModelEnv = "SCENERY_DEV_PROCESS_MODEL"

const (
	// devProcessRetireTimeout bounds how long a replaced generation may keep
	// pinned work before the supervisor forces its retirement.
	devProcessRetireTimeout  = 30 * time.Second
	devProcessRetireInterval = 20 * time.Millisecond
	devProcessControlTimeout = 5 * time.Second
	// devProcessBackgroundTimeout bounds one activation or drain request.
	devProcessBackgroundTimeout = 5 * time.Second
	// devProcessActivationBackoff is the first delay before an unconfirmed
	// activation is repeated; later attempts double up to the request timeout.
	devProcessActivationBackoff = 250 * time.Millisecond
	// A crashed service process restarts from its own verified executable
	// within this budget; beyond it the service stays degraded until its next
	// build, so a failing constructor cannot become a restart storm.
	devProcessRestartBudget  = 3
	devProcessRestartWindow  = time.Minute
	devProcessRestartBackoff = 250 * time.Millisecond
)

func devProcessModelSelected() (bool, error) {
	switch value := strings.TrimSpace(envpolicy.Get(devProcessModelEnv)); value {
	case "", "application":
		return false, nil
	case "service":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported %s %q; use application or service", devProcessModelEnv, value)
	}
}

// devProcessModel owns the host and service process instances of a
// process-model session; mu serializes activations and guards the model, and
// every critical section that changes the model publishes its status before
// releasing mu (see unlock). Each host incarnation has
// its own link (link file and dispatch socket). Service instances start without
// background work; once a generation is published, the instances it replaced
// are drained and its new instances are activated, so only instances of the
// current generation acquire background work. A replaced generation retires
// when the host reports no pinned work or when its retirement is forced, and an
// instance stops only when neither the current services nor any generation the
// host still retains name it.
type devProcessModel struct {
	mu         sync.Mutex
	token      string
	socketDir  string
	sequence   int
	epoch      uint64
	link       *devProcessLink
	generation uint64
	contract   string
	bindings   map[string]string
	host       *devProcessInstance
	services   map[string]*devProcessInstance
	// retained names the instances of every published generation the current
	// host incarnation still retains, the current generation included.
	retained map[uint64]map[string]*devProcessInstance
	// restarts records the recent crash recoveries of each service process and
	// degraded names the services whose budget is exhausted.
	restarts map[string][]time.Time
	degraded map[string]string
	// activationBackoff overrides devProcessActivationBackoff when positive.
	activationBackoff time.Duration
	// statusMu guards status, which readers use instead of mu.
	statusMu sync.Mutex
	status   devProcessStatus
}

// devProcessLink is the wiring of one host incarnation.
type devProcessLink struct {
	epoch    uint64
	token    string
	path     string
	dispatch string
	control  *http.Client
}

type devProcessInstance struct {
	process build.DevelopmentProcess
	socket  string
	// request is the retained session executable and exact environment of a
	// preflighted instance that has not started yet.
	request *devProcessStartRequest
	app     *runningApp
	stopped bool
	// activation is the error of an activation the instance has not confirmed;
	// reconciling reports that a reconciler repeats it. Both are guarded by
	// model.mu.
	activation  string
	reconciling bool
}

func (s *devSupervisor) ensureDevProcessModel() (*devProcessModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.processes != nil {
		return s.processes, nil
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	second, err := randomToken()
	if err != nil {
		return nil, err
	}
	socketDir := ""
	if backend := s.backend.normalized(); backend.Network == "unix" {
		socketDir = filepath.Dir(backend.Addr)
	} else if socketDir, err = os.MkdirTemp("", "scp"); err != nil {
		return nil, err
	}
	s.processes = &devProcessModel{
		token: token + second, socketDir: socketDir,
		services: map[string]*devProcessInstance{}, retained: map[uint64]map[string]*devProcessInstance{},
		restarts: map[string][]time.Time{}, degraded: map[string]string{},
	}
	return s.processes, nil
}

// newLink writes the private link file of the next host incarnation.
func (model *devProcessModel) newLink() (*devProcessLink, error) {
	epoch := model.epoch + 1
	link := &devProcessLink{
		epoch:    epoch,
		token:    model.token,
		path:     filepath.Join(model.socketDir, "process-link-"+strconv.FormatUint(epoch, 10)+".json"),
		dispatch: filepath.Join(model.socketDir, "d"+strconv.FormatUint(epoch, 10)+".sock"),
	}
	data, err := json.Marshal(map[string]any{"token": model.token, "dispatch": map[string]string{"network": "unix", "address": link.dispatch}})
	if err != nil {
		return nil, err
	}
	if err := writePrivateProcessLink(link.path, data); err != nil {
		return nil, err
	}
	model.epoch = epoch
	dialer := &net.Dialer{}
	link.control = &http.Client{Timeout: devProcessControlTimeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", link.dispatch)
		},
		DisableCompression: true,
	}}
	return link, nil
}

func (link *devProcessLink) remove() {
	if link == nil {
		return
	}
	link.control.CloseIdleConnections()
	_ = os.Remove(link.path)
	_ = os.Remove(link.dispatch)
}

func writePrivateProcessLink(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".process-link-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// activateDevProcesses makes the built process set the published generation.
// A changed contract or host identity, or a missing host, starts a complete
// generation; otherwise only services whose linked identity changed start on
// new sockets and the next generation is published.
func (s *devSupervisor) activateDevProcesses(ctx context.Context, plan *devRuntimePlan, earlyAssistants *assistantStageAttempt) (*runningApp, bool, error) {
	result, set := plan.Result, plan.Processes
	for _, resource := range result.Contract.Manifest.Resources {
		if resource.Kind == "scenery.event-emission" || resource.Kind == "scenery.binding" && resource.Spec["protocol"] == "event" {
			return nil, false, errors.New("the process-per-service development model does not run event consumers or emissions yet")
		}
	}
	model, err := s.ensureDevProcessModel()
	if err != nil {
		return nil, false, err
	}
	environment := plan.Environment
	if environment == nil {
		environmentStarted := time.Now()
		environment, err = s.prepareRuntimeEnvironment(ctx, result.Contract)
		build.RecordStep(ctx, build.Step{Name: "supervisor.environment", StartedAt: environmentStarted, Duration: time.Since(environmentStarted), Cache: "not_applicable", Reason: "runtime_capabilities", OK: err == nil})
		if err != nil {
			return nil, false, err
		}
	}
	model.mu.Lock()
	defer model.unlock()
	s.mu.RLock()
	host := s.current
	s.mu.RUnlock()
	contract := result.Contract.Manifest.ContractRevision
	if host != nil && model.host != nil && model.host.app == host && model.contract == contract && model.host.process.Identity == set.Host.Identity {
		return host, true, s.replaceDevServiceProcesses(ctx, model, set, s.appChildEnvironment(result, environment), plan.Prepared)
	}
	var stage, previousStage *assistantStage
	if s.assistants != nil {
		if earlyAssistants == nil {
			s.assistants.lifecycle.Lock()
			defer s.assistants.lifecycle.Unlock()
		}
		previousStage = s.assistants.captureStage()
		defer s.assistants.releaseStage(previousStage)
		var stageErr error
		if earlyAssistants != nil && earlyAssistants.matches(result.Contract) {
			stage, stageErr = earlyAssistants.wait()
		} else {
			if earlyAssistants != nil {
				// A source change during startup may have forced a fresh graph.
				earlyAssistants.release()
			}
			stage, stageErr = s.assistants.stage(ctx, result.Contract)
		}
		defer s.assistants.releaseStage(stage)
		if stageErr != nil && host != nil {
			return nil, true, stageErr
		}
	}
	current, err := s.startDevProcessGeneration(ctx, model, set, result, environment, stage, previousStage, plan.Prepared)
	return current, host != nil, err
}

// devProcessReplacement orders a complete generation replacement. Candidate
// services start while the previous generation serves; the previous host stops
// only once they are ready, because the candidate host needs its listener and
// assistant descriptors. A candidate host that cannot start or accept its
// generation is abandoned and the previous host restored; commit runs only
// after the candidate generation is published.
type devProcessReplacement struct {
	startServices func(context.Context) error
	stopPrevious  func() error
	startHost     func(context.Context) error
	// abandon stops the candidate processes and restores the previous model.
	abandon func() error
	// restore restarts the previous host; it reports false when there is none.
	restore func(context.Context) (bool, error)
	commit  func(context.Context)
}

func (replacement devProcessReplacement) run(ctx context.Context) (bool, error) {
	if err := replacement.startServices(ctx); err != nil {
		return false, err
	}
	if err := replacement.stopPrevious(); err != nil {
		return false, errors.Join(fmt.Errorf("stop previous process host before replacement: %w", err), replacement.abandon())
	}
	err := replacement.startHost(ctx)
	if err == nil {
		replacement.commit(ctx)
		return false, nil
	}
	if abandonErr := replacement.abandon(); abandonErr != nil {
		return false, errors.Join(err, fmt.Errorf("candidate shutdown is unconfirmed; rollback refused: %w", abandonErr))
	}
	if ctx.Err() != nil {
		return false, err
	}
	restored, restoreErr := replacement.restore(ctx)
	if restoreErr != nil {
		return false, errors.Join(err, fmt.Errorf("restore previous process generation: %w", restoreErr))
	}
	if !restored {
		return false, err
	}
	return true, fmt.Errorf("process generation startup failed; restored the previous generation: %w", err)
}

// startDevProcessGeneration starts a complete generation with a new host
// incarnation, restoring the previous generation when the new host fails.
func (s *devSupervisor) startDevProcessGeneration(ctx context.Context, model *devProcessModel, set *build.DevelopmentProcessSet, result *build.Result, environment *devRuntimeEnvironment, stage, previousStage *assistantStage, prepared *devProcessPreparation) (*runningApp, error) {
	link, err := model.newLink()
	if err != nil {
		return nil, err
	}
	base := s.appChildEnvironment(result, environment)
	previous := devProcessState{link: model.link, generation: model.generation, contract: model.contract, bindings: model.bindings, host: model.host, services: model.services, retained: model.retained}
	hostInstance := &devProcessInstance{process: set.Host, socket: s.backend.normalized().Addr}
	var started []*devProcessInstance
	var previousHost *runningApp
	previousStopped := false
	replacement := devProcessReplacement{
		startServices: func(ctx context.Context) error {
			var err error
			if started, err = s.startDevServiceInstances(ctx, model, set.Services, base, link, prepared); err != nil {
				link.remove()
			}
			return err
		},
		stopPrevious: func() error {
			previousHost = s.detachCurrentApp()
			if previousHost == nil {
				previousStopped = true
				return nil
			}
			if err := previousHost.stop(); err != nil {
				s.mu.Lock()
				s.current = previousHost
				s.mu.Unlock()
				return err
			}
			previousStopped = true
			return nil
		},
		startHost: func(ctx context.Context) error {
			if s.assistants != nil {
				// Assistant descriptors (MCP listeners and bridge secrets) change
				// only after the previous host has stopped.
				if err := s.console.Phase("Activating assistant runtimes", func() error { return s.assistants.activateStage(ctx, stage) }); err != nil {
					return err
				}
				setAssistantImplementationWatch(s.root, assistantDefinitionsFromResult(result.Contract, s.root))
				s.refreshAssistantRuntimeConfig()
			}
			hostEnv := append(append([]string(nil), base...), "SCENERY_PROCESS_LINK="+link.path)
			if err := s.startDevProcessInstance(ctx, hostInstance, "host", hostEnv, s.backend); err != nil {
				return err
			}
			services := make(map[string]*devProcessInstance, len(started))
			for _, instance := range started {
				services[instance.process.Name] = instance
			}
			model.link, model.generation, model.contract, model.bindings = link, 0, result.Contract.Manifest.ContractRevision, maps.Clone(set.BindingOwners)
			model.host, model.services, model.retained = hostInstance, services, map[uint64]map[string]*devProcessInstance{}
			return s.publishDevProcessGeneration(ctx, model)
		},
		abandon: func() error {
			candidates := append([]*devProcessInstance(nil), started...)
			if hostInstance.app != nil {
				candidates = append(candidates, hostInstance)
			}
			model.link, model.generation, model.contract, model.bindings = previous.link, previous.generation, previous.contract, previous.bindings
			model.host, model.services, model.retained = previous.host, previous.services, previous.retained
			if previousStopped {
				// The previous host's retained generations ended with it; its
				// current services wait to be republished.
				model.generation, model.retained = 0, map[uint64]map[string]*devProcessInstance{}
				for _, instance := range previous.instances() {
					if previous.services[instance.process.Name] != instance {
						candidates = append(candidates, instance)
					}
				}
			}
			markDevProcessesStopped(candidates)
			err := s.stopInstances(candidates, model.runningCommands())
			link.remove()
			return err
		},
		restore: func(ctx context.Context) (bool, error) {
			if previousHost == nil || previousHost.launch == nil || previous.host == nil {
				return false, nil
			}
			restored, err := s.restoreDevProcessHost(ctx, model, previousHost, previousStage)
			if err != nil {
				return false, err
			}
			s.writeProcessEvent(ctx, "process/rollback", map[string]any{"pid": restored.pid})
			return true, nil
		},
		commit: func(ctx context.Context) {
			// The previous instances stop before the new generation acquires
			// background work; the previous host, which alone reached them, has
			// already stopped.
			stale := previous.instances()
			markDevProcessesStopped(stale)
			s.mu.Lock()
			s.current = hostInstance.app
			s.mu.Unlock()
			_ = s.stopInstances(stale, model.runningCommands())
			s.activateDevProcessInstances(ctx, model, started)
			if previousHost != nil {
				s.releaseUnusedAppBinary(previousHost.launch)
			}
			previous.link.remove()
			go func() {
				<-hostInstance.app.process.Done
				s.handleExit(context.Background(), hostInstance.app)
			}()
			// Helpers that start next register the session, which reads the
			// published status while this activation still holds model.mu.
			model.publishStatus()
			if s.assistants != nil {
				_ = s.console.Phase("Starting prepared assistant runtimes", func() error { return s.assistants.StartPrepared(ctx) })
				s.refreshAssistantRuntimeConfig()
			}
		},
	}
	if _, err := replacement.run(ctx); err != nil {
		return nil, err
	}
	return hostInstance.app, nil
}

// restoreDevProcessHost restarts the previous host from its retained executable
// and environment and republishes the previous services as its generation.
func (s *devSupervisor) restoreDevProcessHost(ctx context.Context, model *devProcessModel, previous *runningApp, stage *assistantStage) (*runningApp, error) {
	if s.assistants != nil {
		if err := s.assistants.activateStage(ctx, stage); err != nil {
			return nil, err
		}
		s.refreshAssistantRuntimeConfig()
	}
	process, err := startDevManagedProcess(ctx, previous.launch.request)
	if err != nil {
		return nil, err
	}
	app := &runningApp{process: process, cmd: process.Cmd, pid: strconv.Itoa(process.PID), output: process.Tail, launch: previous.launch}
	backend := s.backend
	err = process.WaitReady(ctx, devProcessReadyRequest{Timeout: appStartupTimeout, Interval: appStartupPollInterval, Probe: func(context.Context) error {
		if backendAcceptsConnections(backend) {
			return nil
		}
		return fmt.Errorf("restored process host is not accepting connections on %s", backend.Addr)
	}})
	if err == nil {
		model.host.app, model.host.stopped = app, false
		err = s.publishDevProcessGeneration(ctx, model)
	}
	if err != nil {
		_ = app.stop()
		return nil, err
	}
	s.mu.Lock()
	s.current = app
	metadata, apiEncoding := s.status.Metadata, s.status.APIEncoding
	s.mu.Unlock()
	s.setRunning(app.pid, metadata, apiEncoding)
	go func() {
		<-process.Done
		s.handleExit(context.Background(), app)
	}()
	model.publishStatus()
	if s.assistants != nil {
		_ = s.console.Phase("Starting prepared assistant runtimes", func() error { return s.assistants.StartPrepared(ctx) })
		s.refreshAssistantRuntimeConfig()
	}
	return app, nil
}

func (s *devSupervisor) replaceDevServiceProcesses(ctx context.Context, model *devProcessModel, set *build.DevelopmentProcessSet, base []string, prepared *devProcessPreparation) error {
	var changed []build.DevelopmentProcess
	for _, process := range set.Services {
		current := model.services[process.Name]
		if current == nil || current.stopped || current.process.Identity != process.Identity {
			changed = append(changed, process)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	started, err := s.startDevServiceInstances(ctx, model, changed, base, model.link, prepared)
	if err != nil {
		return err
	}
	previous := model.services
	next := maps.Clone(previous)
	for _, instance := range started {
		next[instance.process.Name] = instance
	}
	previousGeneration := model.generation
	model.services = next
	if err := s.publishDevProcessGeneration(ctx, model); err != nil {
		model.services = previous
		markDevProcessesStopped(started)
		_ = s.stopInstances(started, model.runningCommands())
		return err
	}
	var replaced []*devProcessInstance
	for _, instance := range started {
		if old := previous[instance.process.Name]; old != nil {
			replaced = append(replaced, old)
		}
	}
	clearDevProcessRecovery(model, started)
	s.drainDevProcessInstances(ctx, model, replaced)
	s.activateDevProcessInstances(ctx, model, started)
	go s.retireDevProcessGeneration(model, model.link, previousGeneration)
	return nil
}

func (s *devSupervisor) startDevServiceInstances(ctx context.Context, model *devProcessModel, processes []build.DevelopmentProcess, base []string, link *devProcessLink, prepared *devProcessPreparation) ([]*devProcessInstance, error) {
	instances := make([]*devProcessInstance, len(processes))
	errs := make([]error, len(processes))
	for index, process := range processes {
		instance, err := prepared.instance(link, process)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			model.sequence++
			instance = &devProcessInstance{process: process, socket: filepath.Join(model.socketDir, "s"+strconv.Itoa(model.sequence)+".sock")}
		}
		instances[index] = instance
	}
	var wg sync.WaitGroup
	for index, process := range processes {
		instance := instances[index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[index] = s.startDevProcessInstance(ctx, instance, "service:"+process.Name, devProcessInstanceEnvironment(base, instance, link), devBackend{Network: "unix", Addr: instance.socket})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		markDevProcessesStopped(instances)
		_ = s.stopInstances(instances, model.runningCommands())
		return nil, err
	}
	for _, instance := range instances {
		go s.watchDevServiceInstance(model, instance)
	}
	return instances, nil
}

// prepareDevProcessInstance retains the session executable of an instance and
// proves its runtime identity with its exact start environment.
func (s *devSupervisor) prepareDevProcessInstance(ctx context.Context, instance *devProcessInstance, name string, env []string) error {
	step := func(stepName, reason string, started time.Time, err error) {
		build.RecordStep(ctx, build.Step{Name: stepName, StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: reason + "_" + instance.process.Name, OK: err == nil})
	}
	command := instance.process.Binary
	started := time.Now()
	retained, err := prepareSessionAppBinary(s.currentAgentSession(), command, instance.process.ArtifactDigest)
	step("process.retain", "session_executable", started, err)
	if err != nil {
		return err
	} else if retained != "" {
		command = retained
	}
	request := s.appProcessStartRequest(ctx, name, "scenery-"+strings.SplitN(name, ":", 2)[0], command, env)
	identity := instance.process.Identity
	started = time.Now()
	err = preflightProcessStart(ctx, request, func(data []byte) error { return validateDevProcessPreflight(data, instance.process.Name, identity) })
	step("process.preflight", "linked_identity", started, err)
	if err != nil {
		return err
	}
	instance.request = &request
	return nil
}

func (s *devSupervisor) startDevProcessInstance(ctx context.Context, instance *devProcessInstance, name string, env []string, backend devBackend) error {
	step := func(stepName, reason string, started time.Time, err error) {
		build.RecordStep(ctx, build.Step{Name: stepName, StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: reason + "_" + instance.process.Name, OK: err == nil})
	}
	if instance.request == nil {
		if err := s.prepareDevProcessInstance(ctx, instance, name, env); err != nil {
			return err
		}
	}
	request := *instance.request
	started := time.Now()
	if backend.Network == "unix" {
		if err := os.Remove(backend.Addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	process, err := startDevManagedProcess(ctx, request)
	if err != nil {
		step("process.start", "listener_ready", started, err)
		return err
	}
	instance.app = &runningApp{process: process, cmd: process.Cmd, pid: strconv.Itoa(process.PID), output: process.Tail, launch: &appStartPlan{request: request}}
	err = process.WaitReady(ctx, devProcessReadyRequest{Timeout: appStartupTimeout, Interval: appStartupPollInterval, Probe: func(context.Context) error {
		if backendAcceptsConnections(backend) {
			return nil
		}
		return fmt.Errorf("development process %s is not accepting connections on %s", instance.process.Name, backend.Addr)
	}})
	step("process.start", "listener_ready", started, err)
	if err != nil {
		_ = instance.app.stop()
		return err
	}
	return nil
}

func validateDevProcessPreflight(data []byte, name string, identity build.DevelopmentProcessIdentity) error {
	proof, err := runtime.DecodeRuntimePreflight(data)
	if err != nil {
		return fmt.Errorf("development process %s runtime handshake: %w", name, err)
	}
	if proof.RuntimeABI != runtime.ContractRuntimeABI || proof.ContractRevision != identity.ContractRevision || proof.ImplementationRevision != identity.ImplementationRevision ||
		proof.BuildInputDigest != identity.BuildInputDigest || proof.GoTarget != identity.GoTarget {
		return fmt.Errorf("development process %s does not match the supervisor's prepared build; the published generation was not replaced", name)
	}
	return nil
}

func (s *devSupervisor) watchDevServiceInstance(model *devProcessModel, instance *devProcessInstance) {
	<-instance.app.process.Done
	model.mu.Lock()
	current := model.services[instance.process.Name] == instance && !instance.stopped
	instance.stopped = true
	model.unlock()
	if !current {
		return
	}
	if s.console != nil {
		s.console.Event("process.stop", map[string]any{"pid": instance.app.pid, "service_process": instance.process.Name, "output": strings.TrimSpace(instance.app.output.String())})
	}
	s.recoverDevServiceInstance(model, instance)
}

// recoverDevServiceInstance restarts the exact executable of a service process
// that stopped on its own and publishes it as the next generation. A service
// that exhausts its restart budget stays degraded: the published generation
// keeps naming the process that is gone, so requests to it fail visibly until
// its next build replaces it.
func (s *devSupervisor) recoverDevServiceInstance(model *devProcessModel, crashed *devProcessInstance) {
	name := crashed.process.Name
	model.mu.Lock()
	restartable := model.services[name] == crashed && model.link != nil && crashed.request != nil
	allowed := restartable && model.allowRestart(name, time.Now())
	attempts := len(model.restarts[name])
	if restartable && !allowed {
		model.degraded[name] = "restart budget exhausted"
	}
	model.unlock()
	if !restartable {
		return
	}
	if !allowed {
		if s.console != nil {
			s.console.Event("process.degraded", map[string]any{"service_process": name, "restarts": devProcessRestartBudget, "window": devProcessRestartWindow.String()})
		}
		return
	}
	select {
	case <-time.After(time.Duration(attempts) * devProcessRestartBackoff):
	case <-s.ctx.Done():
		return
	}
	model.mu.Lock()
	defer model.unlock()
	if model.services[name] != crashed || model.link == nil {
		return
	}
	model.sequence++
	replacement := &devProcessInstance{process: crashed.process, socket: filepath.Join(model.socketDir, "s"+strconv.Itoa(model.sequence)+".sock")}
	environment := append(envWithoutKeys(crashed.request.Env, "SCENERY_LISTEN_ADDR"), "SCENERY_LISTEN_ADDR="+replacement.socket)
	if err := s.startDevProcessInstance(s.ctx, replacement, "service:"+name, environment, devBackend{Network: "unix", Addr: replacement.socket}); err != nil {
		model.degraded[name] = err.Error()
		if s.console != nil {
			s.console.Event("process.degraded", map[string]any{"service_process": name, "error": err.Error()})
		}
		return
	}
	go s.watchDevServiceInstance(model, replacement)
	previous, previousGeneration := model.services, model.generation
	next := maps.Clone(previous)
	next[name] = replacement
	model.services = next
	if err := s.publishDevProcessGeneration(s.ctx, model); err != nil {
		model.services = previous
		markDevProcessesStopped([]*devProcessInstance{replacement})
		_ = s.stopInstances([]*devProcessInstance{replacement}, model.runningCommands())
		model.degraded[name] = err.Error()
		return
	}
	s.activateDevProcessInstances(s.ctx, model, []*devProcessInstance{replacement})
	go s.retireDevProcessGeneration(model, model.link, previousGeneration)
	if s.console != nil {
		s.console.Event("process.restart", map[string]any{"service_process": name, "pid": replacement.app.pid, "stopped_pid": crashed.app.pid, "attempt": attempts})
	}
}

// allowRestart records one crash recovery of a service process and reports
// whether its budget still allows restarting it; the caller holds model.mu.
func (model *devProcessModel) allowRestart(name string, now time.Time) bool {
	kept := model.restarts[name][:0]
	for _, attempt := range model.restarts[name] {
		if now.Sub(attempt) < devProcessRestartWindow {
			kept = append(kept, attempt)
		}
	}
	model.restarts[name] = kept
	if len(kept) >= devProcessRestartBudget {
		return false
	}
	model.restarts[name] = append(kept, now)
	return true
}

// instances lists every service instance the state names once.
func (state devProcessState) instances() []*devProcessInstance {
	seen := map[*devProcessInstance]bool{}
	var instances []*devProcessInstance
	add := func(named map[string]*devProcessInstance) {
		for _, instance := range named {
			if instance != nil && !seen[instance] {
				seen[instance] = true
				instances = append(instances, instance)
			}
		}
	}
	add(state.services)
	for _, named := range state.retained {
		add(named)
	}
	return instances
}

// markDevProcessesStopped records an intentional stop before it happens, so
// the exit watcher does not report it; the caller holds model.mu.
func markDevProcessesStopped(instances []*devProcessInstance) {
	for _, instance := range instances {
		if instance != nil {
			instance.stopped = true
		}
	}
}

// runningCommands lists the session executables of instances that still run:
// current services and every retained generation; the caller holds model.mu.
func (model *devProcessModel) runningCommands() map[string]bool {
	inUse := map[string]bool{}
	state := devProcessState{services: model.services, retained: model.retained}
	for _, instance := range state.instances() {
		if !instance.stopped && instance.app != nil && instance.app.launch != nil {
			inUse[instance.app.launch.request.Command] = true
		}
	}
	return inUse
}

// stopInstances stops instances already marked stopped and releases retained
// session executables that no running instance in inUse still uses.
func (s *devSupervisor) stopInstances(instances []*devProcessInstance, inUse map[string]bool) error {
	var wg sync.WaitGroup
	stopErrs := make([]error, len(instances))
	for index, instance := range instances {
		if instance == nil || instance.app == nil {
			continue
		}
		wg.Go(func() {
			stopErrs[index] = instance.app.stop()
		})
	}
	wg.Wait()
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		// An instance that was killed rather than stopped leaves its listening
		// socket behind; the supervisor owns that path.
		if instance.socket != "" && instance.process.Name != build.DevelopmentProcessHost {
			_ = os.Remove(instance.socket)
		}
		if instance.app != nil && instance.app.launch != nil && !inUse[instance.app.launch.request.Command] {
			s.releaseUnusedAppBinary(instance.app.launch)
		}
	}
	return errors.Join(stopErrs...)
}

// closeDevProcesses stops every service process instance at session close; the
// host is the supervisor's current application process and stops with it.
func (s *devSupervisor) closeDevProcesses() error {
	s.mu.RLock()
	model := s.processes
	s.mu.RUnlock()
	if model == nil {
		return nil
	}
	model.mu.Lock()
	defer model.unlock()
	instances := devProcessState{services: model.services, retained: model.retained}.instances()
	model.services, model.retained, model.host = map[string]*devProcessInstance{}, map[uint64]map[string]*devProcessInstance{}, nil
	markDevProcessesStopped(instances)
	err := s.stopInstances(instances, nil)
	model.link.remove()
	model.link = nil
	return err
}

func mapValues(values map[string]*devProcessInstance) []*devProcessInstance {
	instances := make([]*devProcessInstance, 0, len(values))
	for _, instance := range values {
		instances = append(instances, instance)
	}
	return instances
}
