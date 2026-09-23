package generate

import (
	"go/token"
	"go/types"
	"testing"
)

func TestContractTypeMatchesGeneratedAliases(t *testing.T) {
	contract := types.NewPackage("example.com/app/recorder/scenerycontract", "scenerycontract")
	other := types.NewPackage("example.com/app/recorder", "recorder")
	record := types.NewNamed(types.NewTypeName(token.NoPos, contract, "SessionRef", nil), types.NewStruct(nil, nil), nil)
	contract.Scope().Insert(record.Obj())
	alias := types.NewAlias(types.NewTypeName(token.NoPos, contract, "FinishSessionInput", nil), record)
	contract.Scope().Insert(alias.Obj())
	distinct := types.NewNamed(types.NewTypeName(token.NoPos, contract, "ListSessionsInput", nil), types.NewStruct(nil, nil), nil)
	contract.Scope().Insert(distinct.Obj())
	local := types.NewNamed(types.NewTypeName(token.NoPos, other, "FinishSessionInput", nil), types.NewStruct(nil, nil), nil)

	cases := []struct {
		name string
		got  types.Type
		want string
		ok   bool
	}{
		{"exact generated name", distinct, "ListSessionsInput", true},
		{"alias spelled by its name", alias, "FinishSessionInput", true},
		{"shared record behind the alias", record, "FinishSessionInput", true},
		{"a different contract record", distinct, "FinishSessionInput", false},
		{"same name outside the contract", local, "FinishSessionInput", false},
		{"missing contract name", record, "MissingInput", false},
	}
	for _, test := range cases {
		if got := contractTypeMatches(test.got, test.want); got != test.ok {
			t.Errorf("%s: contractTypeMatches = %v, want %v", test.name, got, test.ok)
		}
	}
}
