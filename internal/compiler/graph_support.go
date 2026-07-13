package compiler

import graphmodel "scenery.sh/internal/graph"

const (
	agentMaxResources = graphmodel.AgentMaxResources
	agentMaxBytes     = graphmodel.AgentMaxBytes
	agentMaxDepth     = graphmodel.AgentMaxDepth
	contextTokenTTL   = graphmodel.ContextTokenTTL
)

type GraphOptions = graphmodel.GraphOptions
type GraphEdge = graphmodel.GraphEdge
type ResourceGraph = graphmodel.ResourceGraph
type ContextOptions = graphmodel.ContextOptions
type ContextBundle = graphmodel.ContextBundle
type contextTokenPayload = graphmodel.ContextToken

var (
	Graph                          = graphmodel.Graph
	Context                        = graphmodel.Context
	ContextSnapshot                = graphmodel.ContextSnapshot
	ContextSnapshotWithDiagnostics = graphmodel.ContextSnapshotWithDiagnostics
	contextAt                      = graphmodel.ContextAt
	resourceEdges                  = graphmodel.ResourceEdges
	walkRefs                       = graphmodel.WalkReferences
	parseContextToken              = graphmodel.ParseContextToken
	setFieldProvenanceTree         = graphmodel.SetFieldProvenanceTree
	setFieldProvenance             = graphmodel.SetFieldProvenance
	markExpansionFieldProvenance   = graphmodel.MarkExpansionFieldProvenance
	provenanceChildPath            = graphmodel.ProvenanceChildPath
	rebaseFieldProvenance          = graphmodel.RebaseFieldProvenance
	ensureFieldProvenance          = graphmodel.EnsureFieldProvenance
	nearestFieldProvenance         = graphmodel.NearestFieldProvenance
	appendUniqueString             = graphmodel.AppendUniqueString
	canonicalResources             = graphmodel.CanonicalResources
	contractRevision               = graphmodel.ContractRevision
	contractResourceProjection     = graphmodel.ContractResourceProjection
	revisionHash                   = graphmodel.RevisionHash
	isCanonicalSHA256Digest        = graphmodel.IsCanonicalSHA256Digest
	resourceAddress                = graphmodel.ResourceAddress
	moduleResourceAddress          = graphmodel.ModuleResourceAddress
	kindForBlock                   = graphmodel.KindForBlock
)
