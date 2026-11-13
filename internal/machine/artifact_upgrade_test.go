package machine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

type upgradeFixture struct {
	ArtifactIdentity
	Credential string            `json:"credential"`
	Metadata   map[string]string `json:"metadata"`
}

const upgradeFixtureKind = "scenery.test.retained"
const upgradeFixtureDescriptor = `{"identity":"artifact","credential":"string","metadata":"map<string,string>"}`

func TestArtifactSpecUpgradePreservesPayload(t *testing.T) {
	before := upgradeFixture{ArtifactIdentity: NewArtifactIdentity(upgradeFixtureKind, upgradeFixtureDescriptor), Credential: "fixture-secret", Metadata: map[string]string{"key": "value"}}
	before.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(encoded)
	var decoded upgradeFixture
	if err := DecodeArtifact(encoded, &decoded, &decoded.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor, "upgrade"); err == nil {
		t.Fatal("ordinary decoder accepted the old specification")
	}
	after, err := PrepareArtifactSpecUpgrade(encoded, &decoded, &decoded.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, original) || !ArtifactPayloadEqual(encoded, after) || decoded.Credential != before.Credential || decoded.Metadata["key"] != "value" || decoded.SpecRevision != string(spec.CurrentRevision()) {
		t.Fatal("upgrade changed source bytes or payload")
	}
	again, err := PrepareArtifactSpecUpgrade(after, &decoded, &decoded.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor)
	if err != nil || !bytes.Equal(after, again) {
		t.Fatalf("current artifact is not an exact no-op: %v", err)
	}
}

func TestArtifactSpecUpgradeRejectsUnsupportedState(t *testing.T) {
	fixture := upgradeFixture{ArtifactIdentity: NewArtifactIdentity(upgradeFixtureKind, upgradeFixtureDescriptor), Credential: "fixture-secret"}
	fixture.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	data, _ := json.Marshal(fixture)
	for name, encoded := range map[string][]byte{
		"schema":    []byte(strings.Replace(string(data), fixture.SchemaRevision, "sha256:"+strings.Repeat("c", 64), 1)),
		"spec":      []byte(strings.Replace(string(data), fixture.SpecRevision, "not-a-revision", 1)),
		"producer":  []byte(strings.Replace(string(data), `"version":"`+fixture.Producer.Version+`"`, `"version":""`, 1)),
		"unknown":   append(bytes.Clone(data[:len(data)-1]), []byte(`,"extra":true}`)...),
		"duplicate": append(bytes.Clone(data[:len(data)-1]), []byte(`,"credential":"different"}`)...),
		"trailing":  append(bytes.Clone(data), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			var target upgradeFixture
			if _, err := PrepareArtifactSpecUpgrade(encoded, &target, &target.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor); err == nil {
				t.Fatal("unsupported state was accepted")
			}
		})
	}
	changed := bytes.ReplaceAll(data, []byte("fixture-secret"), []byte("different"))
	if ArtifactPayloadEqual(data, changed) {
		t.Fatal("payload comparison ignored credentials")
	}
}

func TestSchemaBoundArtifactAcceptsAnotherSpecificationOnly(t *testing.T) {
	fixture := upgradeFixture{ArtifactIdentity: NewArtifactIdentity(upgradeFixtureKind, upgradeFixtureDescriptor), Credential: "fixture-secret"}
	fixture.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	data, _ := json.Marshal(fixture)
	var decoded upgradeFixture
	if err := DecodeSchemaBoundArtifact(data, &decoded, &decoded.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor, "inspect"); err != nil {
		t.Fatal(err)
	}
	if decoded.Credential != fixture.Credential || decoded.SpecRevision != fixture.SpecRevision {
		t.Fatal("decoding changed the stored record")
	}
	for name, encoded := range map[string][]byte{
		"kind":     []byte(strings.Replace(string(data), upgradeFixtureKind, "scenery.test.other", 1)),
		"schema":   []byte(strings.Replace(string(data), fixture.SchemaRevision, "sha256:"+strings.Repeat("c", 64), 1)),
		"spec":     []byte(strings.Replace(string(data), fixture.SpecRevision, "not-a-revision", 1)),
		"producer": []byte(strings.Replace(string(data), `"version":"`+fixture.Producer.Version+`"`, `"version":""`, 1)),
		"unknown":  append(bytes.Clone(data[:len(data)-1]), []byte(`,"extra":true}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			var target upgradeFixture
			if err := DecodeSchemaBoundArtifact(encoded, &target, &target.ArtifactIdentity, upgradeFixtureKind, upgradeFixtureDescriptor, "inspect"); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}
