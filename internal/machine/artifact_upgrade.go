package machine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// PrepareArtifactSpecUpgrade prepares an explicit, same-schema identity change.
// It never writes state or relaxes ordinary decoding. Owners must additionally
// validate their payload invariants before publishing the returned bytes.
func PrepareArtifactSpecUpgrade(encoded []byte, target any, identity *ArtifactIdentity, kind string, descriptor any) ([]byte, error) {
	fields, err := artifactFields(encoded)
	if err != nil {
		return nil, err
	}
	var stored ArtifactIdentity
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, err
	}
	producerDecoder := json.NewDecoder(bytes.NewReader(fields["producer"]))
	producerDecoder.DisallowUnknownFields()
	if err := producerDecoder.Decode(&stored.Producer); err != nil {
		return nil, fmt.Errorf("invalid %s source producer: %w", kind, err)
	}
	if !isCanonicalDigest(stored.SpecRevision) {
		return nil, fmt.Errorf("invalid %s source specification revision", kind)
	}
	current := NewArtifactIdentity(kind, descriptor)
	checked := stored
	checked.SpecRevision = current.SpecRevision
	if err := ValidateArtifactIdentity(checked, kind, descriptor, "use a supported retained-state upgrade"); err != nil {
		return nil, err
	}
	if stored.SpecRevision == current.SpecRevision {
		if err := DecodeArtifact(encoded, target, identity, kind, descriptor, "inspect retained state"); err != nil {
			return nil, err
		}
		return bytes.Clone(encoded), nil
	}
	fields["spec_revision"], _ = json.Marshal(current.SpecRevision)
	fields["producer"], _ = json.Marshal(current.Producer)
	upgraded, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	upgraded = append(upgraded, '\n')
	if err := DecodeArtifact(upgraded, target, identity, kind, descriptor, "use a supported retained-state upgrade"); err != nil {
		return nil, err
	}
	return upgraded, nil
}

// ArtifactPayloadEqual ignores only specification/producer identity, never
// kind, schema or payload. Transaction recovery uses it before trusting backups.
func ArtifactPayloadEqual(before, after []byte) bool {
	left, err := artifactFields(before)
	if err != nil {
		return false
	}
	right, err := artifactFields(after)
	if err != nil {
		return false
	}
	for _, fields := range []map[string]json.RawMessage{left, right} {
		delete(fields, "spec_revision")
		delete(fields, "producer")
	}
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	return err == nil && bytes.Equal(a, b)
}

func artifactFields(encoded []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("retained artifact must be one JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid retained artifact field")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate retained artifact field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if last, err := decoder.Token(); err != nil || last != json.Delim('}') {
		return nil, fmt.Errorf("invalid retained artifact object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing retained artifact data")
	}
	return fields, nil
}
