package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/build"
)

// devConfigPollInterval is how often a local supervisor checks its
// environment document. It stats one small file; values are read only when
// that file changed.
const devConfigPollInterval = 500 * time.Millisecond

// watchConfiguration applies new desired revisions of a local environment to
// the running generation. Deployable environments never adopt desired
// configuration; only a deployment changes what they run.
func (s *devSupervisor) watchConfiguration(ctx context.Context) {
	if s.env.Deployable() {
		return
	}
	store, err := devConfigStore(s.cfg)
	if err != nil || store == nil {
		return
	}
	path := filepath.Join(store.Dir(), "environments", s.env.Name+".json")
	var last os.FileInfo
	ticker := time.NewTicker(devConfigPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			info = nil
		}
		if sameConfigFile(last, info) {
			continue
		}
		last = info
		s.applyConfigurationChange(ctx)
	}
}

func sameConfigFile(previous, current os.FileInfo) bool {
	if previous == nil || current == nil {
		return previous == nil && current == nil
	}
	return os.SameFile(previous, current) && previous.ModTime().Equal(current.ModTime()) && previous.Size() == current.Size()
}

// applyConfigurationChange resolves the desired revision against the running
// build and restarts only the service processes whose snapshot changed. It
// never compiles, links or generates. An invalid revision leaves the running
// generation serving and is reported as rejected.
func (s *devSupervisor) applyConfigurationChange(ctx context.Context) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	active := s.active
	if active == nil || s.processes == nil {
		return
	}
	store, err := devConfigStore(s.cfg)
	if err != nil || store == nil {
		return
	}
	document, err := devConfigDocument(store, s.cfg, s.env)
	if err != nil {
		s.recordRejectedConfig("", err)
		return
	}
	desired, outcome, _, applied := s.config.snapshot()
	if applied != nil && applied.revision == document.Revision {
		return
	}
	if desired == document.Revision && outcome == "rejected" {
		return
	}
	started := time.Now()
	resolution, err := resolveDevConfig(ctx, active.result.Contract.Manifest, s.cfg, s.env, store, document, generationConsumers(active.set), func() (appconfig.SecretBackend, error) {
		if configSecretBackendOverride != nil {
			return configSecretBackendOverride(store)
		}
		return appconfig.DefaultSecretBackend(store)
	})
	if err != nil {
		s.recordRejectedConfig(document.Revision, err)
		s.announceConfig("config.rejected", map[string]any{"revision": document.Revision, "error": humanCLIErrorMessage(err)})
		return
	}
	changed := changedConfigServices(applied, resolution)
	if slices.Contains(changed, hostConsumer) {
		// The host serves framework routes such as standard authentication, so
		// a change it consumes starts a new generation; services whose own
		// configuration is unchanged keep running.
		_, _, err := s.activateDevProcesses(ctx, &devRuntimePlan{Result: active.result, Processes: active.set}, nil)
		build.RecordStep(ctx, build.Step{Name: "supervisor.configuration_apply", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "environment_revision", OK: err == nil, Actions: len(changed)})
		if err != nil {
			s.recordRejectedConfig(document.Revision, err)
			s.announceConfig("config.rejected", map[string]any{"revision": document.Revision, "error": humanCLIErrorMessage(err)})
			return
		}
		s.announceConfig("config.applied", map[string]any{"revision": resolution.revision, "restarted_services": changed})
		_ = s.persistStatus(ctx)
		return
	}
	model := s.processes
	model.mu.Lock()
	previous := model.config
	model.config = resolution
	err = s.replaceDevServiceProcesses(ctx, model, active.set, active.base, nil)
	if err != nil {
		model.config = previous
	}
	model.unlock()
	build.RecordStep(ctx, build.Step{Name: "supervisor.configuration_apply", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "environment_revision", OK: err == nil, Actions: len(changed)})
	if err != nil {
		s.recordRejectedConfig(document.Revision, err)
		s.announceConfig("config.rejected", map[string]any{"revision": document.Revision, "error": humanCLIErrorMessage(err)})
		return
	}
	s.recordAppliedConfig(resolution, active)
	s.announceConfig("config.applied", map[string]any{"revision": resolution.revision, "restarted_services": changed})
	_ = s.persistStatus(ctx)
}

func (s *devSupervisor) announceConfig(event string, payload map[string]any) {
	if s.console != nil {
		s.console.Event(event, payload)
	}
}
