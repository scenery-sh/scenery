package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/graph"
	"scenery.sh/runtime"
)

// devConfigSnapshot is one service process's configuration: exactly the
// values and secrets its consumers need. identity covers keys, non-secret
// values and opaque secret versions, never secret material, so an unrelated
// change leaves it, and the running process, untouched.
type devConfigSnapshot struct {
	identity string
	revision string
	data     []byte
}

func (snapshot *devConfigSnapshot) privateInput() *devprocess.PrivateInput {
	if snapshot == nil {
		return nil
	}
	return &devprocess.PrivateInput{Env: runtime.ConfigSnapshotFDEnv, Data: snapshot.data}
}

func (snapshot *devConfigSnapshot) identityOrEmpty() string {
	if snapshot == nil {
		return ""
	}
	return snapshot.identity
}

// devConfigResolution is a validated environment revision resolved against
// one build's catalog.
type devConfigResolution struct {
	appID, environment string
	revision           string
	catalogRevision    string
	snapshots          map[string]*devConfigSnapshot
}

func (resolution *devConfigResolution) snapshotFor(service string) *devConfigSnapshot {
	if resolution == nil || service == "" {
		return nil
	}
	return resolution.snapshots[service]
}

func (resolution *devConfigResolution) identityFor(service string) string {
	return resolution.snapshotFor(service).identityOrEmpty()
}

// devConfigState is a supervisor's environment configuration: where it reads
// the environment and which revision the running generation applied.
type devConfigState struct {
	mu sync.Mutex
	// applied is the resolution the published generation runs.
	applied *devConfigResolution
	// observed is the latest desired revision the supervisor considered and its
	// outcome.
	observed string
	state    string
	problem  string
}

func (state *devConfigState) record(desired, outcome, problem string, applied *devConfigResolution) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.observed, state.state, state.problem = desired, outcome, problem
	if applied != nil {
		state.applied = applied
	}
}

func (state *devConfigState) snapshot() (desired, outcome, problem string, applied *devConfigResolution) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.observed, state.state, state.problem, state.applied
}

// devConfigStore opens this supervisor's configuration store. An application
// without an explicit id has no store: only declared defaults apply.
func devConfigStore(cfg app.Config) (*appconfig.Store, error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, nil
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	return appconfig.OpenStore(paths.Home, cfg.ID)
}

// devConfigDocument reads the configuration revision a runtime of env must
// run: the desired revision of a local environment, or the revision the
// active deployment pinned for a deployable one.
func devConfigDocument(store *appconfig.Store, cfg app.Config, env app.ResolvedEnv) (appconfig.Document, error) {
	if store == nil {
		return appconfig.NewDocument(cfg.AppID(), env.Name), nil
	}
	if env.Deployable() {
		paths, err := commandAgentPaths()
		if err != nil {
			return appconfig.Document{}, err
		}
		active, err := readDeploymentActive(paths.Home, cfg.ID, env.Name)
		if err != nil {
			return appconfig.Document{}, err
		}
		if active != nil && active.ConfigRevision != "" {
			return store.ReadRevision(env.Name, active.ConfigRevision)
		}
	}
	document, _, err := store.Read(env.Name)
	return document, err
}

