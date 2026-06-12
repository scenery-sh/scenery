package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

type managedSubstrateManager struct {
	agent  *localagent.Client
	events *devEventSink
}

var managedSubstrateProcessLocks sync.Map

type managedSubstrateAdapter interface {
	Kind() string
	SourceID() string
	SourceName() string
	Role() string
	Start(ctx context.Context, root string) (managedSubstrateHandle, error)
	FromSubstrate(ctx context.Context, substrate localagent.Substrate) (managedSubstrateHandle, bool)
	ReadyFields(handle managedSubstrateHandle) map[string]any
	ReuseFields(handle managedSubstrateHandle, substrate localagent.Substrate) map[string]any
	ExitStatus(component managedSubstrateComponent) string
	ExitMessage(component managedSubstrateComponent) string
	EventSource(handle managedSubstrateHandle, component managedSubstrateComponent, status string) devdash.DevSource
}

type managedSubstrateHandle interface {
	SubstrateRequest(ownerPID int) localagent.UpsertSubstrateRequest
	MarkExternal()
	Components() []managedSubstrateComponent
}

type managedSubstrateComponent struct {
	Name        string
	DisplayName string
	Role        string
	URL         string
	Done        <-chan error
	ExitRecord  func(error) localagent.SubstrateExit
}

func (m managedSubstrateManager) Ensure(ctx context.Context, root string, adapter managedSubstrateAdapter) (managedSubstrateHandle, bool, error) {
	if adapter == nil {
		return nil, false, nil
	}
	if m.agent == nil {
		handle, err := adapter.Start(ctx, root)
		return handle, false, err
	}
	kind := adapter.Kind()
	processUnlock := lockManagedSubstrateProcess(root, kind)
	defer processUnlock()
	unlock, err := lockManagedSubstrateRoot(root, kind)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	if substrate, err := m.agent.GetSubstrate(ctx, kind); err == nil {
		handle, reusable := m.reusable(ctx, adapter, substrate)
		if reusable {
			emitSubstrateManagerEvent(m.events, ctx, adapter, handle, "running", fmt.Sprintf("shared %s reused", adapter.SourceName()), adapter.ReuseFields(handle, substrate))
			return handle, true, nil
		}
		_, _ = m.agent.DeleteSubstrate(ctx, kind)
	}
	handle, err := adapter.Start(ctx, root)
	if handle == nil || err != nil {
		return handle, false, err
	}
	req := handle.SubstrateRequest(os.Getpid())
	if strings.TrimSpace(req.Kind) == "" {
		req.Kind = kind
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = "ready"
	}
	if _, err := m.agent.UpsertSubstrate(ctx, req); err != nil {
		return handle, false, err
	}
	handle.MarkExternal()
	emitSubstrateManagerEvent(m.events, ctx, adapter, handle, "running", fmt.Sprintf("shared %s ready", adapter.SourceName()), adapter.ReadyFields(handle))
	return handle, false, nil
}

func lockManagedSubstrateProcess(root, kind string) func() {
	keyRoot := strings.TrimSpace(root)
	if keyRoot == "" {
		keyRoot = os.TempDir()
	}
	if abs, err := filepath.Abs(keyRoot); err == nil {
		keyRoot = abs
	}
	key := filepath.Clean(keyRoot) + "\x00" + strings.TrimSpace(kind)
	value, _ := managedSubstrateProcessLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (m managedSubstrateManager) reusable(ctx context.Context, adapter managedSubstrateAdapter, substrate localagent.Substrate) (managedSubstrateHandle, bool) {
	if strings.TrimSpace(substrate.Status) != "" && strings.TrimSpace(substrate.Status) != "ready" {
		return nil, false
	}
	if err := verifySubstrateOwner(substrate); err != nil {
		return nil, false
	}
	handle, ok := adapter.FromSubstrate(ctx, substrate)
	if !ok || handle == nil {
		return nil, false
	}
	handle.MarkExternal()
	return handle, true
}

func (m managedSubstrateManager) Monitor(handle managedSubstrateHandle, adapter managedSubstrateAdapter) <-chan struct{} {
	done := make(chan struct{})
	if m.agent == nil || handle == nil || adapter == nil {
		close(done)
		return done
	}
	components := handle.Components()
	if len(components) == 0 {
		close(done)
		return done
	}
	var wg sync.WaitGroup
	for _, component := range components {
		component := component
		if component.Done == nil || component.ExitRecord == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err, ok := <-component.Done
			if !ok {
				return
			}
			exit := component.ExitRecord(err)
			status := strings.TrimSpace(adapter.ExitStatus(component))
			if status == "" {
				status = "degraded"
			}
			req := handle.SubstrateRequest(os.Getpid())
			req.Status = status
			req.LastExit = &exit
			req.ComponentExits = map[string]localagent.SubstrateExit{component.Name: exit}
			_, _ = m.agent.UpsertSubstrate(context.Background(), req)
			source := adapter.EventSource(handle, component, status)
			if source.ID == "" {
				source = devdash.DevSource{
					ID:     adapter.SourceID(),
					Kind:   "substrate",
					Name:   adapter.SourceName(),
					Role:   adapter.Role(),
					PID:    fmt.Sprint(exit.PID),
					Status: status,
					URL:    component.URL,
				}
			}
			emitSubstrateManagerEvent(m.events, context.Background(), adapter, handle, status, adapter.ExitMessage(component), substrateExitEventFields(exit), source)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func emitSubstrateManagerEvent(events *devEventSink, ctx context.Context, adapter managedSubstrateAdapter, handle managedSubstrateHandle, status, message string, fields map[string]any, sourceOverride ...devdash.DevSource) {
	if events == nil || adapter == nil {
		return
	}
	source := devdash.DevSource{
		ID:     adapter.SourceID(),
		Kind:   "substrate",
		Name:   adapter.SourceName(),
		Role:   adapter.Role(),
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
