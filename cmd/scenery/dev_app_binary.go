package main

import (
	"os"
	"path/filepath"
	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"strings"
)

// Retain the executable bytes, not a symlink into the disposable build cache.
// A content-addressed name also separates different framework generations that
// happen to have the same app-source build filename.
func prepareSessionAppBinary(session *localagent.Session, binary string) (string, error) {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" || strings.TrimSpace(binary) == "" {
		return "", nil
	}
	dir := filepath.Join(session.StateRoot, "run", "app")
	return build.RetainBinary(dir, binary)
}

func (s *devSupervisor) releaseUnusedAppBinary(plan *appStartPlan) {
	if plan == nil {
		return
	}
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	if current != nil && current.launch != nil && current.launch.request.Command == plan.request.Command {
		return
	}
	session := s.currentAgentSession()
	if session == nil || filepath.Dir(plan.request.Command) != filepath.Join(session.StateRoot, "run", "app") {
		return
	}
	_ = os.Remove(plan.request.Command)
}
