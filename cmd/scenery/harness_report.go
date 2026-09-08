package main

import "scenery.sh/internal/harnessreport"

type checkDiagnostic = harnessreport.Diagnostic
type harnessArtifact = harnessreport.Artifact
type harnessChangedAreaReport = harnessreport.ChangedAreaReport
type harnessChangedFile = harnessreport.ChangedFile
type harnessDriftReport = harnessreport.DriftReport
type harnessEvidence = harnessreport.Evidence
type harnessEvidenceArtifact = harnessreport.EvidenceArtifact
type harnessKnowledge = harnessreport.Knowledge
type harnessKnowledgeFile = harnessreport.KnowledgeFile
type harnessPackageTiming = harnessreport.PackageTiming
type harnessSelfRepo = harnessreport.SelfRepo
type harnessSelfResponse = harnessreport.SelfResponse
type harnessStep = harnessreport.Step
type harnessTestTiming = harnessreport.TestTiming
type harnessTestTimingBudgets = harnessreport.TestTimingBudgets
type harnessTestTimingReport = harnessreport.TestTimingReport
const harnessSelfSummaryKind = harnessreport.SummaryKind

const (
	harnessChangedAreaKind      = "scenery.harness.changed_area"
	harnessTestTimingKind       = "scenery.harness.test_timing"
	harnessAgentContextKind     = "scenery.agent_context"
	harnessToolchainKind        = "scenery.harness.toolchain"
	harnessDriftKind            = "scenery.harness.drift"
	harnessFixtureMatrixKind    = "scenery.harness.fixture_matrix"
	harnessSchemaValidationKind = "scenery.harness.schema_validation"
)

var buildHarnessSelfSummary = harnessreport.BuildSummary
var capDiagnostics = harnessreport.CapDiagnostics
var capPackages = harnessreport.CapPackages
var capTests = harnessreport.CapTests
var normalizeLikelyPath = harnessreport.NormalizeLikelyPath
var sanitizeHarnessArtifactName = harnessreport.ArtifactName