// resolveDevConfig validates document against the build's catalog and builds
// each service process's snapshot. An invalid candidate is an error naming
// every problem; it never starts.
func resolveDevConfig(ctx context.Context, manifest *graph.Manifest, cfg app.Config, env app.ResolvedEnv, store *appconfig.Store, document appconfig.Document, consumers []string, backend func() (appconfig.SecretBackend, error)) (*devConfigResolution, error) {
	catalog, err := appconfig.BuildCatalog(manifest, configFrameworkOptions(cfg))
	if err != nil {
		return nil, err
	}
	resolved := appconfig.Resolve(catalog, document, env.Deployable())
	if !resolved.Valid() {
		var problems []string
		for _, entry := range resolved.Entries {
			if entry.State == appconfig.StateMissing || entry.State == appconfig.StateInvalid {
				problems = append(problems, entry.Key+": "+entry.Problem)
			}
		}
		return nil, preconditionErrorf("environment %s configuration revision %s is not valid for this build: %s; fix it with scenery config set --env %s", env.Name, document.Revision, strings.Join(problems, "; "), env.Name)
	}
	resolution := &devConfigResolution{appID: cfg.AppID(), environment: env.Name, revision: document.Revision, catalogRevision: catalog.Revision, snapshots: map[string]*devConfigSnapshot{}}
	secrets := map[string][]byte{}
	var secretBackend appconfig.SecretBackend
	// The host and the first service process may serve framework routes,
	// including the public configuration, so they carry public values.
	publicConsumers := map[string]bool{hostConsumer: true}
	for _, service := range consumers {
		if service != "" && service != hostConsumer {
			publicConsumers[service] = true
			break
		}
	}
	for _, service := range consumers {
		if service == "" {
			continue
		}
		snapshot := runtime.ConfigSnapshot{Kind: runtime.ConfigSnapshotKind, AppID: resolution.appID, Environment: env.Name, Revision: document.Revision, CatalogRevision: catalog.Revision, Consumer: service, Values: map[string]json.RawMessage{}, Secrets: map[string][]byte{}, Public: []string{}}
		identity := sha256.New()
		_, _ = fmt.Fprintf(identity, "%s\x00%s\x00", resolution.appID, env.Name)
		for _, entry := range resolved.Entries {
			input, _ := catalog.Lookup(entry.Key)
			public := input.Public && publicConsumers[service]
			if !consumes(input, service) && !public {
				continue
			}
			if public && entry.Value != nil {
				snapshot.Public = append(snapshot.Public, entry.Key)
				_, _ = fmt.Fprintf(identity, "public\x00%s\x00", entry.Key)
			}
			switch {
			case entry.Sensitive && entry.Secret != nil:
				value, cached := secrets[entry.Key]
				if !cached {
					if secretBackend == nil {
						if secretBackend, err = backend(); err != nil {
							return nil, unavailableErrorf("%v", err)
						}
					}
					value, err = secretBackend.Resolve(ctx, env.Name, entry.Key, *entry.Secret)
					if err != nil {
						return nil, unavailableErrorf("resolve configured secret %s: %v", entry.Key, err)
					}
					secrets[entry.Key] = value
				}
				snapshot.Secrets[entry.Key] = value
				_, _ = fmt.Fprintf(identity, "secret\x00%s\x00%s\x00%s\x00", entry.Key, entry.Secret.Backend, entry.Secret.Version)
			case !entry.Sensitive && entry.Value != nil:
				snapshot.Values[entry.Key] = entry.Value
				_, _ = fmt.Fprintf(identity, "value\x00%s\x00%s\x00", entry.Key, entry.Value)
			}
		}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		resolution.snapshots[service] = &devConfigSnapshot{identity: hex.EncodeToString(identity.Sum(nil)), revision: document.Revision, data: data}
	}
	return resolution, nil
}

// applicationConsumer is the consumer of a single-process application
// executable, which hosts every service.
const applicationConsumer = "application"

// hostConsumer is the consumer of a process-model generation's host, which
// serves framework routes such as standard authentication.
const hostConsumer = "host"

func consumes(input appconfig.Input, service string) bool {
	for _, consumer := range input.Consumers {
		if service == applicationConsumer && consumer.Service != appconfig.FrameworkConsumer+"/assistants" {
			return true
		}
		if consumer.Service == service || consumer.Service == appconfig.FrameworkConsumer+"/auth" {
			return true
		}
	}
	return false
}

// devConfigHolder names this worktree's pin on the environment's history.
func devConfigHolder(root string) string {
	root = filepath.Clean(root)
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	sum := sha256.Sum256([]byte(root))
	return "worktree-" + hex.EncodeToString(sum[:8])
}

// sortedConfigServices lists the services whose snapshot identity differs
// between two resolutions.
func changedConfigServices(previous, next *devConfigResolution) []string {
	var changed []string
	for service := range next.snapshots {
		if previous.identityFor(service) != next.identityFor(service) {
			changed = append(changed, service)
		}
	}
	sort.Strings(changed)
	return changed
}

