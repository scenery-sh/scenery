package mcpcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"scenery.sh/internal/spec"
)

var toolNameRE = regexp.MustCompile(ToolNamePattern)

func (manifest Manifest) Validate() error {
	if manifest.Kind != ManifestKind {
		return fmt.Errorf("mcp manifest kind %q is unsupported", manifest.Kind)
	}
	if manifest.SchemaRevision != ManifestSchemaRevision {
		return fmt.Errorf("mcp manifest schema revision %q is unsupported", manifest.SchemaRevision)
	}
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("MCP protocol version %q is unsupported", manifest.ProtocolVersion)
	}
	if strings.TrimSpace(manifest.ContractRevision) == "" {
		return errors.New("mcp manifest contract revision is required")
	}
	capabilityIDs := map[string]bool{}
	toolNames := map[string]bool{}
	previousName := ""
	for index, capability := range manifest.Capabilities {
		if err := capability.validate(); err != nil {
			return fmt.Errorf("capability %d: %w", index, err)
		}
		if capabilityIDs[capability.ID] {
			return fmt.Errorf("duplicate MCP capability id %q", capability.ID)
		}
		if toolNames[capability.Name] {
			return fmt.Errorf("duplicate MCP tool name %q", capability.Name)
		}
		if previousName != "" && capability.Name < previousName {
			return errors.New("MCP capabilities are not sorted by name")
		}
		capabilityIDs[capability.ID], toolNames[capability.Name] = true, true
		previousName = capability.Name
	}
	namespaces := map[string]bool{}
	previousNamespace := ""
	for index, connection := range manifest.Connections {
		if err := connection.validate(); err != nil {
			return fmt.Errorf("connection %d: %w", index, err)
		}
		if namespaces[connection.Namespace] {
			return fmt.Errorf("duplicate MCP connection namespace %q", connection.Namespace)
		}
		if previousNamespace != "" && connection.Namespace < previousNamespace {
			return errors.New("MCP connections are not sorted by namespace")
		}
		namespaces[connection.Namespace] = true
		previousNamespace = connection.Namespace
	}
	return nil
}

func (capability Capability) validate() error {
	if strings.TrimSpace(capability.ID) == "" || strings.TrimSpace(capability.OperationAddress) == "" || strings.TrimSpace(capability.ExecutionAddress) == "" {
		return errors.New("id, operation_address, and execution_address are required")
	}
	if !toolNameRE.MatchString(capability.Name) {
		return fmt.Errorf("tool name %q does not match %s", capability.Name, ToolNamePattern)
	}
	if err := validateJSONObjectSchema(capability.InputSchema, true); err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if err := validateJSONObjectSchema(capability.OutputSchema, false); err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	if capability.Origin.Kind != "local" && capability.Origin.Kind != "federated" {
		return fmt.Errorf("origin kind %q is unsupported", capability.Origin.Kind)
	}
	if strings.TrimSpace(capability.Origin.Address) == "" {
		return errors.New("origin address is required")
	}
	if capability.Limits.MaxInputBytes <= 0 || capability.Limits.MaxInputBytes > MaximumInputBytes || capability.Limits.MaxResultBytes <= 0 || capability.Limits.MaxResultBytes > MaximumResultBytes {
		return errors.New("capability limits must be positive and bounded")
	}
	if capability.Effect.ReadOnly && capability.Effect.Destructive {
		return errors.New("capability cannot be both read-only and destructive")
	}
	if capability.Approval != ApprovalNever && capability.Approval != ApprovalAlways {
		return fmt.Errorf("approval %q is unsupported", capability.Approval)
	}
	return nil
}

func (connection Connection) validate() error {
	if strings.TrimSpace(connection.Address) == "" || !toolNameRE.MatchString(connection.Namespace) {
		return errors.New("connection address and portable namespace are required")
	}
	if len(connection.Allow) > 0 && len(connection.Block) > 0 {
		return errors.New("connection cannot specify both allow and block")
	}
	for _, values := range [][]string{connection.Allow, connection.Block} {
		if !sort.StringsAreSorted(values) {
			return errors.New("connection filters must be sorted")
		}
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || seen[value] {
				return errors.New("connection filters must contain unique non-empty names")
			}
			seen[value] = true
		}
	}
	return nil
}

func validateJSONObjectSchema(raw json.RawMessage, requireObject bool) error {
	if len(raw) == 0 || !json.Valid(raw) {
		return errors.New("valid JSON is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil || schema == nil {
		return errors.New("schema must be a JSON object")
	}
	if requireObject && schema["type"] != "object" {
		return errors.New("input schema root type must be object")
	}
	return nil
}

func MarshalCanonical(manifest Manifest) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return spec.MarshalCanonical(manifest)
}

func Parse(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("mcp manifest must contain one JSON value")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Digest(manifest Manifest) (string, error) {
	encoded, err := MarshalCanonical(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(ManifestKind+"\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func MarshalOutcome(outcome ToolOutcome) ([]byte, error) {
	if strings.TrimSpace(outcome.Outcome) == "" {
		return nil, errors.New("tool outcome name is required")
	}
	payloads := 0
	if len(outcome.Value) > 0 {
		if !json.Valid(outcome.Value) {
			return nil, errors.New("tool outcome value must be valid JSON")
		}
		payloads++
	}
	if len(outcome.Problem) > 0 {
		if !json.Valid(outcome.Problem) {
			return nil, errors.New("tool outcome problem must be valid JSON")
		}
		payloads++
	}
	if outcome.Receipt != nil {
		if outcome.Receipt.ExecutionID == "" || outcome.Receipt.DurableIdentity == "" || outcome.Receipt.AcceptedRevision == "" {
			return nil, errors.New("durable receipt identity, execution id, and revision are required")
		}
		payloads++
	}
	if payloads != 1 {
		return nil, errors.New("tool outcome must contain exactly one value, problem, or receipt")
	}
	return spec.MarshalCanonical(outcome)
}
