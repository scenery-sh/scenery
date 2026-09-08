package main

import "scenery.sh/internal/repoinfo"

var buildInspectDocsAgents = repoinfo.BuildAgents

type docsKnowledgeDocument = repoinfo.KnowledgeDocument
type harnessPackageInfo = repoinfo.PackageInfo
type inspectDocsAgentScope = repoinfo.AgentScope
type inspectDocsAgents = repoinfo.Agents
type inspectDocsArtifactRef = repoinfo.ArtifactRef
type inspectDocsDocument = repoinfo.Document
type inspectDocsOptions = repoinfo.Options
type inspectDocsPlans = repoinfo.Plans
type inspectDocsQuery = repoinfo.Query
type inspectDocsResponse = repoinfo.Response
type inspectDocsSection = repoinfo.Section
type inspectDocsSummary = repoinfo.Summary

var readDocsKnowledgeIndex = repoinfo.ReadKnowledge
var docsDocumentReviewDue = repoinfo.DocumentReviewDue
var isCompletedExecPlanDocument = repoinfo.IsCompletedPlan
var isExecPlanPath = repoinfo.IsExecPlanPath
var populateHarnessChangedAreaReport = repoinfo.PopulateChangedArea
var isIgnoredHarnessLocalArtifact = repoinfo.IsLocalArtifact
var discoverSceneryRepoRoot = repoinfo.DiscoverRoot
var findSceneryRepoRoot = repoinfo.FindRoot

const docsIndexKind = repoinfo.IndexKind
const inspectDocsKind = repoinfo.InspectKind

var docsIndexSchemaRevision = repoinfo.IndexSchemaRevision

const harnessValidationDocumentation = repoinfo.ValidationDocumentation
const harnessValidationGoPackage = repoinfo.ValidationGoPackage
const harnessValidationCLIJSONContract = repoinfo.ValidationCLIJSONContract
const harnessValidationCompilerGenerator = repoinfo.ValidationCompilerGenerator
const harnessValidationUICatalog = repoinfo.ValidationUICatalog
const harnessValidationDashboard = repoinfo.ValidationDashboard
const harnessValidationReleaseRuntime = repoinfo.ValidationReleaseRuntime
const harnessValidationRepositoryFallback = repoinfo.ValidationRepositoryFallback
const harnessValidationQuickCommand = repoinfo.ValidationQuickCommand
const harnessValidationFullCommand = repoinfo.ValidationFullCommand
const harnessValidationUICommand = repoinfo.ValidationUICommand

var harnessFixtureRegenerationCommands = repoinfo.FixtureRegenerationCommands
