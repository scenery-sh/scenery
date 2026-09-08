package main

import "scenery.sh/internal/harnessevidence"

var annotateHarnessEvidence = harnessevidence.Annotate
var evidenceArtifactsFromHarnessArtifacts = harnessevidence.ArtifactReferences
var exitCodeFromError = harnessevidence.ExitCode
var finalizeHarnessEvidence = harnessevidence.Finalize
var formatArtifactWriteError = harnessevidence.WriteDiagnostic

type harnessArtifactContext = harnessevidence.Context

const harnessArtifactEvidenceKind = harnessevidence.Kind

var newHarnessArtifact = harnessevidence.NewArtifact
var newHarnessArtifactContext = harnessevidence.NewContext
var newHarnessEvidence = harnessevidence.New
var newHarnessEvidenceArtifact = harnessevidence.NewArtifactReference
var optionalHarnessArtifactContext = harnessevidence.OptionalContext
var sanitizeHarnessArtifactFilename = harnessevidence.ArtifactFilename
var writeHarnessOutputEvidenceArtifacts = harnessevidence.WriteOutputArtifacts

func intPtr(value int) *int { return &value }
