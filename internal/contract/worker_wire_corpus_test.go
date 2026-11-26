package contract

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"testing"
)

//go:embed testdata/worker-wire-v1.json
var workerWireCorpus []byte

//go:embed testdata/worker-keys-v1.json
var workerKeysCorpus []byte

func TestWorkerWireV1Corpus(t *testing.T) {
	types := ManifestTypes{
		"probe/record/patch": {Kind: "record", Fields: []ManifestField{{Name: "note", Type: "optional(nullable(string))"}}},
		"probe/enum/status":  {Kind: "enum", Values: []string{"in_review"}},
		"probe/record/note": {Kind: "record", UnknownFields: "preserve", Fields: []ManifestField{
			{Name: "text", Type: "string"}, {Name: "note", Type: "optional(nullable(string))"}, {Name: "ratio", Type: "optional(float64)"},
		}},
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
			var got []byte
			var err error
			if entry.Type == "probe/outcome/quote" {
				kind, name, payload, decodeErr := DecodeContractOutcomeEnvelope([]byte(entry.Wire))
				err = decodeErr
				if err == nil {
					got, err = EncodeManifestOutcome(kind, name, payload)
				}
			} else {
				got, err = types.Normalize(entry.Type, []byte(entry.Wire))
			}
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

// TestWorkerKeysV1Corpus pins the durable deduplication and concurrency keys
// of normalized worker inputs. The Rust host builds its keys from the same
// corpus, so a job enqueued by one host deduplicates against the other's.
func TestWorkerKeysV1Corpus(t *testing.T) {
	var cases []struct {
		Name      string
		Fields    []struct{ Name, Type string }
		Input     string
		Canonical string
		Reject    bool
	}
	if err := json.Unmarshal(workerKeysCorpus, &cases); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		t.Run(entry.Name, func(t *testing.T) {
			var input map[string]json.RawMessage
			if err := json.Unmarshal([]byte(entry.Input), &input); err != nil {
				t.Fatal(err)
			}
			var key string
			var err error
			components := make([][]byte, len(entry.Fields))
			for index, field := range entry.Fields {
				if components[index], err = EncodeWireKeyComponent(input[field.Name], field.Type); err != nil {
					break
				}
			}
			if err == nil {
				key, err = EncodeContractCompositeKey(components...)
			}
			if entry.Reject {
				if err == nil {
					t.Fatalf("accepted %s as %s", entry.Input, key)
				}
				return
			}
			if want := "scenery-key-v1." + base64.RawURLEncoding.EncodeToString([]byte(entry.Canonical)); err != nil || key != want {
				t.Fatalf("key = %s, %v; want %s", key, err, want)
			}
		})
	}
}
