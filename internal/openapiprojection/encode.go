package openapiprojection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// EncodeJSON encodes an OpenAPI document as deterministic 2-space indented JSON
// with a trailing newline.
func EncodeJSON(doc map[string]any) ([]byte, error) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode openapi json: %w", err)
	}
	return append(encoded, '\n'), nil
}

// EncodeYAML encodes an OpenAPI document as deterministic 2-space indented YAML
// with a trailing newline.
func EncodeYAML(doc map[string]any) ([]byte, error) {
	// Normalize through JSON first so Go-specific integer/slice types become
	// canonical JSON-compatible values with deterministic key order.
	rawJSON, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("normalize openapi for yaml: %w", err)
	}
	var normalized any
	dec := json.NewDecoder(bytes.NewReader(rawJSON))
	dec.UseNumber()
	if err := dec.Decode(&normalized); err != nil {
		return nil, fmt.Errorf("decode normalized openapi for yaml: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(convertJSONNumbers(normalized)); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encode openapi yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close openapi yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// ContentDigest returns the canonical sha256 content digest for encoded bytes.
func ContentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func convertJSONNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = convertJSONNumbers(item)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = convertJSONNumbers(item)
		}
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
		return typed.String()
	default:
		return value
	}
}
