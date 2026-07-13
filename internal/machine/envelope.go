// Package machine owns Scenery's current cross-process CLI envelope shapes.
package machine

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	EnvelopeKind      = "scenery.cli"
	EventEnvelopeKind = "scenery.cli.event"

	// These revisions identify the concrete shapes described by the matching
	// schemas under docs/schemas. They do not select an older decoder.
	EnvelopeSchemaRevision      = "sha256:63e0e06289654ca0ab355a28890148f4d5bf7d905c3a857f2d6d2ef07f753bb6"
	EventEnvelopeSchemaRevision = "sha256:8138ed6b8d979ade5ae1c826a0c2615f36384180ef841d2be1e2c7357f38d46d"
)

type Toolchain struct {
	GoVersion        string `json:"go_version"`
	ManifestRevision string `json:"manifest_revision,omitempty"`
}

type Producer struct {
	Version   string    `json:"version"`
	Commit    string    `json:"commit,omitempty"`
	BuiltAt   string    `json:"built_at,omitempty"`
	Toolchain Toolchain `json:"toolchain"`
}

type Envelope[D any] struct {
	Kind                   string   `json:"kind"`
	SchemaRevision         string   `json:"schema_revision"`
	SpecRevision           string   `json:"spec_revision"`
	Producer               Producer `json:"producer"`
	OK                     bool     `json:"ok"`
	WorkspaceRevision      any      `json:"workspace_revision"`
	ContractRevision       any      `json:"contract_revision"`
	ImplementationRevision any      `json:"implementation_revision"`
	DeploymentRevision     any      `json:"deployment_revision"`
	Data                   any      `json:"data"`
	Diagnostics            []D      `json:"diagnostics"`
}

type EventEnvelope[D any] struct {
	Kind                   string   `json:"kind"`
	SchemaRevision         string   `json:"schema_revision"`
	SpecRevision           string   `json:"spec_revision"`
	Producer               Producer `json:"producer"`
	Sequence               uint64   `json:"sequence"`
	Event                  string   `json:"event"`
	Terminal               bool     `json:"terminal"`
	WorkspaceRevision      any      `json:"workspace_revision"`
	ContractRevision       any      `json:"contract_revision"`
	ImplementationRevision any      `json:"implementation_revision"`
	DeploymentRevision     any      `json:"deployment_revision"`
	Data                   any      `json:"data"`
	Diagnostics            []D      `json:"diagnostics"`
}

func NewEnvelope[D any](specRevision string, producer Producer, ok bool, data any, diagnostics []D) Envelope[D] {
	return Envelope[D]{
		Kind: EnvelopeKind, SchemaRevision: EnvelopeSchemaRevision, SpecRevision: specRevision, Producer: producer,
		OK: ok, Data: data, Diagnostics: nonNil(diagnostics),
	}
}

func NewEventEnvelope[D any](specRevision string, producer Producer, sequence uint64, event string, terminal bool, data any, diagnostics []D) EventEnvelope[D] {
	return EventEnvelope[D]{
		Kind: EventEnvelopeKind, SchemaRevision: EventEnvelopeSchemaRevision, SpecRevision: specRevision, Producer: producer,
		Sequence: sequence, Event: event, Terminal: terminal, Data: data, Diagnostics: nonNil(diagnostics),
	}
}

func Decode[D any](encoded []byte, specRevision string) (Envelope[D], error) {
	var envelope Envelope[D]
	if err := requireFields(encoded, "kind", "schema_revision", "spec_revision", "producer", "ok", "workspace_revision", "contract_revision", "implementation_revision", "deployment_revision", "data", "diagnostics"); err != nil {
		return Envelope[D]{}, err
	}
	if err := decodeExact(encoded, &envelope); err != nil {
		return Envelope[D]{}, err
	}
	if err := validateEnvelope(envelope.Kind, envelope.SchemaRevision, envelope.SpecRevision, specRevision, envelope.Producer); err != nil {
		return Envelope[D]{}, err
	}
	if err := validateRevisionFields(envelope.WorkspaceRevision, envelope.ContractRevision, envelope.ImplementationRevision, envelope.DeploymentRevision); err != nil {
		return Envelope[D]{}, err
	}
	return envelope, nil
}

func DecodeData[D any](encoded []byte, specRevision string, target any) error {
	if _, err := Decode[D](encoded, specRevision); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return err
	}
	return json.Unmarshal(object["data"], target)
}

