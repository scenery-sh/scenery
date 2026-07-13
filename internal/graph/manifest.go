package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func DecodeManifest(encoded []byte) (*Manifest, error) {
	var identity struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(encoded, &identity); err != nil {
		return nil, fmt.Errorf("decode manifest identity: %w", err)
	}
	var manifest *Manifest
	switch identity.Kind {
	case ManifestKind:
		manifest = &Manifest{}
		if err := decodeManifestExact(encoded, manifest); err != nil {
			return nil, fmt.Errorf("decode manifest: %w", err)
		}
	case machine.EnvelopeKind:
		var data struct {
			Manifest *Manifest `json:"manifest"`
		}
		if err := machine.DecodeData[Diagnostic](encoded, string(spec.CurrentRevision()), &data); err != nil {
			return nil, fmt.Errorf("decode compile envelope: %w", err)
		}
		manifest = data.Manifest
	default:
		return nil, fmt.Errorf("unexpected manifest document kind %q", identity.Kind)
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func ValidateManifest(manifest *Manifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if manifest.Kind != ManifestKind || manifest.SchemaRevision != ManifestSchemaRevision {
		return fmt.Errorf("unexpected manifest identity %q %q", manifest.Kind, manifest.SchemaRevision)
	}
	if manifest.SpecRevision != string(spec.CurrentRevision()) {
		return fmt.Errorf("unexpected manifest specification revision %q", manifest.SpecRevision)
	}
	if manifest.DiagnosticCatalog != DiagnosticCatalog {
		return fmt.Errorf("unexpected manifest diagnostic catalog %q", manifest.DiagnosticCatalog)
	}
	if err := machine.ValidateProducer(manifest.Producer); err != nil {
		return fmt.Errorf("invalid manifest producer: %w", err)
	}
	if strings.TrimSpace(manifest.Application.Name) == "" || manifest.SourceMap == nil || manifest.Resources == nil || manifest.Diagnostics == nil {
		return fmt.Errorf("manifest application, resources, source map, and diagnostics are required")
	}
	previous := ""
	for _, resource := range manifest.Resources {
		if resource.Address == "" || resource.Address <= previous {
			return fmt.Errorf("manifest resource addresses must be unique and canonically ordered")
		}
		previous = resource.Address
		if resource.Spec == nil {
			return fmt.Errorf("manifest resource %s has no specification", resource.Address)
		}
		if violations := spec.ValidateResource(resource.Kind, resource.Spec); len(violations) > 0 {
			return fmt.Errorf("manifest resource %s is invalid: %s %s", resource.Address, violations[0].Code, violations[0].Message)
		}
	}
	want, err := ContractRevision(manifest.Resources, manifest.Application.Name)
	if err != nil {
		return err
	}
	if manifest.ContractRevision != want {
		return fmt.Errorf("manifest contract revision %q does not match canonical graph %q", manifest.ContractRevision, want)
	}
	return nil
}

func decodeManifestExact(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
