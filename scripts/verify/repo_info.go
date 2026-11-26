package main

import "scenery.sh/internal/repoinfo"

type harnessPackageInfo = repoinfo.PackageInfo

var populateHarnessChangedAreaReport = repoinfo.PopulateChangedArea
var isIgnoredHarnessLocalArtifact = repoinfo.IsLocalArtifact
var harnessOnlvImpactingPath = repoinfo.OnlvImpactingPath
var sortedStringSet = repoinfo.SortedStringSet
var appendUniqueSorted = repoinfo.AppendUniqueSorted
var discoverSceneryRepoRoot = repoinfo.DiscoverRoot

const harnessValidationCLIJSONContract = repoinfo.ValidationCLIJSONContract
const harnessValidationUICatalog = repoinfo.ValidationUICatalog
const harnessValidationReleaseRuntime = repoinfo.ValidationReleaseRuntime
const harnessValidationQuickCommand = repoinfo.ValidationQuickCommand
