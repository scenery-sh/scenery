package contract

import (
	_ "embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/worker-wire-v1.json
var workerWireCorpus []byte

func TestWorkerWireV1Corpus(t *testing.T) {
	types := ManifestTypes{
		"probe/record/patch": {Kind: "record", Fields: []ManifestField{{Name: "note", Type: "optional(nullable(string))"}}},
		"probe/enum/status":  {Kind: "enum", Values: []string{"in_review"}},
	}
	var cases []struct {
		Name   string
		Type   string
		Wire   string
		Reject bool
	}
	if err := json.Unmarshal(workerWireCorpus, &cases); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		t.Run(entry.Name, func(t *testing.T) {
			got, err := types.Normalize(entry.Type, []byte(entry.Wire))
			if entry.Reject {
				if err == nil {
					t.Fatalf("accepted %s", entry.Wire)
				}
			} else if err != nil || string(got) != entry.Wire {
				t.Fatalf("normalized %s to %s with %v", entry.Wire, got, err)
			}
		})
	}
}
