package main

import "scenery.sh/internal/repoinfo"

var buildInspectDocsAgents = repoinfo.BuildAgents

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
var discoverSceneryRepoRoot = repoinfo.DiscoverRoot

const inspectDocsKind = repoinfo.InspectKind

const harnessValidationQuickCommand = repoinfo.ValidationQuickCommand
const harnessValidationFullCommand = repoinfo.ValidationFullCommand
