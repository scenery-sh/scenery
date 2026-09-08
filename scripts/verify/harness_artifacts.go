package main

import "scenery.sh/internal/harnessevidence"

var annotateHarnessEvidence = harnessevidence.Annotate
var exitCodeFromError = harnessevidence.ExitCode
var finalizeHarnessEvidence = harnessevidence.Finalize
type harnessArtifactContext = harnessevidence.Context

const harnessArtifactEvidenceKind = harnessevidence.Kind

var harnessStepEvidenceCWD = harnessevidence.StepCWD
var newHarnessArtifact = harnessevidence.NewArtifact
var newHarnessArtifactContext = harnessevidence.NewContext
var newHarnessEvidence = harnessevidence.New
var optionalHarnessArtifactContext = harnessevidence.OptionalContext
var reproCommand = harnessevidence.ReproCommand
var shellQuote = harnessevidence.ShellQuote
var writeHarnessOutputEvidenceArtifacts = harnessevidence.WriteOutputArtifacts

func intPtr(value int) *int { return &value }
