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
	if preparation == nil || preparation.link == nil || link == nil || preparation.link.path != link.path {
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
	link        *devProcessLink
	generation  uint64
	contract    string
	identity    build.DevelopmentProcessIdentity
	environment string
	bindings    map[string]string
	host        *devProcessInstance
	services    map[string]*devProcessInstance
	retained    map[uint64]map[string]*devProcessInstance
	unconfirmed []uint64
}

// publishDevProcessGeneration publishes the model's services as a new
// generation of the current host incarnation under a number no earlier
// publication of the session proposed; the caller holds model.mu.
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
	attested := model.identity
	model.published = max(model.published, model.generation) + 1
	manifest := struct {
		Generation       uint64              `json:"generation"`
		ContractRevision string              `json:"contract_revision"`
		Identity         identity            `json:"identity"`
		Processes        map[string]instance `json:"processes"`
		Bindings         map[string]string   `json:"bindings"`
	}{Generation: model.published, ContractRevision: model.contract, Identity: identity{attested.ContractRevision, attested.ImplementationRevision, attested.BuildInputDigest, attested.GoTarget}, Processes: map[string]instance{}, Bindings: model.bindings}
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
	status, err := model.link.request(ctx, http.MethodPut, "/__scenery/process/v1/generations", body, http.StatusNoContent)
	unknown := false
	if err != nil && status == 0 {
		// The host may have applied the manifest and lost its answer; only its
		// current generation decides.
		err, unknown = model.link.confirm(ctx, manifest.Generation, err, model.confirmInterval())
	}
	build.RecordStep(ctx, build.Step{Name: "process.publish", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "host_generation_manifest", OK: err == nil, Actions: len(manifest.Processes)})
	if unknown {
		return &devProcessPublicationUnknownError{generation: manifest.Generation, err: err}
	}
	if err != nil {
		return fmt.Errorf("publish process generation %d: %w", manifest.Generation, err)
	}
	model.generation = manifest.Generation
	model.retained[manifest.Generation] = maps.Clone(model.services)
	// Generations whose publication outcome was unknown are no longer current
	// once a later publication is; they retire, and so do their instances.
	for _, number := range model.unconfirmed {
		go s.retireDevProcessGeneration(model, model.link, number)
	}
	model.unconfirmed, model.publication = nil, ""
	return nil
}

// devProcessPublicationUnknownError is the failure of a publication the host
// may have applied: neither its answer nor a confirmation of the host's
// current generation arrived.
type devProcessPublicationUnknownError struct {
	generation uint64
	err        error
}

func (err *devProcessPublicationUnknownError) Error() string {
	return fmt.Sprintf("the outcome of publishing process generation %d is unknown: %v", err.generation, err.err)
}

func (err *devProcessPublicationUnknownError) Unwrap() error { return err.err }

const (
	// devProcessConfirmAttempts bounds the status reads that confirm a
	// publication whose answer was lost.
	devProcessConfirmAttempts = 3
	devProcessConfirmInterval = 100 * time.Millisecond
)

// confirm reads the host's current generation after a publication whose answer
// was lost. It reports no failure when the host applied the publication, the
// host's refusal when it serves another generation, and unknown when the host
// does not answer.
func (link *devProcessLink) confirm(ctx context.Context, generation uint64, cause error, interval time.Duration) (error, bool) {
	for attempt := range devProcessConfirmAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return cause, true
			case <-time.After(interval):
			}
		}
		if current, ok := link.current(ctx); ok {
			if current == generation {
				return nil, false
			}
			return fmt.Errorf("host serves generation %d instead: %w", current, cause), false
		}
	}
	return cause, true
}

// settleFailedDevProcessPublication disposes of the candidate instances of a
// failed publication on the current host; the caller holds model.mu and has
// restored the model's services. Candidates of a refused publication stop. A
// publication whose outcome is unknown may have made the host route to its
// candidates, so they stay owned and running, retained under the unconfirmed
// generation, and a reconciler republishes the known services under a new
// number; only then does the unconfirmed generation retire and its instances
// stop.
func (s *devSupervisor) settleFailedDevProcessPublication(model *devProcessModel, candidates []*devProcessInstance, err error) error {
	unknown, ok := errors.AsType[*devProcessPublicationUnknownError](err)
	if !ok {
		markDevProcessesStopped(candidates)
		_ = s.stopInstances(candidates, model.runningCommands())
		return err
	}
	named := map[string]*devProcessInstance{}
	for _, instance := range candidates {
		named[instance.process.Name] = instance
	}
	model.retained[unknown.generation] = named
	model.unconfirmed = append(model.unconfirmed, unknown.generation)
	if s.console != nil {
		s.console.Event("process.publication_unknown", map[string]any{"generation": unknown.generation, "error": unknown.err.Error()})
	}
	go s.reconcileDevProcessPublication(model, model.link)
	return err
}

