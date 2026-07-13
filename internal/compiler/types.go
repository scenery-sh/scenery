package compiler

import (
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

const ManifestKind = graph.ManifestKind

var (
	ManifestSchemaRevision = graph.ManifestSchemaRevision
	DiagnosticCatalog      = graph.DiagnosticCatalog
)

type Diagnostic = graph.Diagnostic
type Resource = graph.Resource
type Manifest = graph.Manifest
type PartialGraph = graph.PartialGraph
type SourceRecord = graph.SourceRecord
type FieldProvenance = graph.FieldProvenance
type Origin = graph.Origin
type Related = graph.Related
type ApplicationIdentity = graph.ApplicationIdentity
type Range = scn.Range
type Position = scn.Position
type ExpansionStep = graph.ExpansionStep
