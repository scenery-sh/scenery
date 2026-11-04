package machine

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkJSONV2MachineEnvelope(b *testing.B) {
	rows := make([]map[string]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"name": fmt.Sprintf("row-%d", i), "count": i, "tags": []string{"one", "two"}}
	}
	encoded, err := json.Marshal(NewEnvelope(testSpecRevision, testProducer(), true, rows, []string{}))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Decode[string](encoded, testSpecRevision); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONV2Artifact(b *testing.B) {
	type artifact struct {
		ArtifactIdentity
		Values map[string]json.RawMessage `json:"values"`
	}
	const kind, descriptor = "scenery.bench-artifact", `{"values":"json_map"}`
	value := artifact{NewArtifactIdentity(kind, descriptor), map[string]json.RawMessage{}}
	for i := range 100 {
		value.Values[fmt.Sprintf("key_%d", i)] = json.RawMessage(`{"exact":9007199254740993,"tags":["one","two"]}`)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var decoded artifact
		if err := DecodeArtifact(encoded, &decoded, &decoded.ArtifactIdentity, kind, descriptor, "regenerate"); err != nil {
			b.Fatal(err)
		}
	}
}