// reconcileDevProcessPublication republishes the model's services until a
// publication succeeds, which retires every unconfirmed generation, or until
// devProcessRetireTimeout passes, after which the services report the
// unconfirmed publication as degraded until the next successful publication.
func (s *devSupervisor) reconcileDevProcessPublication(model *devProcessModel, link *devProcessLink) {
	deadline := time.Now().Add(devProcessRetireTimeout)
	model.mu.Lock()
	delay := model.confirmInterval()
	model.mu.Unlock()
	for {
		model.mu.Lock()
		if model.link != link || len(model.unconfirmed) == 0 {
			model.unlock()
			return
		}
		err := s.publishDevProcessGeneration(s.ctx, model)
		if unknown, ok := errors.AsType[*devProcessPublicationUnknownError](err); ok {
			model.unconfirmed = append(model.unconfirmed, unknown.generation)
		}
		if err == nil {
			if s.console != nil {
				s.console.Event("process.publication_reconciled", map[string]any{"generation": model.generation})
			}
			model.unlock()
			return
		}
		expired := time.Now().After(deadline)
		if expired {
			model.publication = err.Error()
		}
		model.unlock()
		if expired {
			if s.console != nil {
				s.console.Event("process.publication_degraded", map[string]any{"error": err.Error()})
			}
			return
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, devProcessBackgroundTimeout)
	}
}

// devProcessStatus is the service-process view of the model's last committed
// state. It is published while mu is held and read without it, because status
// readers include callbacks that run inside an activation: an assistant helper
// that reports its process during activation registers the session, which
// names the service processes.
type devProcessStatus struct {
	services []devdash.ServiceProcess
	// running names the live process of each service, including a service
	// whose background work is not yet confirmed, so session cleanup sees it.
	running map[string]int
}

// publishStatus publishes the current service-process view; the caller holds
// model.mu.
func (model *devProcessModel) publishStatus() {
	status := devProcessStatus{services: make([]devdash.ServiceProcess, 0, len(model.services)), running: map[string]int{}}
	for name, instance := range model.services {
		service := devdash.ServiceProcess{
			Name: name, Generation: model.generation, State: "running",
			ImplementationRevision: instance.process.Identity.ImplementationRevision,
		}
		if instance.app != nil {
			service.PID = instance.app.pid
		}
		switch {
		case model.degraded[name] != "":
			service.State, service.Reason = "degraded", model.degraded[name]
		case model.unconfirmedNames(name):
			service.State, service.Reason = "degraded", "publication outcome unknown: "+model.publication
		case instance.stopped || instance.app == nil:
			service.State = "degraded"
		case instance.unavailable != "":
			service.State, service.Reason = "degraded", "background work unavailable: "+instance.unavailable
		case instance.activation != "":
			service.State, service.Reason = "degraded", "background work activation unconfirmed: "+instance.activation
		}
		if pid := atoiPID(service.PID); pid > 0 && !instance.stopped && instance.app != nil {
			status.running[name] = pid
		}
		status.services = append(status.services, service)
	}
	sort.Slice(status.services, func(i, j int) bool { return status.services[i].Name < status.services[j].Name })
	model.statusMu.Lock()
	model.status = status
	model.statusMu.Unlock()
}

// confirmInterval is the delay between confirmations of a publication whose
// outcome is unknown; the caller holds model.mu.
func (model *devProcessModel) confirmInterval() time.Duration {
	if model.activationBackoff > 0 {
		return model.activationBackoff
	}
	return devProcessConfirmInterval
}

// unconfirmedNames reports whether an unconfirmed generation names a candidate
// of service name whose reconciliation expired; the caller holds model.mu.
func (model *devProcessModel) unconfirmedNames(name string) bool {
	if model.publication == "" {
		return false
	}
	for _, number := range model.unconfirmed {
		if model.retained[number][name] != nil {
			return true
		}
	}
	return false
}

// unlock publishes the state a critical section committed and releases mu.
func (model *devProcessModel) unlock() {
	model.publishStatus()
	model.mu.Unlock()
}

