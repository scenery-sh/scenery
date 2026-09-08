package main

import "scenery.sh/internal/harnessreport"

type checkDiagnostic = harnessreport.Diagnostic
type harnessArtifact = harnessreport.Artifact
type harnessArtifactHygieneReport = harnessreport.ArtifactHygieneReport
type harnessCLIContractCommand = harnessreport.CLIContractCommand
type harnessCLIContractReport = harnessreport.CLIContractReport
type harnessChangedAreaReport = harnessreport.ChangedAreaReport
type harnessChangedFile = harnessreport.ChangedFile
type harnessDriftReport = harnessreport.DriftReport
type harnessEmbedFinding = harnessreport.EmbedFinding
type harnessEmbedReport = harnessreport.EmbedReport
type harnessEnvValue = harnessreport.EnvValue
type harnessEnvVarFinding = harnessreport.EnvVarFinding
type harnessEnvVarReport = harnessreport.EnvVarReport
type harnessEvidence = harnessreport.Evidence
type harnessEvidenceArtifact = harnessreport.EvidenceArtifact
type harnessFixtureMatrixReport = harnessreport.FixtureMatrixReport
type harnessFixtureResult = harnessreport.FixtureResult
type harnessKnowledge = harnessreport.Knowledge
type harnessKnowledgeFile = harnessreport.KnowledgeFile
type harnessPackageTiming = harnessreport.PackageTiming
type harnessSchemaValidationItem = harnessreport.SchemaValidationItem
type harnessSchemaValidationReport = harnessreport.SchemaValidationReport
type harnessSelfRepo = harnessreport.SelfRepo
type harnessSelfResponse = harnessreport.SelfResponse
type harnessSelfSummaryResponse = harnessreport.SelfSummaryResponse
type harnessStep = harnessreport.Step
type harnessTestBinaryBuild = harnessreport.TestBinaryBuild
type harnessTestBinaryTiming = harnessreport.TestBinaryTiming
type harnessTestTiming = harnessreport.TestTiming
type harnessTestTimingBudgets = harnessreport.TestTimingBudgets
type harnessTestTimingException = harnessreport.TestTimingException
type harnessTestTimingReport = harnessreport.TestTimingReport
type harnessTimingDeferral = harnessreport.TimingDeferral
type harnessToolchainReport = harnessreport.ToolchainReport
type harnessToolchainTool = harnessreport.ToolchainTool
const harnessSelfSummaryKind = harnessreport.SummaryKind

var buildHarnessSelfSummary = harnessreport.BuildSummary
var tailString = harnessreport.TailString
var countDiagnosticsBySeverity = harnessreport.CountDiagnostics
var sanitizeHarnessArtifactName = harnessreport.ArtifactName