// resolveGenerationConfig resolves the configuration a new generation of this
// build starts with.
func (s *devSupervisor) resolveGenerationConfig(ctx context.Context, result *build.Result, set *build.DevelopmentProcessSet) (*devConfigResolution, error) {
	store, err := devConfigStore(s.cfg)
	if err != nil {
		return nil, err
	}
	document, err := devConfigDocument(store, s.cfg, s.env)
	if err != nil {
		return nil, preconditionErrorf("read %s configuration: %v", s.env.Name, err)
	}
	resolution, err := resolveDevConfig(ctx, result.Contract.Manifest, s.cfg, s.env, store, document, generationConsumers(set), func() (appconfig.SecretBackend, error) {
		if configSecretBackendOverride != nil {
			return configSecretBackendOverride(store)
		}
		return appconfig.DefaultSecretBackend(store)
	})
	if err != nil {
		s.config.record(document.Revision, "rejected", err.Error(), nil)
		return nil, err
	}
	return resolution, nil
}

// recordAppliedConfig publishes that the running generation applied a
// revision and pins it, so pruning keeps it while this worktree runs it.
func (s *devSupervisor) recordAppliedConfig(resolution *devConfigResolution, generation *devActiveGeneration) {
	s.active = generation
	s.config.record(resolution.revision, "applied", "", resolution)
	s.pinConfigObservation(appconfig.Pin{Revision: resolution.revision, Desired: resolution.revision, State: "applied"})
}

// recordRejectedConfig reports a desired revision this worktree could not
// apply; the running generation keeps its revision.
func (s *devSupervisor) recordRejectedConfig(desired string, err error) {
	s.config.record(desired, "rejected", err.Error(), nil)
	_, _, _, applied := s.config.snapshot()
	if applied != nil {
		s.pinConfigObservation(appconfig.Pin{Revision: applied.revision, Desired: desired, State: "rejected", Problem: humanCLIErrorMessage(err)})
	}
}

func (s *devSupervisor) pinConfigObservation(pin appconfig.Pin) {
	if store, err := devConfigStore(s.cfg); err == nil && store != nil {
		if len(pin.Problem) > 2048 {
			pin.Problem = pin.Problem[:2048]
		}
		_ = store.PinRecord(s.env.Name, devConfigHolder(s.root), pin)
	}
}

// releaseConfigPin removes this worktree's pin when its runtime stops.
func (s *devSupervisor) releaseConfigPin() {
	if store, err := devConfigStore(s.cfg); err == nil && store != nil {
		_ = store.Unpin(s.env.Name, devConfigHolder(s.root))
	}
}

// generationConsumers are the configuration consumers of a process set: its
// host, then each service process.
func generationConsumers(set *build.DevelopmentProcessSet) []string {
	return append([]string{hostConsumer}, processServices(set.Services)...)
}

func processServices(processes []build.DevelopmentProcess) []string {
	services := make([]string, 0, len(processes))
	for _, process := range processes {
		services = append(services, process.Service)
	}
	return services
}

// applicationConfigInput resolves the snapshot of a single-process
// application executable, such as a worker, for an environment.
func applicationConfigInput(ctx context.Context, root string, cfg app.Config, envName string, manifest *graph.Manifest) (*devprocess.PrivateInput, error) {
	env, err := cfg.ResolveEnv(envName)
	if err != nil {
		return nil, &codedCLIError{err: err, code: 3}
	}
	store, err := devConfigStore(cfg)
	if err != nil {
		return nil, err
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return nil, preconditionErrorf("read %s configuration: %v", env.Name, err)
	}
	resolution, err := resolveDevConfig(ctx, manifest, cfg, env, store, document, []string{applicationConsumer}, func() (appconfig.SecretBackend, error) {
		if configSecretBackendOverride != nil {
			return configSecretBackendOverride(store)
		}
		return appconfig.DefaultSecretBackend(store)
	})
	if err != nil {
		return nil, err
	}
	return resolution.snapshotFor(applicationConsumer).privateInput(), nil
}
