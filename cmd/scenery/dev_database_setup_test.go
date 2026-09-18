package main

import (
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func TestDatabaseSetupInputsDescribeOnlyAnEqualContract(t *testing.T) {
	t.Parallel()
	contract := func(revision, schema string) *compiler.Result {
		return &compiler.Result{
			Manifest:        &graph.Manifest{ContractRevision: revision},
			SQLRequirements: compiler.SQLRequirements{{Name: "main", Schema: schema}},
		}
	}
	read := func(contract *compiler.Result) devDatabaseSetupInputs {
		return devDatabaseSetupInputs{read: true, revision: contract.Manifest.ContractRevision, sql: contract.SQLRequirements}
	}
	inputs := read(contract("revision-1", "public"))
	for name, candidate := range map[string]*compiler.Result{
		"changed revision":     contract("revision-2", "public"),
		"changed requirements": contract("revision-1", "tenant"),
		"no manifest":          {SQLRequirements: compiler.SQLRequirements{{Name: "main", Schema: "public"}}},
		"no contract":          nil,
	} {
		if inputs.describes(candidate) {
			t.Errorf("%s: inputs read for another contract were reused", name)
		}
	}
	// The checked contract is a clone of the prepared one.
	if !inputs.describes(contract("revision-1", "public")) {
		t.Fatal("inputs do not describe an equal contract")
	}
	if (devDatabaseSetupInputs{}).describes(contract("", "public")) || read(contract("", "public")).describes(contract("", "public")) {
		t.Fatal("inputs without a revision were reused")
	}
}
