package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeMCPFixtureIsDeterministicAndPreservesRevisionDomains(t *testing.T) {
	fixture := filepath.Join("testdata", "native")
	first, err := Compile(fixture)
	if err != nil || !first.Valid() {
		t.Fatalf("first native compile: err=%v diagnostics=%#v", err, first.Diagnostics)
	}
	second, err := Compile(fixture)
	if err != nil || !second.Valid() {
		t.Fatalf("second native compile: err=%v diagnostics=%#v", err, second.Diagnostics)
	}

	wantKinds := map[string]string{
		"app/mcp_connection/docs":         "scenery.mcp-connection",
		"app/mcp_server/support":          "scenery.mcp-server",
		"app/assistant/support":           "scenery.assistant",
		"house/binding/process_scene_mcp": "scenery.binding",
	}
	for _, view := range []string{"source", "effective", "expanded"} {
		firstManifest, err := first.ManifestForView(view)
		if err != nil {
			t.Fatalf("first %s view: %v", view, err)
		}
		secondManifest, err := second.ManifestForView(view)
		if err != nil {
			t.Fatalf("second %s view: %v", view, err)
		}
		firstJSON, err := MarshalCanonical(firstManifest)
		if err != nil {
			t.Fatalf("first %s canonical JSON: %v", view, err)
		}
		secondJSON, err := MarshalCanonical(secondManifest)
		if err != nil {
			t.Fatalf("second %s canonical JSON: %v", view, err)
		}
		if !bytes.Equal(firstJSON, secondJSON) {
			t.Fatalf("%s view is not deterministic", view)
		}

		gotKinds := map[string]string{}
		for _, resource := range firstManifest.Resources {
			if _, expected := wantKinds[resource.Address]; expected {
				gotKinds[resource.Address] = resource.Kind
			}
		}
		if len(gotKinds) != len(wantKinds) {
			t.Fatalf("%s MCP resource count = %d, want exactly %d: %#v", view, len(gotKinds), len(wantKinds), gotKinds)
		}
		for address, wantKind := range wantKinds {
			if gotKinds[address] != wantKind {
				t.Fatalf("%s %s kind = %q, want %q", view, address, gotKinds[address], wantKind)
			}
		}
	}

	effective, err := first.ManifestForView("effective")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := first.ManifestForView("expanded")
	if err != nil {
		t.Fatal(err)
	}
	server := resourcesByAddress(expanded)["app/mcp_server/support"]
	capabilities := namedChildren(server.Spec, "capability")
	if len(capabilities) != 1 || refString(capabilities[0]["binding"]) != "house/binding/process_scene_mcp" {
		t.Fatalf("expanded MCP capability binding = %#v", capabilities)
	}
	if capabilities[0]["label"] != "process_scene" || capabilities[0]["name"] != "house__process_scene" {
		t.Fatalf("expanded MCP capability identity = %#v", capabilities[0])
	}
	capabilityProvenance := server.Origin.FieldProvenance["/spec/capability/binding"]
	if capabilityProvenance.Kind != "module_export" || capabilityProvenance.Input != "module.house.process_scene_mcp" || capabilityProvenance.ProvidedBy != "app/module/house/export/process_scene_mcp" || !containsString(capabilityProvenance.Transformations, "module_export_substitution") {
		t.Fatalf("MCP capability provenance = %#v", capabilityProvenance)
	}
	connections := namedChildren(server.Spec, "connection")
	if len(connections) != 1 || refString(connections[0]["connection"]) != "mcp_connection.docs" {
		t.Fatalf("expanded MCP server connection = %#v", connections)
	}
	assistant := resourcesByAddress(expanded)["app/assistant/support"]
	if refString(assistant.Spec["mcp_server"]) != "mcp_server.support" {
		t.Fatalf("expanded assistant mcp_server = %#v", assistant.Spec["mcp_server"])
	}
	binding := resourcesByAddress(effective)["house/binding/process_scene_mcp"]
	mcp, ok := binding.Spec["mcp"].(map[string]any)
	if !ok || mcp["allow_sensitive_output"] != false {
		t.Fatalf("effective MCP default allow_sensitive_output = %#v", binding.Spec["mcp"])
	}
	defaultProvenance := binding.Origin.FieldProvenance["/spec/mcp/allow_sensitive_output"]
	if defaultProvenance.Kind != "default" || defaultProvenance.ProvidedBy != "spec" {
		t.Fatalf("MCP default provenance = %#v", defaultProvenance)
	}

	source, err := first.ManifestForView("source")
	if err != nil {
		t.Fatal(err)
	}
	sourceMCP, _ := resourcesByAddress(source)["house/binding/process_scene_mcp"].Spec["mcp"].(map[string]any)
	if _, authored := sourceMCP["allow_sensitive_output"]; authored {
		t.Fatalf("source MCP view unexpectedly contains default: %#v", sourceMCP)
	}

	implementationRoot := t.TempDir()
	copyTree(t, fixture, implementationRoot)
	copyTree(t, filepath.Join(implementationRoot, "assistants", "support"), filepath.Join(implementationRoot, "assistants", "support-alt"))
	appPath := filepath.Join(implementationRoot, appFilename)
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	appText := string(appSource)
	appText = strings.Replace(appText, `source       = "./assistants/support"`, `source       = "./assistants/support-alt"`, 1)
	appText = strings.Replace(appText, `package      = "./assistants/support/package.json"`, `package      = "./assistants/support-alt/package.json"`, 1)
	appText = strings.Replace(appText, `package_lock = "./assistants/support/package-lock.json"`, `package_lock = "./assistants/support-alt/package-lock.json"`, 1)
	if err := os.WriteFile(appPath, []byte(appText), 0o644); err != nil {
		t.Fatal(err)
	}
	implementationResult, err := Compile(implementationRoot)
	if err != nil || !implementationResult.Valid() {
		t.Fatalf("assistant implementation mutation compile: err=%v diagnostics=%#v", err, implementationResult.Diagnostics)
	}
	if implementationResult.Manifest.ContractRevision != first.Manifest.ContractRevision {
		t.Fatalf("assistant implementation mutation changed contract revision: %s -> %s", first.Manifest.ContractRevision, implementationResult.Manifest.ContractRevision)
	}
	if implementationResult.WorkspaceRevision == first.WorkspaceRevision {
		t.Fatalf("assistant implementation mutation did not change workspace revision: %s", first.WorkspaceRevision)
	}

	surfaceRoot := t.TempDir()
	copyTree(t, fixture, surfaceRoot)
	surfacePath := filepath.Join(surfaceRoot, appFilename)
	surfaceSource, err := os.ReadFile(surfacePath)
	if err != nil {
		t.Fatal(err)
	}
	surfaceText := strings.Replace(string(surfaceSource), `path           = "/assistants/support"`, `path           = "/assistants/support-v2"`, 1)
	if err := os.WriteFile(surfacePath, []byte(surfaceText), 0o644); err != nil {
		t.Fatal(err)
	}
	surfaceResult, err := Compile(surfaceRoot)
	if err != nil || !surfaceResult.Valid() {
		t.Fatalf("assistant surface mutation compile: err=%v diagnostics=%#v", err, surfaceResult.Diagnostics)
	}
	if surfaceResult.Manifest.ContractRevision == first.Manifest.ContractRevision {
		t.Fatalf("assistant surface mutation did not change contract revision: %s", first.Manifest.ContractRevision)
	}
}
