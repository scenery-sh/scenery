package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/victoria"
)

const (
	victoriaHealthCheckInterval = time.Second
	victoriaRecoveryBackoffMax  = 30 * time.Second
)

var victoriaSubstrateProcessLocks sync.Map

type victoriaRunConsole struct {
	console *runConsole
}

func (c victoriaRunConsole) Verbose() bool { return c.console.verbose }

func (c victoriaRunConsole) JSON() bool { return c.console.json }

func (c victoriaRunConsole) Event(event string, fields map[string]any) {
	c.console.Event(event, fields)
}

func victoriaConsole(console *runConsole) victoria.Console {
	if console == nil {
		return nil
	}
	return victoriaRunConsole{console: console}
}

func (s *devSupervisor) ensureSharedVictoriaStack(ctx context.Context, root string) (*victoria.Stack, bool, error) {
	console := (*runConsole)(nil)
	if s != nil {
		console = s.console
	}
	if s == nil || s.agent == nil {
		return victoria.StartAtRoot(ctx, root, victoriaConsole(console)), false, nil
	}
	processUnlock := lockVictoriaSubstrateProcess(root)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	var existing *localagent.Substrate
	if substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria); err == nil {
		stack, reusable := reusableVictoriaStack(substrate)
		if reusable {
			emitVictoriaSubstrateEvent(s.eventSink(), ctx, "running", "shared Victoria stack reused", map[string]any{
				"owner":     "agent",
				"endpoints": substrate.Endpoints,
			})
			return stack, true, nil
		}
		existing = &substrate
	} else if !localagent.IsNotFound(err) {
		return nil, false, err
	}
	if existing != nil {
		if err := stopVerifiedVictoriaStack(ctx, *existing); err != nil {
			return nil, false, err
		}
		if _, err := s.agent.DeleteSubstrate(ctx, localagent.SubstrateVictoria); err != nil {
			return nil, false, err
		}
	}
	stack := victoria.StartAtRoot(ctx, root, victoriaConsole(console))
	if stack == nil {
		return nil, false, nil
	}
	if !stack.FullyManaged() {
		discardVictoriaStack(stack)
		return nil, false, fmt.Errorf("shared Victoria stack did not start all components")
	}
	req := stack.SubstrateRequest(os.Getpid())
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = localagent.SubstrateVictoria
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = "ready"
	}
	if _, err := s.agent.UpsertSubstrate(ctx, req); err != nil {
		discardVictoriaStack(stack)
		return nil, false, err
	}
	stack.MarkExternal()
	emitVictoriaSubstrateEvent(s.eventSink(), ctx, "running", "shared Victoria stack ready", map[string]any{
		"owner":     "agent",
		"endpoints": req.Endpoints,
	})
	return stack, false, nil
}

