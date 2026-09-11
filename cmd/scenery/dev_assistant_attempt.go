package main

import (
	"context"
	"slices"

	"scenery.sh/internal/compiler"
)

// One startup owns this speculative stage while holding assistant lifecycle.
// Cancellation always joins preparation before releasing its private trees.
type assistantStageAttempt struct {
	supervisor *assistantSupervisor
	contract   *compiler.Result
	cancel     context.CancelFunc
	done       chan struct{}
	stage      *assistantStage
	err        error
}

func (s *assistantSupervisor) beginStage(ctx context.Context, contract *compiler.Result) *assistantStageAttempt {
	ctx, cancel := context.WithCancel(ctx)
	attempt := &assistantStageAttempt{supervisor: s, contract: contract, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(attempt.done)
		attempt.stage, attempt.err = s.stage(ctx, contract)
	}()
	return attempt
}

func (a *assistantStageAttempt) wait() (*assistantStage, error) {
	<-a.done
	return a.stage, a.err
}

func (a *assistantStageAttempt) release() {
	a.cancel()
	stage, _ := a.wait()
	a.supervisor.releaseStage(stage)
}

func (a *assistantStageAttempt) matches(contract *compiler.Result) bool {
	if contract == nil || contract.Manifest == nil || a.contract.Manifest == nil ||
		a.contract.Root != contract.Root || a.contract.WorkspaceRevision == "" ||
		a.contract.WorkspaceRevision != contract.WorkspaceRevision ||
		a.contract.Manifest.ContractRevision != contract.Manifest.ContractRevision {
		return false
	}
	return slices.Equal(assistantDefinitionsFromResult(a.contract, a.supervisor.config.Root), assistantDefinitionsFromResult(contract, a.supervisor.config.Root))
}
