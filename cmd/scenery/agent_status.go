package main

import (
	"context"

	localagent "scenery.sh/internal/agent"
)

type statusSubstrateClient interface {
	ListSubstrates(context.Context) ([]localagent.Substrate, error)
	DeleteSubstrate(context.Context, string) (localagent.Substrate, error)
}
