package main

// Publication, background-work transfer and retirement of process-model
// generations, and the preparation of replacement instances a build relinked.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
)

// devProcessPreparation retains and preflights the replacement instances of a
// build while the supervisor verifies the candidate, so activation only starts
// them. It belongs to one host incarnation; a complete replacement prepares its
// own instances.
type devProcessPreparation struct {
	link      *devProcessLink
	instances map[string]*devProcessInstance
	err       error
	done      chan struct{}
}

// beginDevProcessPreparation starts retaining and preflighting the service
// processes this build relinked.
func (s *devSupervisor) beginDevProcessPreparation(ctx context.Context, result *build.Result, environment *devRuntimeEnvironment, set *build.DevelopmentProcessSet) *devProcessPreparation {
	model, err := s.ensureDevProcessModel()
	if err != nil {
		return nil
	}
	model.mu.Lock()
	link := model.link
	preparation := &devProcessPreparation{link: link, instances: map[string]*devProcessInstance{}, done: make(chan struct{})}
	if link != nil {
		for _, process := range set.Services {
			if !slices.Contains(set.Rebuilt, process.Name) {
				continue
			}
			model.sequence++
			preparation.instances[process.Name] = &devProcessInstance{process: process, socket: filepath.Join(model.socketDir, "s"+strconv.Itoa(model.sequence)+".sock")}
		}
	}
	model.mu.Unlock()
	if link == nil || len(preparation.instances) == 0 {
		close(preparation.done)
		return preparation
	}
	base := s.appChildEnvironment(result, environment)
	go func() {
		defer close(preparation.done)
		var wg sync.WaitGroup
		errs := make([]error, 0, len(preparation.instances))
		var mu sync.Mutex
		for _, instance := range preparation.instances {
			wg.Go(func() {
				err := s.prepareDevProcessInstance(ctx, instance, "service:"+instance.process.Name, devProcessInstanceEnvironment(base, instance, link))
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			})
		}
		wg.Wait()
		preparation.err = errors.Join(errs...)
	}()
	return preparation
}

// instance reports the prepared instance of a process of this link, once its
// preparation has finished.
func (preparation *devProcessPreparation) instance(link *devProcessLink, process build.DevelopmentProcess) (*devProcessInstance, error) {
	if preparation == nil || preparation.link != link {
		return nil, nil
	}
	<-preparation.done
	if preparation.err != nil {
		return nil, preparation.err
	}
	instance := preparation.instances[process.Name]
	if instance == nil || instance.process.Identity != process.Identity || instance.process.Binary != process.Binary || instance.app != nil {
		return nil, nil
	}
	return instance, nil
}

// release deletes the session executables of instances activation never started.
func (preparation *devProcessPreparation) release(s *devSupervisor) {
	if preparation == nil {
		return
	}
	<-preparation.done
	for _, instance := range preparation.instances {
		if instance.app == nil && instance.request != nil {
			s.releaseUnusedAppBinary(&appStartPlan{request: *instance.request})
		}
	}
}

func devProcessInstanceEnvironment(base []string, instance *devProcessInstance, link *devProcessLink) []string {
	return append(envWithoutKeys(base, "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR"),
		"SCENERY_LISTEN_NETWORK=unix", "SCENERY_LISTEN_ADDR="+instance.socket, "SCENERY_PROCESS_LINK="+link.path)
}

// devProcessState is the model state a complete replacement restores when the
// new host incarnation cannot serve.
type devProcessState struct {
	link       *devProcessLink
	generation uint64
	contract   string
	bindings   map[string]string
	host       *devProcessInstance
	services   map[string]*devProcessInstance
	retained   map[uint64]map[string]*devProcessInstance
}