func stopVerifiedVictoriaStack(ctx context.Context, substrate localagent.Substrate) error {
	live := make([]int, 0, len(substrate.PIDs))
	seen := map[int]bool{}
	for name, pid := range substrate.PIDs {
		if pid <= 0 || !processAliveForEdge(pid) {
			continue
		}
		owner := substrate.Owners[name]
		if owner.PID != pid {
			return fmt.Errorf("Victoria component %s process %d has no matching owner", name, pid)
		}
		if err := localagent.VerifyOwner(owner); err != nil {
			return fmt.Errorf("Victoria component %s process %d owner cannot be verified: %w", name, pid, err)
		}
		if !seen[pid] {
			live = append(live, pid)
		}
		seen[pid] = true
	}
	ownerPID := firstPositiveInt(substrate.Owner.PID, substrate.OwnerPID)
	if ownerPID > 0 && !seen[ownerPID] && processAliveForEdge(ownerPID) {
		return fmt.Errorf("Victoria stack owner process %d is not a registered component", ownerPID)
	}
	for _, pid := range live {
		if err := signalPID(pid, os.Interrupt); err != nil {
			return err
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		stopped := true
		for _, pid := range live {
			stopped = stopped && !processAliveForEdge(pid)
		}
		stopped = stopped && victoria.ComponentPortsAvailable()
		if stopped {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for the previous Victoria stack to stop")
		case <-ticker.C:
		}
	}
}

func discardVictoriaStack(stack *victoria.Stack) {
	_ = stack.Interrupt()
	_ = stack.WaitOrKill(time.Second)
}

func lockVictoriaSubstrateProcess(root string) func() {
	keyRoot := strings.TrimSpace(root)
	if keyRoot == "" {
		keyRoot = os.TempDir()
	}
	if abs, err := filepath.Abs(keyRoot); err == nil {
		keyRoot = abs
	}
	key := filepath.Clean(keyRoot) + "\x00" + localagent.SubstrateVictoria
	value, _ := victoriaSubstrateProcessLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func reusableVictoriaStack(substrate localagent.Substrate) (*victoria.Stack, bool) {
	if strings.TrimSpace(substrate.Status) != "" && strings.TrimSpace(substrate.Status) != "ready" {
		return nil, false
	}
	if err := verifySubstrateOwner(substrate); err != nil {
		return nil, false
	}
	stack := victoria.FromSubstrate(substrate)
	if stack == nil || !stack.Reachable() {
		return nil, false
	}
	stack.MarkExternal()
	return stack, true
}

func (s *devSupervisor) startVictoriaRecoveryMonitor() {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		s.reportVictoriaRecoveryFailure("", fmt.Errorf("recovery monitor unavailable: %w", err), 0)
		return
	}
	s.monitorVictoriaRecovery(filepath.Join(paths.AgentDir, "victoria"), victoriaHealthCheckInterval, victoriaRecoveryBackoffMax)
}

func (s *devSupervisor) monitorVictoriaRecovery(root string, interval, maxBackoff time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if s == nil || s.agent == nil || s.ctx == nil {
		close(done)
		return done
	}
	if interval <= 0 {
		interval = victoriaHealthCheckInterval
	}
	if maxBackoff < interval {
		maxBackoff = interval
	}
	go func() {
		defer close(done)
		delay := interval
		for {
			timer := time.NewTimer(delay)
			select {
			case <-s.ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}

			s.mu.RLock()
			current := s.victoria
			s.mu.RUnlock()
			if current != nil && current.Reachable() {
				delay = interval
				continue
			}

			stack, reused, err := s.ensureSharedVictoriaStack(s.ctx, root)
			if err != nil || stack == nil || !stack.Reachable() {
				if err == nil {
					err = errors.New("shared Victoria stack remains unavailable")
				}
				delay *= 2
				if delay > maxBackoff {
					delay = maxBackoff
				}
				s.reportVictoriaRecoveryFailure(root, err, delay)
				continue
			}

			s.mu.Lock()
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			s.victoria = stack
			s.mu.Unlock()
			monitorVictoriaSubstrate(root, s.agent, s.eventSink(), stack)
			emitVictoriaSubstrateEvent(s.eventSink(), s.ctx, "running", "shared Victoria stack recovered", map[string]any{
				"reused":    reused,
				"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
			})
			delay = interval
		}
	}()
	return done
}

func (s *devSupervisor) reportVictoriaRecoveryFailure(root string, recoveryErr error, retryAfter time.Duration) {
	if s == nil || recoveryErr == nil {
		return
	}
	message := fmt.Sprintf("Victoria observability recovery failed: %v", recoveryErr)
	if retryAfter > 0 {
		message = fmt.Sprintf("Victoria observability recovery failed; retrying in %s: %v", formatDuration(retryAfter), recoveryErr)
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.eventSink().Output(ctx, devdash.DevSource{
		ID:     "victoria",
		Kind:   "substrate",
		Name:   "Victoria stack",
		Role:   "observability",
		Stream: "stderr",
		Status: "degraded",
		Reason: recoveryErr.Error(),
	}, []byte("ERR "+message+"\n"))
	if s.console != nil && !s.console.json {
		s.console.printf(s.console.err, "%s\n", s.console.palette.Bold(s.console.palette.Red("ERR "+message)))
	}
	if strings.TrimSpace(root) != "" {
		s.markVictoriaSubstrateDegraded(ctx, root)
	}
}

func (s *devSupervisor) markVictoriaSubstrateDegraded(ctx context.Context, root string) {
	if s == nil || s.agent == nil {
		return
	}
	processUnlock := lockVictoriaSubstrateProcess(root)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria)
	if err != nil {
		return
	}
	defer unlock()
	current, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return
	}
	if _, reusable := reusableVictoriaStack(current); reusable {
		return
	}
	_, _ = s.agent.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:           current.Kind,
		Status:         "degraded",
		OwnerPID:       current.OwnerPID,
		Owner:          current.Owner,
		PIDs:           current.PIDs,
		Owners:         current.Owners,
		URLs:           current.URLs,
		Endpoints:      current.Endpoints,
		Leases:         current.Leases,
		LastExit:       current.LastExit,
		ComponentExits: current.ComponentExits,
	})
}

