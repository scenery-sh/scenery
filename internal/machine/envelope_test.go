package machine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSpecRevision = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testProducer() Producer {
	return Producer{Version: "dev", Toolchain: Toolchain{GoVersion: "go1.26.0"}}
}

func TestEnvelopeRoundTripUsesOnlyCurrentIdentity(t *testing.T) {
	encoded, err := json.Marshal(NewEnvelope(testSpecRevision, testProducer(), true, map[string]any{"value": "ok"}, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Value string `json:"value"`
	}
	if err := DecodeData[string](encoded, testSpecRevision, &data); err != nil {
		t.Fatal(err)
	}
	if data.Value != "ok" {
		t.Fatalf("data = %#v", data)
	}
}

func TestSchemaRevisionsMatchCanonicalShapeDescriptors(t *testing.T) {
	for descriptor, want := range map[string]string{
		envelopeSchemaDescriptor:      EnvelopeSchemaRevision,
		eventEnvelopeSchemaDescriptor: EventEnvelopeSchemaRevision,
	} {
		digest := sha256.Sum256([]byte("scenery.machine.schema\x00" + descriptor))
		if got := fmt.Sprintf("sha256:%x", digest); got != want {
			t.Fatalf("schema revision = %q, want %q", got, want)
		}
	}
}

func TestSchemaFilesPublishCurrentMachineIdentity(t *testing.T) {
	for path, identity := range map[string]struct {
		kind     string
		revision string
	}{
		"scenery.cli.schema.json":       {EnvelopeKind, EnvelopeSchemaRevision},
		"scenery.cli.event.schema.json": {EventEnvelopeKind, EventEnvelopeSchemaRevision},
	} {
		encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", path))
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		var kind, revision struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(schema.Properties["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(schema.Properties["schema_revision"], &revision); err != nil {
			t.Fatal(err)
		}
		if kind.Const != identity.kind || revision.Const != identity.revision {
			t.Fatalf("%s identity = %q %q", path, kind.Const, revision.Const)
		}
	}
}

func TestEnvelopeDecoderRejectsOldUnknownAndWrongSchemas(t *testing.T) {
	current, err := json.Marshal(NewEnvelope(testSpecRevision, testProducer(), true, nil, []string{}))
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string][]byte{
		"old":     []byte(`{"api_version":"scenery.cli.v1","ok":true}`),
		"unknown": []byte(strings.Replace(string(current), `"ok":true`, `"ok":true,"extra":true`, 1)),
		"schema":  []byte(strings.Replace(string(current), EnvelopeSchemaRevision, "sha256:"+strings.Repeat("0", 64), 1)),
		"spec":    []byte(strings.Replace(string(current), testSpecRevision, "sha256:"+strings.Repeat("0", 64), 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode[string](encoded, testSpecRevision); err == nil {
				t.Fatal("Decode() accepted non-current envelope")
			}
		})
	}
}

func TestEventEnvelopeRequiresCurrentEventShape(t *testing.T) {
	event := NewEventEnvelope(testSpecRevision, testProducer(), 1, "summary", true, map[string]any{"event_count": 0}, []string{})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeEvent[string](encoded, testSpecRevision); err != nil {
		t.Fatal(err)
	}
	event.Terminal = false
	encoded, _ = json.Marshal(event)
	if _, err := DecodeEvent[string](encoded, testSpecRevision); err == nil {
		t.Fatal("DecodeEvent() accepted a non-terminal summary")
	}
}
