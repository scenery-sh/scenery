package main

import "scenery.sh/internal/repoinfo"

type harnessPackageInfo = repoinfo.PackageInfo

var populateHarnessChangedAreaReport = repoinfo.PopulateChangedArea
var discoverSceneryRepoRoot = repoinfo.DiscoverRoot

const harnessValidationQuickCommand = repoinfo.ValidationQuickCommand
const harnessValidationFullCommand = repoinfo.ValidationFullCommand
