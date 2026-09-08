package main

import "scenery.sh/internal/repoinfo"

var buildInspectDocsAgents = repoinfo.BuildAgents

type docsKnowledgeDocument = repoinfo.KnowledgeDocument
type harnessPackageInfo = repoinfo.PackageInfo
type inspectDocsResponse = repoinfo.Response
var readDocsKnowledgeIndex = repoinfo.ReadKnowledge
var docsDocumentReviewDue = repoinfo.DocumentReviewDue
var populateHarnessChangedAreaReport = repoinfo.PopulateChangedArea
var isIgnoredHarnessLocalArtifact = repoinfo.IsLocalArtifact
var harnessOnlvImpactingPath = repoinfo.OnlvImpactingPath
var sortedStringSet = repoinfo.SortedStringSet
var appendUniqueSorted = repoinfo.AppendUniqueSorted
var discoverSceneryRepoRoot = repoinfo.DiscoverRoot
const docsIndexKind = repoinfo.IndexKind
const inspectDocsKind = repoinfo.InspectKind

var docsIndexSchemaRevision = repoinfo.IndexSchemaRevision

const harnessValidationCLIJSONContract = repoinfo.ValidationCLIJSONContract
const harnessValidationUICatalog = repoinfo.ValidationUICatalog
const harnessValidationDashboard = repoinfo.ValidationDashboard
const harnessValidationReleaseRuntime = repoinfo.ValidationReleaseRuntime
const harnessValidationQuickCommand = repoinfo.ValidationQuickCommand