func DecodeEvent[D any](encoded []byte, specRevision string) (EventEnvelope[D], error) {
	var envelope EventEnvelope[D]
	if err := requireFields(encoded, "kind", "schema_revision", "spec_revision", "producer", "sequence", "event", "terminal", "workspace_revision", "contract_revision", "implementation_revision", "deployment_revision", "data", "diagnostics"); err != nil {
		return EventEnvelope[D]{}, err
	}
	if err := decodeExact(encoded, &envelope); err != nil {
		return EventEnvelope[D]{}, err
	}
	if err := validateEnvelope(envelope.Kind, envelope.SchemaRevision, envelope.SpecRevision, specRevision, envelope.Producer); err != nil {
		return EventEnvelope[D]{}, err
	}
	if err := validateRevisionFields(envelope.WorkspaceRevision, envelope.ContractRevision, envelope.ImplementationRevision, envelope.DeploymentRevision); err != nil {
		return EventEnvelope[D]{}, err
	}
	if envelope.Kind != EventEnvelopeKind || envelope.SchemaRevision != EventEnvelopeSchemaRevision {
		return EventEnvelope[D]{}, fmt.Errorf("unexpected CLI event envelope identity")
	}
	if envelope.Sequence == 0 || envelope.Event != "event" && envelope.Event != "summary" || envelope.Terminal != (envelope.Event == "summary") {
		return EventEnvelope[D]{}, fmt.Errorf("invalid CLI event envelope")
	}
	return envelope, nil
}

func validateRevisionFields(workspace, contract, implementation, deployment any) error {
	if err := validateSingularRevision("workspace_revision", workspace); err != nil {
		return err
	}
	if err := validateSingularRevision("contract_revision", contract); err != nil {
		return err
	}
	if err := validateRevisionValue("implementation_revision", implementation); err != nil {
		return err
	}
	return validateRevisionValue("deployment_revision", deployment)
}

func validateSingularRevision(field string, value any) error {
	if value == nil {
		return nil
	}
	revision, ok := value.(string)
	if !ok || !isDigest(revision) {
		return fmt.Errorf("invalid %s: expected a canonical digest or null", field)
	}
	return nil
}

func validateRevisionValue(field string, value any) error {
	if value == nil {
		return nil
	}
	if revision, ok := value.(string); ok {
		if isDigest(revision) {
			return nil
		}
		return fmt.Errorf("invalid %s: expected a canonical digest, digest map, or null", field)
	}
	byTarget, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid %s: expected a canonical digest, digest map, or null", field)
	}
	for target, value := range byTarget {
		revision, ok := value.(string)
		if !ok || !isDigest(revision) {
			return fmt.Errorf("invalid %s target %q: expected a canonical digest", field, target)
		}
	}
	return nil
}

func validateEnvelope(kind, schemaRevision, specRevision, currentSpecRevision string, producer Producer) error {
	wantSchema := EnvelopeSchemaRevision
	if kind == EventEnvelopeKind {
		wantSchema = EventEnvelopeSchemaRevision
	} else if kind != EnvelopeKind {
		return fmt.Errorf("unexpected machine kind %q", kind)
	}
	if schemaRevision != wantSchema {
		return fmt.Errorf("unexpected %s schema revision %q; regenerate with the current Scenery CLI", kind, schemaRevision)
	}
	if !isDigest(specRevision) {
		return fmt.Errorf("invalid %s spec revision %q", kind, specRevision)
	}
	if specRevision != currentSpecRevision {
		return fmt.Errorf("unexpected %s spec revision %q; regenerate with the current Scenery CLI", kind, specRevision)
	}
	if err := ValidateProducer(producer); err != nil {
		return fmt.Errorf("invalid %s producer identity: %w", kind, err)
	}
	return nil
}

func ValidateProducer(producer Producer) error {
	if strings.TrimSpace(producer.Version) == "" || strings.TrimSpace(producer.Toolchain.GoVersion) == "" {
		return fmt.Errorf("version and Go toolchain are required")
	}
	if producer.Toolchain.ManifestRevision != "" && !isDigest(producer.Toolchain.ManifestRevision) {
		return fmt.Errorf("toolchain manifest revision is not a canonical digest")
	}
	return nil
}

func decodeExact(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if err != io.EOF {
		return err
	}
	return nil
}

func requireFields(encoded []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return err
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("machine envelope is missing %q", field)
		}
	}
	return nil
}

func isDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func nonNil[D any](values []D) []D {
	if values == nil {
		return []D{}
	}
	return values
}
