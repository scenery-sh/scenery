package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestDecodeManifestAcceptsOnlyExactCurrentManifestOrEnvelope(t *testing.T) {
	manifest := validTestManifest(t, nil)
	raw, _ := json.Marshal(manifest)
	if got, err := DecodeManifest(raw); err != nil || got.ContractRevision != manifest.ContractRevision {
		t.Fatalf("raw manifest: got=%#v err=%v", got, err)
	}
	envelope := machine.NewEnvelope[Diagnostic](string(spec.CurrentRevision()), manifest.Producer, true, map[string]any{
		"contract_status": "valid", "implementation_status": "not_requested", "view": "expanded", "manifest": manifest,
	}, []Diagnostic{})
	wrapped, _ := json.Marshal(envelope)
	if got, err := DecodeManifest(wrapped); err != nil || got.ContractRevision != manifest.ContractRevision {
		t.Fatalf("compile envelope: got=%#v err=%v", got, err)
	}

	oldEnvelope := []byte(`{"api_version":"` + "scenery.cli." + `v1","data":{"manifest":{"resources":[]}}}`)
	for name, encoded := range map[string][]byte{
		"missing nested identity": []byte(`{"data":{"manifest":{"resources":[]}}}`),
		"old envelope":            oldEnvelope,
		"unknown raw field":       []byte(strings.Replace(string(raw), `"diagnostics":[]`, `"diagnostics":[],"extra":true`, 1)),
		"trailing value":          append(append([]byte(nil), raw...), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeManifest(encoded); err == nil {
				t.Fatal("DecodeManifest() accepted invalid input")
			}
		})
	}
}

func TestValidateManifestRecomputesIdentityAndRequiresCanonicalResources(t *testing.T) {
	resources := []Resource{
		{Address: "app/pipeline/a", Kind: "scenery.pipeline", Name: "a", Module: "app", Spec: map[string]any{}, Origin: Origin{Kind: "authored"}},
		{Address: "app/pipeline/b", Kind: "scenery.pipeline", Name: "b", Module: "app", Spec: map[string]any{}, Origin: Origin{Kind: "authored"}},
	}
	manifest := validTestManifest(t, resources)
	manifest.ContractRevision = "sha256:" + strings.Repeat("0", 64)
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered revision error = %v", err)
	}
	manifest = validTestManifest(t, []Resource{resources[1], resources[0]})
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "canonically ordered") {
		t.Fatalf("unordered resources error = %v", err)
	}
	manifest = validTestManifest(t, []Resource{{Address: "app/unknown/a", Kind: "scenery.unknown", Name: "a", Module: "app", Spec: map[string]any{}, Origin: Origin{Kind: "authored"}}})
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "SCN1008") {
		t.Fatalf("unknown resource error = %v", err)
	}
}

func validTestManifest(t *testing.T, resources []Resource) *Manifest {
	t.Helper()
	if resources == nil {
		resources = []Resource{}
	}
	revision, err := ContractRevision(resources, "app")
	if err != nil {
		t.Fatal(err)
	}
	return &Manifest{
		Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, SpecRevision: string(spec.CurrentRevision()),
		Producer:          machine.Producer{Version: "dev", Toolchain: machine.Toolchain{GoVersion: "go1.26.0"}},
		DiagnosticCatalog: DiagnosticCatalog, Application: ApplicationIdentity{Name: "app"}, ContractRevision: revision,
		Resources: resources, SourceMap: map[string]SourceRecord{}, Diagnostics: []Diagnostic{},
	}
}
