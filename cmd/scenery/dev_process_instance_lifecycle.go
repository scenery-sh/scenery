package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/runtime"
)

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
	model, err := s.ensureDevProcessModel()
	if err != nil {
		return err
	}
	proof := devProcessProofKey(instance.process, request.Command, request.Env)
	started = time.Now()
	if model.proofs.proven(proof) {
		build.RecordStep(ctx, build.Step{Name: "process.preflight", StartedAt: started, Duration: time.Since(started), Cache: "hit", Reason: "retained_proof_" + instance.process.Name, OK: true})
		instance.request = &request
		return nil
	}
	err = preflightProcessStart(ctx, request, func(data []byte) error { return validateDevProcessPreflight(data, instance.process.Name, identity) })
	step("process.preflight", "linked_identity", started, err)
	if err != nil {
		return err
	}
	model.proofs.remember(proof)
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
	request.PrivateInput = instance.config.privateInput()
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
	replacement := &devProcessInstance{process: crashed.process, socket: filepath.Join(model.socketDir, "s"+strconv.Itoa(model.sequence)+".sock"), config: crashed.config}
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
		model.degraded[name] = s.settleFailedDevProcessPublication(model, []*devProcessInstance{replacement}, err).Error()
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