// publishDevProcessGeneration publishes the model's services as the next
// generation of the current host incarnation; the caller holds model.mu.
func (s *devSupervisor) publishDevProcessGeneration(ctx context.Context, model *devProcessModel) error {
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
	}{Generation: model.generation + 1, ContractRevision: model.contract, Processes: map[string]instance{}, Bindings: model.bindings}
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
	_, err = model.link.request(ctx, http.MethodPut, "/__scenery/process/v1/generations", body, http.StatusNoContent)
	if err != nil && model.link.current(ctx) == manifest.Generation {
		// The host applied the manifest although its answer was lost.
		err = nil
	}
	build.RecordStep(ctx, build.Step{Name: "process.publish", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "host_generation_manifest", OK: err == nil, Actions: len(manifest.Processes)})
	if err != nil {
		return fmt.Errorf("publish process generation %d: %w", manifest.Generation, err)
	}
	model.generation = manifest.Generation
	model.retained[manifest.Generation] = maps.Clone(model.services)
	return nil
}

// serviceProcessStatuses reports the service processes of a process-model
// session for the dashboard, including services whose process is gone.
func (s *devSupervisor) serviceProcessStatuses() []devdash.ServiceProcess {
	s.mu.RLock()
	model := s.processes
	s.mu.RUnlock()
	if model == nil {
		return nil
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	statuses := make([]devdash.ServiceProcess, 0, len(model.services))
	for name, instance := range model.services {
		status := devdash.ServiceProcess{
			Name: name, Generation: model.generation, State: "running",
			ImplementationRevision: instance.process.Identity.ImplementationRevision,
		}
		if instance.app != nil {
			status.PID = instance.app.pid
		}
		if reason := model.degraded[name]; reason != "" {
			status.State, status.Reason = "degraded", reason
		} else if instance.stopped || instance.app == nil {
			status.State = "degraded"
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

// sessionServiceProcesses names each running service process of the session so
// cleanup and inspection see them beside the host and helper processes.
func (s *devSupervisor) sessionServiceProcesses() map[string]localagent.Process {
	processes := map[string]localagent.Process{}
	for _, status := range s.serviceProcessStatuses() {
		if pid := atoiPID(status.PID); pid > 0 && status.State == "running" {
			processes["service:"+status.Name] = localagent.Process{PID: pid}
		}
	}
	return processes
}

// clearDevProcessRecovery forgets the crash history and degraded state of the
// services a new build replaced; the caller holds model.mu.
func clearDevProcessRecovery(model *devProcessModel, instances []*devProcessInstance) {
	for _, instance := range instances {
		delete(model.restarts, instance.process.Name)
		delete(model.degraded, instance.process.Name)
	}
}

// drainDevProcessInstances revokes the background work of instances whose
// generation was replaced. An instance that does not confirm the revocation is
// stopped, so it cannot acquire work beside its activated replacement.
func (s *devSupervisor) drainDevProcessInstances(ctx context.Context, model *devProcessModel, instances []*devProcessInstance) {
	failed := s.controlDevProcessInstances(ctx, model.token, instances, runtimeProcessDrainPath, "process.drain_failed")
	if len(failed) == 0 {
		return
	}
	markDevProcessesStopped(failed)
	_ = s.stopInstances(failed, model.runningCommands())
}

// activateDevProcessInstances grants background work to instances of the
// published generation. A failure is reported; the generation keeps serving.
func (s *devSupervisor) activateDevProcessInstances(ctx context.Context, model *devProcessModel, instances []*devProcessInstance) {
	s.controlDevProcessInstances(ctx, model.token, instances, runtimeProcessActivatePath, "process.activate_failed")
}

const (
	runtimeProcessActivatePath = "/__scenery/process/v1/activate"
	runtimeProcessDrainPath    = "/__scenery/process/v1/drain"
)

func (s *devSupervisor) controlDevProcessInstances(ctx context.Context, token string, instances []*devProcessInstance, path, failureEvent string) []*devProcessInstance {
	started := time.Now()
	failures := make([]error, len(instances))
	var wg sync.WaitGroup
	for index, instance := range instances {
		if instance == nil || instance.app == nil || instance.stopped {
			continue
		}
		wg.Go(func() {
			failures[index] = instance.control(ctx, path, token)
		})
	}
	wg.Wait()
	var failed []*devProcessInstance
	for index, err := range failures {
		if err == nil {
			continue
		}
		failed = append(failed, instances[index])
		if s.console != nil {
			s.console.Event(failureEvent, map[string]any{"service_process": instances[index].process.Name, "pid": instances[index].app.pid, "error": err.Error()})
		}
	}
	build.RecordStep(ctx, build.Step{Name: "process.background", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: strings.TrimPrefix(path, "/__scenery/process/v1/"), OK: len(failed) == 0, Actions: len(instances)})
	return failed
}

// control sends one background control request to the instance's private
// socket; drain answers 202 when work was revoked but is still stopping.
func (instance *devProcessInstance) control(ctx context.Context, path, token string) error {
	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", instance.socket)
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, devProcessBackgroundTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scenery-process"+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusAccepted {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s answered HTTP %d: %s", path, response.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

// retireDevProcessGeneration waits until the host reports no work pinned to a
// replaced generation, or forces its retirement after devProcessRetireTimeout,
// and then stops the instances no retained generation names any longer.
func (s *devSupervisor) retireDevProcessGeneration(model *devProcessModel, link *devProcessLink, generation uint64) {
	ctx := context.Background()
	deadline := time.Now().Add(devProcessRetireTimeout)
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	forced := false
	for {
		status, err := link.request(ctx, http.MethodDelete, path, nil, 0)
		if err == nil && (status == http.StatusNoContent || status == http.StatusNotFound) {
			break
		}
		if !model.hostIncarnation(link) {
			return
		}
		if time.Now().After(deadline) {
			// Forced retirement ends dispatch within the generation; an
			// unreachable host dispatches nothing to its instances either.
			forced = true
			_, _ = link.request(ctx, http.MethodDelete, path+"?force=true", nil, 0)
			break
		}
		time.Sleep(devProcessRetireInterval)
	}
	model.mu.Lock()
	if model.link != link {
		// A complete replacement ended this host incarnation and its instances.
		model.mu.Unlock()
		return
	}
	named := model.retained[generation]
	delete(model.retained, generation)
	stale := model.unreferenced(mapValues(named))
	markDevProcessesStopped(stale)
	inUse := model.runningCommands()
	model.mu.Unlock()
	if forced && s.console != nil {
		names := make([]string, 0, len(stale))
		for _, instance := range stale {
			names = append(names, instance.process.Name)
		}
		s.console.Event("process.retire_forced", map[string]any{"generation": generation, "stopped_service_processes": names})
	}
	_ = s.stopInstances(stale, inUse)
}

func (model *devProcessModel) hostIncarnation(link *devProcessLink) bool {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.link == link
}

// unreferenced returns the instances neither the current services nor any
// retained generation name; the caller holds model.mu.
func (model *devProcessModel) unreferenced(instances []*devProcessInstance) []*devProcessInstance {
	var stale []*devProcessInstance
	for _, instance := range instances {
		if instance == nil || model.services[instance.process.Name] == instance {
			continue
		}
		referenced := false
		for _, named := range model.retained {
			if named[instance.process.Name] == instance {
				referenced = true
				break
			}
		}
		if !referenced {
			stale = append(stale, instance)
		}
	}
	return stale
}

func (link *devProcessLink) request(ctx context.Context, method, path string, body []byte, want int) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://scenery-host"+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+link.token)
	response, err := link.control.Do(request)
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

// current reads the host's current generation, or zero when it cannot.
func (link *devProcessLink) current(ctx context.Context) uint64 {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://scenery-host/__scenery/process/v1/generations", nil)
	if err != nil {
		return 0
	}
	request.Header.Set("Authorization", "Bearer "+link.token)
	response, err := link.control.Do(request)
	if err != nil {
		return 0
	}
	defer func() { _ = response.Body.Close() }()
	var status struct {
		Current uint64 `json:"current"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&status) != nil {
		return 0
	}
	return status.Current
}
