package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	devProcessRetireTimeout  = 30 * time.Second
	devProcessRetireInterval = 20 * time.Millisecond
	devProcessControlTimeout = 5 * time.Second
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
// process-model session. Activations are serialized; retirement of a replaced
// generation runs in the background once the host reports it drained.
type devProcessModel struct {
	mu         sync.Mutex
	token      string
	socketDir  string
	linkPath   string
	dispatch   string
	sequence   int
	generation uint64
	contract   string
	host       *devProcessInstance
	services   map[string]*devProcessInstance
	retiring   map[*devProcessInstance]bool
	control    *http.Client
}

type devProcessInstance struct {
	process build.DevelopmentProcess
	socket  string
	app     *runningApp
	stopped bool
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
	model := &devProcessModel{
		token: token + second, socketDir: socketDir, dispatch: filepath.Join(socketDir, "d.sock"),
		services: map[string]*devProcessInstance{}, retiring: map[*devProcessInstance]bool{},
	}
	model.linkPath = filepath.Join(socketDir, "process-link.json")
	link, err := json.Marshal(map[string]any{"token": model.token, "dispatch": map[string]string{"network": "unix", "address": model.dispatch}})
	if err != nil {
		return nil, err
	}
	if err := writePrivateProcessLink(model.linkPath, link); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	model.control = &http.Client{Timeout: devProcessControlTimeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", model.dispatch)
		},
		DisableCompression: true,
	}}
	s.processes = model
	return model, nil
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
// new sockets, the next generation is published, and the replaced instances
// retire after the host reports their generation drained.
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
		if environment, err = s.prepareRuntimeEnvironment(ctx, result.Contract); err != nil {
			return nil, false, err
		}
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	s.mu.RLock()
	host := s.current
	s.mu.RUnlock()
	contract := result.Contract.Manifest.ContractRevision
	if host != nil && model.host != nil && model.host.app == host && model.contract == contract && model.host.process.Identity == set.Host.Identity {
		return host, true, s.replaceDevServiceProcesses(ctx, model, set, s.appChildEnvironment(result, environment))
	}
	var stage *assistantStage
	if s.assistants != nil {
		if earlyAssistants == nil {
			s.assistants.lifecycle.Lock()
			defer s.assistants.lifecycle.Unlock()
		}
		previousStage := s.assistants.captureStage()
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
	current, err := s.startDevProcessGeneration(ctx, model, set, contract, result, environment, stage)
	return current, host != nil, err
}

func (s *devSupervisor) startDevProcessGeneration(ctx context.Context, model *devProcessModel, set *build.DevelopmentProcessSet, contract string, result *build.Result, environment *devRuntimeEnvironment, stage *assistantStage) (*runningApp, error) {
	previous := s.detachCurrentApp()
	stopErr := s.stopDevProcessInstances(model, previous, true)
	if stopErr != nil {
		return nil, fmt.Errorf("stop previous process generation: %w", stopErr)
	}
	if s.assistants != nil {
		// Assistant descriptors (MCP listeners and bridge secrets) change only
		// after the previous host has stopped, as for the application process.
		if err := s.console.Phase("Activating assistant runtimes", func() error { return s.assistants.activateStage(ctx, stage) }); err != nil {
			return nil, err
		}
		setAssistantImplementationWatch(s.root, assistantDefinitionsFromResult(result.Contract, s.root))
		s.refreshAssistantRuntimeConfig()
	}
	base := s.appChildEnvironment(result, environment)
	started, err := s.startDevServiceInstances(ctx, model, set.Services, base)
	if err != nil {
		return nil, err
	}
	hostInstance := &devProcessInstance{process: set.Host, socket: s.backend.normalized().Addr}
	hostEnv := append(append([]string(nil), base...), "SCENERY_PROCESS_LINK="+model.linkPath)
	if err := s.startDevProcessInstance(ctx, hostInstance, "host", hostEnv, s.backend); err != nil {
		markDevProcessesStopped(started)
		_ = s.stopInstances(started, currentDevProcessCommands(model))
		return nil, err
	}
	model.host, model.contract, model.generation = hostInstance, contract, 0
	model.services = map[string]*devProcessInstance{}
	for _, instance := range started {
		model.services[instance.process.Name] = instance
	}
	if err := s.publishDevProcessGeneration(ctx, model, set); err != nil {
		_ = s.stopDevProcessInstances(model, hostInstance.app, true)
		return nil, err
	}
	s.mu.Lock()
	s.current = hostInstance.app
	s.mu.Unlock()
	go func() {
		<-hostInstance.app.process.Done
		s.handleExit(context.Background(), hostInstance.app)
	}()
	if s.assistants != nil {
		_ = s.console.Phase("Starting prepared assistant runtimes", func() error { return s.assistants.StartPrepared(ctx) })
		s.refreshAssistantRuntimeConfig()
	}
	return hostInstance.app, nil
}

