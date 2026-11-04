package machine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestArtifactIdentityRequiresExactCurrentShape(t *testing.T) {
	const (
		kind       = "scenery.test-artifact"
		descriptor = `{"kind":"scenery.test-artifact","value":"string"}`
	)
	identity := NewArtifactIdentity(kind, descriptor)
	if err := ValidateArtifactIdentity(identity, kind, descriptor, "regenerate"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ArtifactIdentity){
		"legacy shape": func(value *ArtifactIdentity) { *value = ArtifactIdentity{} },
		"schema":       func(value *ArtifactIdentity) { value.SchemaRevision = "sha256:" + strings.Repeat("0", 64) },
		"spec":         func(value *ArtifactIdentity) { value.SpecRevision = "sha256:" + strings.Repeat("0", 64) },
		"producer":     func(value *ArtifactIdentity) { value.Producer = Producer{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := identity
			mutate(&candidate)
			if err := ValidateArtifactIdentity(candidate, kind, descriptor, "regenerate"); err == nil {
				t.Fatal("stale artifact identity was accepted")
			}
		})
	}
}

func TestExactSchemaRevisionIsUsedDirectlyAndValidated(t *testing.T) {
	revision := ExactSchemaRevision("sha256:" + strings.Repeat("a", 64))
	identity := NewArtifactIdentity("scenery.test-artifact", revision)
	if identity.SchemaRevision != string(revision) {
		t.Fatalf("schema revision = %q", identity.SchemaRevision)
	}
	if err := ValidateArtifactIdentity(identity, identity.Kind, revision, "regenerate"); err != nil {
		t.Fatal(err)
	}
	invalid := identity
	invalid.SchemaRevision = "not-a-digest"
	if err := ValidateArtifactIdentity(invalid, identity.Kind, ExactSchemaRevision("not-a-digest"), "regenerate"); err == nil {
		t.Fatal("invalid exact schema revision was accepted by validation")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("invalid exact schema revision was accepted by the producer")
		}
	}()
	_ = NewArtifactIdentity(identity.Kind, ExactSchemaRevision("not-a-digest"))
}

func TestDecodeArtifactRejectsUnknownAndTrailingJSON(t *testing.T) {
	type artifact struct {
		ArtifactIdentity
		Value string `json:"value"`
	}
	const (
		kind       = "scenery.test-artifact"
		descriptor = `{"kind":"scenery.test-artifact","value":"string"}`
	)
	current, err := json.Marshal(artifact{ArtifactIdentity: NewArtifactIdentity(kind, descriptor), Value: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string][]byte{
		"unknown":          []byte(strings.Replace(string(current), `"value":"ok"`, `"value":"ok","extra":true`, 1)),
		"trailing":         append(append([]byte(nil), current...), []byte(` {}`)...),
		"duplicate":        []byte(strings.Replace(string(current), `"value":"ok"`, `"value":"ok","\u0076alue":"different"`, 1)),
		"nested duplicate": []byte(strings.Replace(string(current), `"go_version":`, `"go_version":"other","go_version":`, 1)),
		"case alias":       []byte(strings.Replace(string(current), `"value":`, `"Value":`, 1)),
		"invalid Unicode":  []byte(strings.Replace(string(current), `"value":"ok"`, `"value":"\ud800"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			var candidate artifact
			if err := DecodeArtifact(encoded, &candidate, &candidate.ArtifactIdentity, kind, descriptor, "regenerate"); err == nil {
				t.Fatal("DecodeArtifact accepted non-current JSON")
			}
		})
	}
}

func TestDecodeArtifactPreservesDurationAndExactJSON(t *testing.T) {
	type artifact struct {
		ArtifactIdentity
		Duration time.Duration   `json:"duration"`
		Value    json.RawMessage `json:"value"`
		Bytes    [2]byte         `json:"bytes"`
	}
	const kind = "scenery.test-artifact"
	const descriptor = `{"duration":"nanoseconds","value":"json"}`
	value := artifact{NewArtifactIdentity(kind, descriptor), 123456789 * time.Nanosecond, json.RawMessage(`9007199254740993.123456789`), [2]byte{1, 2}}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded artifact
	if err := DecodeArtifact(encoded, &decoded, &decoded.ArtifactIdentity, kind, descriptor, "regenerate"); err != nil {
		t.Fatal(err)
	}
	if decoded.Duration != value.Duration || string(decoded.Value) != string(value.Value) || decoded.Bytes != value.Bytes {
		t.Fatalf("decoded = %+v, value = %s", decoded, decoded.Value)
	}
}
