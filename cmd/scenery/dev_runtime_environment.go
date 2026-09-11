package main

import (
	"context"

	"scenery.sh/internal/compiler"
)

// devRuntimeEnvironment is private to one candidate preparation. Database
// setup and child launch share the same freshly resolved endpoints; the next
// rebuild/start resolves ownership and environment again.
type devRuntimeEnvironment struct {
	base, managed, storage []string
}

func (s *devSupervisor) prepareRuntimeEnvironment(ctx context.Context, contract *compiler.Result) (*devRuntimeEnvironment, error) {
	base, err := appEnvWithDotEnv(s.processEnvironment(), s.root, s.env.DotEnvFiles()...)
	if err != nil {
		return nil, err
	}
	managed, err := s.managedAppEnv(ctx, base, contract.SQLRequirements)
	if err != nil {
		return nil, err
	}
	storage, err := storageCapabilityEnv(ctx, s.root, s.cfg, s.currentAgentSession(), base, "")
	if err != nil {
		return nil, err
	}
	return &devRuntimeEnvironment{base: base, managed: managed, storage: storage}, nil
}