func (s *devSupervisor) replaceDevServiceProcesses(ctx context.Context, model *devProcessModel, set *build.DevelopmentProcessSet, base []string) error {
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
	started, err := s.startDevServiceInstances(ctx, model, changed, base)
	if err != nil {
		return err
	}
	previous := model.services
	next := make(map[string]*devProcessInstance, len(previous))
	for name, instance := range previous {
		next[name] = instance
	}
	for _, instance := range started {
		next[instance.process.Name] = instance
	}
	previousGeneration := model.generation
	model.services = next
	if err := s.publishDevProcessGeneration(ctx, model, set); err != nil {
		model.services = previous
		markDevProcessesStopped(started)
		_ = s.stopInstances(started, currentDevProcessCommands(model))
		return err
	}
	var replaced []*devProcessInstance
	for _, instance := range started {
		if old := previous[instance.process.Name]; old != nil {
			replaced = append(replaced, old)
			model.retiring[old] = true
		}
	}
	go s.retireDevProcessGeneration(model, previousGeneration, replaced)
	return nil
}

func (s *devSupervisor) startDevServiceInstances(ctx context.Context, model *devProcessModel, processes []build.DevelopmentProcess, base []string) ([]*devProcessInstance, error) {
	instances := make([]*devProcessInstance, len(processes))
	errs := make([]error, len(processes))
	var wg sync.WaitGroup
	for index, process := range processes {
		model.sequence++
		instance := &devProcessInstance{process: process, socket: filepath.Join(model.socketDir, "s"+strconv.Itoa(model.sequence)+".sock")}
		instances[index] = instance
		env := append(envWithoutKeys(base, "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR"),
			"SCENERY_LISTEN_NETWORK=unix", "SCENERY_LISTEN_ADDR="+instance.socket, "SCENERY_PROCESS_LINK="+model.linkPath)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[index] = s.startDevProcessInstance(ctx, instance, "service:"+process.Name, env, devBackend{Network: "unix", Addr: instance.socket})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		markDevProcessesStopped(instances)
		_ = s.stopInstances(instances, currentDevProcessCommands(model))
		return nil, err
	}
	for _, instance := range instances {
		go s.watchDevServiceInstance(model, instance)
	}
	return instances, nil
}