func monitorVictoriaSubstrate(root string, agent *localagent.Client, events *devEventSink, stack *victoria.Stack) <-chan struct{} {
	done := make(chan struct{})
	if agent == nil || stack == nil {
		close(done)
		return done
	}
	components := stack.Components()
	if len(components) == 0 {
		close(done)
		return done
	}
	var wg sync.WaitGroup
	for _, component := range components {
		if component == nil || component.Done() == nil {
			continue
		}
		wg.Add(1)
		go func(component *victoria.Component) {
			defer wg.Done()
			err, ok := <-component.Done()
			if !ok {
				return
			}
			exit := component.ExitRecord(err)
			processUnlock := lockVictoriaSubstrateProcess(root)
			if unlock, lockErr := lockManagedSubstrateRoot(root, localagent.SubstrateVictoria); lockErr == nil {
				if current, getErr := agent.GetSubstrate(context.Background(), localagent.SubstrateVictoria); getErr == nil && current.PIDs[component.Name()] == exit.PID {
					req := localagent.UpsertSubstrateRequest{
						Kind:      current.Kind,
						Status:    "degraded",
						OwnerPID:  current.OwnerPID,
						Owner:     current.Owner,
						PIDs:      current.PIDs,
						Owners:    current.Owners,
						URLs:      current.URLs,
						Endpoints: current.Endpoints,
						Leases:    current.Leases,
						LastExit:  &exit,
						ComponentExits: map[string]localagent.SubstrateExit{
							component.Name(): exit,
						},
					}
					_, _ = agent.UpsertSubstrate(context.Background(), req)
				}
				unlock()
			}
			processUnlock()
			emitVictoriaSubstrateEvent(events, context.Background(), "degraded", component.DisplayName()+" exited", substrateExitEventFields(exit), devdash.DevSource{
				ID:     "victoria." + component.Name(),
				Kind:   "substrate",
				Name:   component.DisplayName(),
				Role:   "observability",
				Status: "degraded",
				URL:    component.BaseURL(),
			})
		}(component)
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func emitVictoriaSubstrateEvent(events *devEventSink, ctx context.Context, status, message string, fields map[string]any, sourceOverride ...devdash.DevSource) {
	if events == nil {
		return
	}
	source := devdash.DevSource{
		ID:     "victoria",
		Kind:   "substrate",
		Name:   "Victoria stack",
		Role:   "observability",
		Status: status,
	}
	if len(sourceOverride) > 0 {
		source = sourceOverride[0]
	}
	events.Emit(ctx, source, levelForSubstrateStatus(status), message, fields)
}

func levelForSubstrateStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "degraded", "exited", "unavailable":
		return "error"
	default:
		return "info"
	}
}