func (s *devSupervisor) processStatus() devProcessStatus {
	s.mu.RLock()
	model := s.processes
	s.mu.RUnlock()
	if model == nil {
		return devProcessStatus{}
	}
	model.statusMu.Lock()
	defer model.statusMu.Unlock()
	return model.status
}

// serviceProcessStatuses reports the service processes of a process-model
// session for the dashboard, including services whose process is gone.
func (s *devSupervisor) serviceProcessStatuses() []devdash.ServiceProcess {
	s.mu.RLock()
	selected := s.processes != nil
	s.mu.RUnlock()
	if !selected {
		return nil
	}
	return append([]devdash.ServiceProcess{}, s.processStatus().services...)
}

// devProcessSessionPrefix begins the session record key of every service
// process. The agent stores record keys as labels, so the prefix is already a
// label: a key the supervisor reads back from a stored record still carries it
// and is replaced rather than kept beside a newer instance of the service.
const devProcessSessionPrefix = "service-"

// sessionServiceProcesses names each live service process of the session so
// cleanup and inspection see them beside the host and helper processes.
func (s *devSupervisor) sessionServiceProcesses() map[string]localagent.Process {
	processes := map[string]localagent.Process{}
	for name, pid := range s.processStatus().running {
		processes[devProcessSessionPrefix+localagentLabel(name)] = localagent.Process{PID: pid}
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
	failures := s.controlDevProcessInstances(ctx, model.token, instances, runtimeProcessDrainPath, "process.drain_failed")
	if len(failures) == 0 {
		return
	}
	failed := slices.Collect(maps.Keys(failures))
	markDevProcessesStopped(failed)
	_ = s.stopInstances(failed, model.runningCommands())
}

// activateDevProcessInstances grants background work to instances of the
// published generation; the caller holds model.mu. An instance whose
// activation is unconfirmed keeps serving, reports degraded background work,
// and is reconciled until it confirms or stops being current: activation is
// idempotent, so a request whose answer was lost is simply repeated.
func (s *devSupervisor) activateDevProcessInstances(ctx context.Context, model *devProcessModel, instances []*devProcessInstance) {
	failures := s.controlDevProcessInstances(ctx, model.token, instances, runtimeProcessActivatePath, "process.activate_failed")
	for _, instance := range instances {
		if instance != nil && failures[instance] == nil {
			instance.activation, instance.unavailable = "", ""
		}
	}
	for instance, err := range failures {
		if refused, ok := errors.AsType[*devProcessActivationRefusedError](err); ok {
			instance.activation, instance.unavailable = "", refused.reason
			continue
		}
		instance.activation = err.Error()
		if !instance.reconciling {
			instance.reconciling = true
			go s.reconcileDevProcessActivation(model, instance)
		}
	}
}

// reconcileDevProcessActivation repeats the activation of a current instance
// until it confirms. It waits for the instance's answer without holding
// model.mu, so an unresponsive service delays only control of that instance,
// never a replacement of another; the instance's own control serialization
// guarantees a drain is never followed by a late activation.
func (s *devSupervisor) reconcileDevProcessActivation(model *devProcessModel, instance *devProcessInstance) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	backoff := model.activationBackoff
	if backoff <= 0 {
		backoff = devProcessActivationBackoff
	}
	current := func() bool {
		return model.services[instance.process.Name] == instance && !instance.stopped && instance.app != nil
	}
	for {
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, devProcessBackgroundTimeout)
		model.mu.Lock()
		if !current() {
			instance.reconciling = false
			model.unlock()
			return
		}
		model.mu.Unlock()
		err := instance.control(ctx, runtimeProcessActivatePath, model.token)
		model.mu.Lock()
		if !current() || errors.Is(err, errDevProcessDrained) {
			instance.reconciling = false
			model.unlock()
			return
		}
		if refused, ok := errors.AsType[*devProcessActivationRefusedError](err); ok {
			instance.activation, instance.unavailable, instance.reconciling = "", refused.reason, false
			model.unlock()
			return
		}
		if err != nil {
			instance.activation = err.Error()
			model.unlock()
			continue
		}
		instance.activation, instance.reconciling = "", false
		model.unlock()
		if s.console != nil {
			s.console.Event("process.activate_reconciled", map[string]any{"service_process": instance.process.Name, "pid": instance.app.pid})
		}
		return
	}
}