func (s *devSupervisor) startDevProcessInstance(ctx context.Context, instance *devProcessInstance, name string, env []string, backend devBackend) error {
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
	if backend.Network == "unix" {
		if err := os.Remove(backend.Addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	started = time.Now()
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

func (s *devSupervisor) publishDevProcessGeneration(ctx context.Context, model *devProcessModel, set *build.DevelopmentProcessSet) error {
	type identity struct {
		ContractRevision       string `json:"contract_revision"`
		ImplementationRevision string `json:"implementation_revision"`
		BuildInputDigest       string `json:"build_input_digest"`
		GoTarget               string `json:"go_target"`
	}
	type instance struct {
		Network  string   `json:"network"`
		Address  string   `json:"address"`
		PID      int      `json:"pid"`
		Identity identity `json:"identity"`
	}
	manifest := struct {
		Generation       uint64              `json:"generation"`
		ContractRevision string              `json:"contract_revision"`
		Processes        map[string]instance `json:"processes"`
		Bindings         map[string]string   `json:"bindings"`
	}{Generation: model.generation + 1, ContractRevision: model.contract, Processes: map[string]instance{}, Bindings: set.BindingOwners}
	for name, service := range model.services {
		pid, _ := strconv.Atoi(service.app.pid)
		value := service.process.Identity
		manifest.Processes[name] = instance{Network: "unix", Address: service.socket, PID: pid, Identity: identity{value.ContractRevision, value.ImplementationRevision, value.BuildInputDigest, value.GoTarget}}
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	started := time.Now()
	_, err = model.request(ctx, http.MethodPut, "/__scenery/process/v1/generations", body, http.StatusNoContent)
	build.RecordStep(ctx, build.Step{Name: "process.publish", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "host_generation_manifest", OK: err == nil, Actions: len(manifest.Processes)})
	if err != nil {
		return fmt.Errorf("publish process generation %d: %w", manifest.Generation, err)
	}
	model.generation = manifest.Generation
	return nil
}

// retireDevProcessGeneration waits until the host reports no work pinned to a
// replaced generation, retires it, and stops the instances it alone used.
func (s *devSupervisor) retireDevProcessGeneration(model *devProcessModel, generation uint64, replaced []*devProcessInstance) {
	// Replaced instances stop schedules, event consumers and durable acquisition
	// at once; requests and calls pinned to their generation still complete.
	for _, instance := range replaced {
		if err := model.drain(instance.socket); err != nil && s.console != nil {
			s.console.Event("process.drain_failed", map[string]any{"service_process": instance.process.Name, "pid": instance.app.pid, "error": err.Error()})
		}
	}
	deadline := time.Now().Add(devProcessRetireTimeout)
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	for {
		status, err := model.request(context.Background(), http.MethodDelete, path, nil, 0)
		if err == nil && (status == http.StatusNoContent || status == http.StatusNotFound) || time.Now().After(deadline) {
			break
		}
		time.Sleep(devProcessRetireInterval)
	}
	model.mu.Lock()
	for _, instance := range replaced {
		delete(model.retiring, instance)
	}
	markDevProcessesStopped(replaced)
	inUse := currentDevProcessCommands(model)
	model.mu.Unlock()
	_ = s.stopInstances(replaced, inUse)
}

func (model *devProcessModel) drain(socket string) error {
	dialer := &net.Dialer{}
	client := &http.Client{Timeout: devProcessRetireTimeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}}
	defer client.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodPost, "http://scenery-process/__scenery/process/v1/drain", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+model.token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("drain answered HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func (model *devProcessModel) request(ctx context.Context, method, path string, body []byte, want int) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://scenery-host"+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+model.token)
	response, err := model.control.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if want != 0 && response.StatusCode != want {
		return response.StatusCode, fmt.Errorf("host answered HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return response.StatusCode, nil
}

func (s *devSupervisor) watchDevServiceInstance(model *devProcessModel, instance *devProcessInstance) {
	<-instance.app.process.Done
	model.mu.Lock()
	current := model.services[instance.process.Name] == instance && !instance.stopped
	instance.stopped = true
	model.mu.Unlock()
	if current && s.console != nil {
		s.console.Event("process.stop", map[string]any{"pid": instance.app.pid, "service_process": instance.process.Name, "output": strings.TrimSpace(instance.app.output.String())})
	}
}

// stopDevProcessInstances stops the host and every service instance of the
// model; the caller holds model.mu or owns the model exclusively.
func (s *devSupervisor) stopDevProcessInstances(model *devProcessModel, host *runningApp, releaseHost bool) error {
	var instances []*devProcessInstance
	for _, instance := range model.services {
		instances = append(instances, instance)
	}
	for instance := range model.retiring {
		instances = append(instances, instance)
	}
	model.services, model.retiring, model.host = map[string]*devProcessInstance{}, map[*devProcessInstance]bool{}, nil
	markDevProcessesStopped(instances)
	var stopErrs []error
	if host != nil {
		stopErrs = append(stopErrs, host.stop())
		if releaseHost {
			s.releaseUnusedAppBinary(host.launch)
		}
	}
	stopErrs = append(stopErrs, s.stopInstances(instances, currentDevProcessCommands(model)))
	return errors.Join(stopErrs...)
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

// currentDevProcessCommands lists the session executables current service
// instances run; the caller holds model.mu.
func currentDevProcessCommands(model *devProcessModel) map[string]bool {
	inUse := map[string]bool{}
	for _, current := range model.services {
		if current.app != nil && current.app.launch != nil {
			inUse[current.app.launch.request.Command] = true
		}
	}
	return inUse
}

// stopInstances stops instances already marked stopped and releases retained
// session executables that no current instance in inUse still runs.
func (s *devSupervisor) stopInstances(instances []*devProcessInstance, inUse map[string]bool) error {
	var wg sync.WaitGroup
	stopErrs := make([]error, len(instances))
	for index, instance := range instances {
		if instance == nil || instance.app == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			stopErrs[index] = instance.app.stop()
		}()
	}
	wg.Wait()
	for _, instance := range instances {
		if instance != nil && instance.app != nil && instance.app.launch != nil && !inUse[instance.app.launch.request.Command] {
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
	defer model.mu.Unlock()
	err := s.stopDevProcessInstances(model, nil, false)
	_ = os.Remove(model.linkPath)
	return err
}
