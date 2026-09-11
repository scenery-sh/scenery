package main

import (
	"context"
	"errors"
	"fmt"

	"scenery.sh/internal/assistantruntime"
)

// A started helper remains owned until shutdown is confirmed, even if it never
// became ready. Retaining its instance also blocks retries and stage replacement.
func (s *assistantSupervisor) failStartedDefinition(ctx context.Context, instance *assistantProcessInstance, failure error) error {
	if stopErr := s.stopInstance(instance); stopErr != nil {
		definition := instance.definition
		s.mu.Lock()
		s.instances[definition.Address] = instance
		status := s.statuses[definition.Address]
		status.State = string(assistantruntime.StateUnavailable)
		status.Ready = false
		status.PID = instance.process.PID
		status.ControlAddress = instance.controlURL
		status.OverlayPath = instance.overlay.Root
		status.LastFailure = assistantFailureCode(failure)
		status.LastFailureAt = s.config.Now().UTC()
		s.statuses[definition.Address] = status
		s.mu.Unlock()
		s.publishStatuses()
		s.reportProcess(definition.Name, instance.process.PID)
		s.emit(ctx, definition, "error", "assistant startup failed and shutdown is unconfirmed; replacement refused", map[string]any{"error_code": status.LastFailure})
		return errors.Join(failure, fmt.Errorf("assistant shutdown unconfirmed: %w", stopErr))
	}
	return s.failDefinition(ctx, instance.definition, failure)
}
