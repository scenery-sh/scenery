package harnessreport

import (
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

// Provenance survives when an artifact is read outside its CLI envelope.
type Provenance struct {
	SpecRevision string           `json:"spec_revision"`
	Producer     machine.Producer `json:"producer"`
}

func CurrentProvenance() Provenance {
	return Provenance{SpecRevision: string(spec.CurrentRevision()), Producer: machine.RuntimeProducer()}
}
