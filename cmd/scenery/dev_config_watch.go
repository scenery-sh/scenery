package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/appconfig"
)

// devConfigPollInterval is how often a local supervisor checks its
// environment document. It stats one small file; values are read only when
// that file changed.
const devConfigPollInterval = 500 * time.Millisecond

// watchConfiguration applies new desired revisions of the environment to the
// running generation.
func (s *devSupervisor) watchConfiguration(ctx context.Context) {
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
// generation and restarts the host when the application's configuration
// changed. It never compiles. An invalid revision leaves the running
// generation serving and is reported as rejected.
func (s *devSupervisor) applyConfigurationChange(ctx context.Context) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	active := s.active
	if active == nil {
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
	resolution, err := resolveDevConfig(ctx, active.manifest, s.cfg, s.env, store, document, []string{applicationConsumer}, func() (appconfig.SecretBackend, error) {
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
	if len(changed) == 0 {
		// Values no consumer reads changed; the running generation applies
		// the new revision as is.
		s.recordAppliedConfig(resolution, active)
		s.announceConfig("config.applied", map[string]any{"revision": resolution.revision, "restarted_services": changed})
		return
	}
	s.announceConfig("config.applied", map[string]any{"revision": resolution.revision, "restarted_services": changed})
	s.requestHostRestart()
}

func (s *devSupervisor) announceConfig(event string, payload map[string]any) {
	if s.console != nil {
		s.console.Event(event, payload)
	}
}
