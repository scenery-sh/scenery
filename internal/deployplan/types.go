// Package deployplan owns immutable deployment plans, provider planning, and
// crash-safe application of a compiler-resolved deployment projection.
package deployplan

import (
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
)

type DeploymentProjection = compiler.DeploymentProjection
type DeploymentResourceProjection = compiler.DeploymentResourceProjection
type Result = compiler.Result
type Manifest = graph.Manifest
type Resource = graph.Resource
type Diagnostic = graph.Diagnostic
type ApprovalToken = evolution.ApprovalToken
type ApprovalVerifier = evolution.ApprovalVerifier