const (
	runtimeProcessActivatePath = "/__scenery/process/v1/activate"
	runtimeProcessDrainPath    = "/__scenery/process/v1/drain"
)

// controlDevProcessInstances sends one control request to each running instance
// and reports the instances that did not confirm it.
func (s *devSupervisor) controlDevProcessInstances(ctx context.Context, token string, instances []*devProcessInstance, path, failureEvent string) map[*devProcessInstance]error {
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
	failed := map[*devProcessInstance]error{}
	for index, err := range failures {
		if err == nil {
			continue
		}
		failed[instances[index]] = err
		if s.console != nil {
			s.console.Event(failureEvent, map[string]any{"service_process": instances[index].process.Name, "pid": instances[index].app.pid, "error": err.Error()})
		}
	}
	build.RecordStep(ctx, build.Step{Name: "process.background", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: strings.TrimPrefix(path, "/__scenery/process/v1/"), OK: len(failed) == 0, Actions: len(instances)})
	return failed
}

// errDevProcessDrained refuses to activate an instance whose background work
// was revoked.
var errDevProcessDrained = errors.New("development process was drained")

// devProcessActivationRefusedError reports an activation the instance refused
// because its build lacks a capability its background work requires.
type devProcessActivationRefusedError struct{ reason string }

func (e *devProcessActivationRefusedError) Error() string { return e.reason }

// control sends one background control request to the instance's private
// socket; drain answers 202 when work was revoked but is still stopping.
// Requests to one instance are serialized, and once a drain was sent the
// instance is never activated again, whatever the drain's answer.
func (instance *devProcessInstance) control(ctx context.Context, path, token string) error {
	instance.controlMu.Lock()
	defer instance.controlMu.Unlock()
	if path == runtimeProcessActivatePath && instance.drained {
		return errDevProcessDrained
	}
	if path == runtimeProcessDrainPath {
		instance.drained = true
	}
	return instance.sendControl(ctx, path, token)
}

func (instance *devProcessInstance) sendControl(ctx context.Context, path, token string) error {
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
		if path == runtimeProcessActivatePath && response.StatusCode == http.StatusServiceUnavailable {
			return &devProcessActivationRefusedError{reason: strings.TrimSpace(string(message))}
		}
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
	model.unlock()
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

// current reads the host's current generation and reports whether the host
// answered.
func (link *devProcessLink) current(ctx context.Context) (uint64, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://scenery-host/__scenery/process/v1/generations", nil)
	if err != nil {
		return 0, false
	}
	request.Header.Set("Authorization", "Bearer "+link.token)
	response, err := link.control.Do(request)
	if err != nil {
		return 0, false
	}
	defer func() { _ = response.Body.Close() }()
	var status struct {
		Current uint64 `json:"current"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&status) != nil {
		return 0, false
	}
	return status.Current, true
}

// devProcessProofs remembers the runtime preflights the session's retained
// executables passed. A preflight is a read-only handshake of an executable's
// linked identity with its start environment, so the same retained bytes with
// the same identity and environment, apart from the instance's own listener
// and link, prove the same handshake: an unchanged executable restarted for a
// new host incarnation is not preflighted again.
type devProcessProofs struct {
	sync.Mutex
	keys map[string]bool
}

// devProcessProofLimit bounds the proofs one session remembers.
const devProcessProofLimit = 1024

// devProcessProofKey identifies a preflight of a retained executable, or is
// empty when the executable has no verified digest.
func devProcessProofKey(process build.DevelopmentProcess, command string, env []string) string {
	if process.ArtifactDigest == "" || command == "" {
		return ""
	}
	identity := process.Identity
	return strings.Join([]string{process.ArtifactDigest, command, identity.ContractRevision, identity.ImplementationRevision, identity.BuildInputDigest, identity.GoTarget,
		devProcessEnvironmentIdentity(envWithoutKeys(env, "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR", "SCENERY_PROCESS_LINK"))}, "\x00")
}

func (proofs *devProcessProofs) proven(key string) bool {
	if key == "" {
		return false
	}
	proofs.Lock()
	defer proofs.Unlock()
	return proofs.keys[key]
}

func (proofs *devProcessProofs) remember(key string) {
	if key == "" {
		return
	}
	proofs.Lock()
	defer proofs.Unlock()
	if proofs.keys == nil || len(proofs.keys) >= devProcessProofLimit {
		proofs.keys = map[string]bool{}
	}
	proofs.keys[key] = true
}
