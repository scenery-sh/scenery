package appconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"

	"scenery.sh/internal/machine"
)

// DocumentKind is the authoritative environment configuration document.
const DocumentKind = "scenery.app-environment"

// MaxDocumentBytes bounds one environment document or history entry.
const MaxDocumentBytes = 1 << 20

// documentSchemaDescriptor is the complete structural shape of Document. A
// shape change changes its revision; retained documents then need an explicit
// state upgrade, never a second decoder.
var documentSchemaDescriptor = map[string]any{
	"kind": "string", "schema_revision": "digest", "app_id": "string", "environment": "string",
	"revision": "string", "values": map[string]any{"*": "json"},
	"secrets": map[string]any{"*": map[string]any{"backend": "string", "version": "string"}},
}

// DocumentSchemaRevision identifies the current document shape.
var DocumentSchemaRevision = machine.ArtifactSchemaRevision(documentSchemaDescriptor)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
var revisionPattern = regexp.MustCompile(`^cfg-[0-9a-f]{32}$`)
var versionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// SecretVersion references one immutable secret version held by a backend.
// It carries no secret material and no digest of it.
type SecretVersion struct {
	Backend string `json:"backend"`
	Version string `json:"version"`
}

// Document is one environment's configured values. Values hold canonical
// contract wire JSON; secrets hold only version references.
type Document struct {
	Kind           string                     `json:"kind"`
	SchemaRevision string                     `json:"schema_revision"`
	AppID          string                     `json:"app_id"`
	Environment    string                     `json:"environment"`
	Revision       string                     `json:"revision"`
	Values         map[string]json.RawMessage `json:"values"`
	Secrets        map[string]SecretVersion   `json:"secrets"`
}

// NewDocument returns the empty document of an environment.
func NewDocument(appID, environment string) Document {
	document := Document{Kind: DocumentKind, SchemaRevision: DocumentSchemaRevision, AppID: appID, Environment: environment, Values: map[string]json.RawMessage{}, Secrets: map[string]SecretVersion{}}
	document.Revision = document.contentRevision()
	return document
}

// ValidIdentifier reports whether a name is a path-safe application ID or
// environment name.
func ValidIdentifier(name string) bool {
	return identifierPattern.MatchString(name)
}

// ValidRevision reports whether text has the opaque revision syntax.
func ValidRevision(text string) bool {
	return revisionPattern.MatchString(text)
}

// contentRevision is the opaque, content-addressed revision of the document's
// configured state. Equal content always has the same revision.
func (d Document) contentRevision() string {
	keys := make([]string, 0, len(d.Values))
	for key := range d.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = fmt.Fprintf(hash, "%d:%s", len(part), part)
		}
	}
	write(DocumentKind, d.AppID, d.Environment)
	for _, key := range keys {
		write("value", key, string(d.Values[key]))
	}
	keys = keys[:0]
	for key := range d.Secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		write("secret", key, d.Secrets[key].Backend, d.Secrets[key].Version)
	}
	sum := hash.Sum(nil)
	return "cfg-" + hex.EncodeToString(sum[:16])
}

func (d Document) clone() Document {
	clone := d
	clone.Values = make(map[string]json.RawMessage, len(d.Values))
	for key, value := range d.Values {
		clone.Values[key] = append(json.RawMessage(nil), value...)
	}
	clone.Secrets = make(map[string]SecretVersion, len(d.Secrets))
	for key, value := range d.Secrets {
		clone.Secrets[key] = value
	}
	return clone
}

// encode renders the deterministic document encoding.
func (d Document) encode() ([]byte, error) {
	encoded, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// DecodeDocument strictly decodes one current document and verifies that it
// belongs to the expected application and environment and that its revision
// matches its content.
func DecodeDocument(encoded []byte, appID, environment string) (Document, error) {
	if len(encoded) > MaxDocumentBytes {
		return Document{}, fmt.Errorf("environment document exceeds %d bytes", MaxDocumentBytes)
	}
	if err := rejectDuplicateKeys(encoded); err != nil {
		return Document{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("decode environment document: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Document{}, fmt.Errorf("environment document has trailing content")
	}
	if document.Kind != DocumentKind || document.SchemaRevision != DocumentSchemaRevision {
		return Document{}, fmt.Errorf("environment document has kind %q schema %q; this Scenery reads %s %s", document.Kind, document.SchemaRevision, DocumentKind, DocumentSchemaRevision)
	}
	if document.AppID != appID || document.Environment != environment {
		return Document{}, fmt.Errorf("environment document belongs to %s/%s, not %s/%s", document.AppID, document.Environment, appID, environment)
	}
	if document.Values == nil || document.Secrets == nil {
		return Document{}, fmt.Errorf("environment document is missing values or secrets")
	}
	for key, value := range document.Values {
		if !keyPattern.MatchString(key) || len(value) == 0 || len(value) > MaxValueBytes || !json.Valid(value) {
			return Document{}, fmt.Errorf("environment document has an invalid entry")
		}
	}
	for key, secret := range document.Secrets {
		if !keyPattern.MatchString(key) || !identifierPattern.MatchString(secret.Backend) || !versionPattern.MatchString(secret.Version) {
			return Document{}, fmt.Errorf("environment document has an invalid secret reference")
		}
		if _, both := document.Values[key]; both {
			return Document{}, fmt.Errorf("environment document stores a key as both value and secret")
		}
	}
	if document.Revision != document.contentRevision() {
		return Document{}, fmt.Errorf("environment document revision does not match its content")
	}
	return document, nil
}

// rejectDuplicateKeys fails for any JSON object with a repeated member name,
// which encoding/json would otherwise silently resolve to the last one.
func rejectDuplicateKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, _ := keyToken.(string)
				if seen[key] {
					return fmt.Errorf("environment document repeats member %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(); err != nil {
		return fmt.Errorf("decode environment document: %w", err)
	}
	return nil
}
