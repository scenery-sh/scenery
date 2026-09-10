package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
)

// assistantStage owns candidate-private preparation. Callers hold lifecycle
// from capture/staging through activation and release, so helper-only watches
// cannot retire a retained rollback tree or publish a competing generation.
type assistantStage struct {
	contract *compiler.Result
	prepared map[string]assistantPreparedRuntime
	failures map[string]error
}

func (s *assistantSupervisor) captureStage() *assistantStage {
	s.mu.Lock()
	defer s.mu.Unlock()
	stage := &assistantStage{contract: s.contract, prepared: make(map[string]assistantPreparedRuntime, len(s.prepared))}
	for address, prepared := range s.prepared {
		stage.prepared[address] = prepared
	}
	return stage
}

func (s *assistantSupervisor) stage(ctx context.Context, result *compiler.Result) (*assistantStage, error) {
	if ctx == nil {
		ctx = s.ctx
	}
	stage := &assistantStage{contract: result, prepared: map[string]assistantPreparedRuntime{}, failures: map[string]error{}}
	var failures []error
	for _, definition := range assistantDefinitionsFromResult(result, s.config.Root) {
		started := time.Now()
		s.mu.Lock()
		prepared, ok := s.prepared[definition.Address]
		s.mu.Unlock()
		if ok && prepared.hasDescriptor() && prepared.definition.Identity == definition.Identity && (!s.config.UseAppGateway || prepared.overlay.Root != "") {
			stage.prepared[definition.Address] = prepared
			s.emitStep(ctx, definition, "assistant.stage", started, "hit", "prepared_identity_unchanged", nil)
			continue
		}
		prepared, err := newAssistantPreparedRuntime(result, definition)
		if err == nil && s.config.UseAppGateway {
			err = s.materializeOverlay(ctx, &prepared)
		}
		stage.prepared[definition.Address] = prepared
		if err != nil {
			stage.failures[definition.Address] = err
			failures = append(failures, fmt.Errorf("stage assistant %s: %w", definition.Address, err))
		}
		s.emitStep(ctx, definition, "assistant.stage", started, "miss", "candidate_private_preparation", err)
	}
	return stage, errors.Join(failures...)
}

func (prepared assistantPreparedRuntime) hasDescriptor() bool {
	return prepared.controlURL != "" && prepared.controlToken != "" && prepared.mcpListenAddress != "" && len(prepared.bridgeSecret) != 0
}

func newAssistantPreparedRuntime(result *compiler.Result, definition assistantDefinition) (assistantPreparedRuntime, error) {
	prepared := assistantPreparedRuntime{definition: definition, approvalNeverTools: assistantApprovalNeverTools(result, definition.MCPServer)}
	var err error
	if prepared.controlURL, err = assistantControlURLAllocator(); err != nil {
		return prepared, err
	}
	if prepared.controlToken, err = randomToken(); err != nil {
		return prepared, err
	}
	if prepared.mcpListenAddress, err = assistantMCPListenAddressAllocator(); err != nil {
		return prepared, err
	}
	prepared.mcpURL = "http://" + prepared.mcpListenAddress
	prepared.bridgeSecret, err = randomSecret()
	return prepared, err
}

// activateStage changes live descriptors only after the old app has stopped.
// Retired roots survive until releaseStage, including across candidate failure.
func (s *assistantSupervisor) activateStage(ctx context.Context, stage *assistantStage) error {
	if stage == nil {
		return errors.New("assistant candidate stage is missing")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("assistant supervisor is closed")
	}
	var stale []*assistantProcessInstance
	for address, instance := range s.instances {
		prepared, ok := stage.prepared[address]
		if !ok || instance.stopping || prepared.definition.Identity != instance.definition.Identity || prepared.overlay.Root != instance.overlay.Root {
			stale = append(stale, instance)
		}
	}
	s.mu.Unlock()
	for _, instance := range stale {
		if err := s.stopInstance(instance); err != nil {
			return fmt.Errorf("stop previous assistant: %w", err)
		}
		s.mu.Lock()
		delete(s.instances, instance.definition.Address)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.contract = stage.contract
	s.prepared = make(map[string]assistantPreparedRuntime, len(stage.prepared))
	s.ownedRoots = make(map[string]string, len(stage.prepared))
	statuses := make(map[string]AssistantStatusRecord, len(stage.prepared))
	for address, prepared := range stage.prepared {
		s.prepared[address] = prepared
		if prepared.ownedRoot != "" {
			s.ownedRoots[address] = prepared.ownedRoot
		}
		definition := prepared.definition
		state := s.statuses[address]
		if instance := s.instances[address]; instance == nil {
			state = AssistantStatusRecord{
				Address: address, Name: definition.Name, SourceID: "assistant:" + definition.Name,
				State: string(assistantruntime.StateStarting), Required: definition.Required,
				RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision,
				LogSource: "assistant:" + definition.Name, RestartCount: s.restarts[address],
				ControlAddress: prepared.controlURL, MCPAddress: prepared.mcpURL, OverlayPath: prepared.overlay.Root,
			}
		}
		if err := stage.failures[address]; err != nil {
			state.State = string(assistantruntime.StateUnavailable)
			state.Ready = false
			state.LastFailure = assistantFailureCode(err)
			state.LastFailureAt = s.config.Now().UTC()
		}
		statuses[address] = state
	}
	s.statuses = statuses
	s.mu.Unlock()
	s.publishStatuses()
	return nil
}

func (s *assistantSupervisor) releaseStage(stage *assistantStage) {
	if stage == nil {
		return
	}
	s.mu.Lock()
	active := make(map[string]bool, len(s.ownedRoots))
	for _, root := range s.ownedRoots {
		active[root] = true
	}
	s.mu.Unlock()
	for _, prepared := range stage.prepared {
		if prepared.ownedRoot != "" && !active[prepared.ownedRoot] {
			_ = os.RemoveAll(prepared.ownedRoot)
		}
	}
}
