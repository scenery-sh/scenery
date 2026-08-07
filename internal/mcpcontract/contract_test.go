package mcpcontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

func TestManifestSchemaRevisionMatchesDescriptor(t *testing.T) {
	if got := string(spec.SchemaRevision(manifestSchemaDescriptor)); got != ManifestSchemaRevision {
		t.Fatalf("manifest schema revision = %q, want %q", got, ManifestSchemaRevision)
	}
}

func TestManifestCanonicalRoundTripAndDigest(t *testing.T) {
	manifest := validManifest()
	first, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCanonical(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical manifest changed:\n%s\n%s", first, second)
	}
	digest, err := Digest(manifest)
	if err != nil || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		t.Fatalf("digest = %q, %v", digest, err)
	}
}

func TestManifestRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"protocol", func(m *Manifest) { m.ProtocolVersion = "older" }},
		{"tool name", func(m *Manifest) { m.Capabilities[0].Name = "Not Portable" }},
		{"duplicate", func(m *Manifest) { m.Capabilities = append(m.Capabilities, m.Capabilities[0]) }},
		{"input schema", func(m *Manifest) { m.Capabilities[0].InputSchema = json.RawMessage(`{"type":"string"}`) }},
		{"limits", func(m *Manifest) { m.Capabilities[0].Limits.MaxInputBytes = MaximumInputBytes + 1 }},
		{"effects", func(m *Manifest) {
			m.Capabilities[0].Effect.ReadOnly, m.Capabilities[0].Effect.Destructive = true, true
		}},
		{"filters", func(m *Manifest) { m.Connections[0].Block = []string{"other"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOutcomeEnvelopeRequiresOnePayload(t *testing.T) {
	value := ToolOutcome{Outcome: "processed", Value: json.RawMessage(`{"status":"ok"}`)}
	encoded, err := MarshalOutcome(value)
	if err != nil || string(encoded) != `{"outcome":"processed","value":{"status":"ok"}}` {
		t.Fatalf("value envelope = %s, %v", encoded, err)
	}
	value.Problem = json.RawMessage(`{"code":"bad","message":"bad input"}`)
	if _, err := MarshalOutcome(value); err == nil {
		t.Fatal("expected multiple payload rejection")
	}
}

func validManifest() Manifest {
	return Manifest{
		Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, ProtocolVersion: ProtocolVersion,
		SourceRevision: "sha256:source", ContractRevision: "sha256:contract",
		Capabilities: []Capability{{
			ID: "house/binding/process", Name: "process", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			OperationAddress: "house/operation/process", ExecutionAddress: "house/execution/process", Origin: Origin{Kind: "local", Address: "house/binding/process"},
			Auth: Auth{Authentication: "std.authentication.inherit", Authorization: "std.authorization.public"}, Limits: Limits{MaxInputBytes: 1024, MaxResultBytes: 2048}, Approval: ApprovalAlways,
		}},
		Connections: []Connection{{Address: "app/mcp_connection/docs", Namespace: "docs", Allow: []string{"fetch", "search"}}},
	}
}
