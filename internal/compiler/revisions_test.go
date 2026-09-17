package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	graphmodel "scenery.sh/internal/graph"
)

// Adapter digests are retained by contract revision, which hashes the same
// resource projection; a retained digest equals a fresh computation and the
// retained set stays bounded.
func TestGeneratedAdapterDigestsAreRetainedByContractRevision(t *testing.T) {
	result := func(revision, handler string) *Result {
		return &Result{Manifest: &Manifest{ContractRevision: revision, Resources: []Resource{
			{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"handler": handler}},
		}}}
	}
	for index := range adapterDigestLimit + 2 {
		revision := "sha256:adapter-test-" + strconv.Itoa(index)
		current := result(revision, "Get"+strconv.Itoa(index))
		fresh := computeGeneratedApplicationAdapterDigest(current)
		if first, second := generatedApplicationAdapterDigest(current), generatedApplicationAdapterDigest(current); first != fresh || second != fresh {
			t.Fatalf("revision %s: retained digests %s, %s; fresh %s", revision, first, second, fresh)
		}
	}
	adapterDigests.Lock()
	retained := len(adapterDigests.values)
	adapterDigests.Unlock()
	if retained > adapterDigestLimit {
		t.Fatalf("retained %d adapter digests, limit %d", retained, adapterDigestLimit)
	}
	if uncached := result("", "Other"); generatedApplicationAdapterDigest(uncached) != computeGeneratedApplicationAdapterDigest(uncached) {
		t.Fatal("a result without a contract revision used a retained digest")
	}
}

// A service process's implementation revision follows its own contract
// revision, build inputs and implementation bindings, and nothing of another
// service.
func TestServiceProcessImplementationRevisionsAreIndependentAcrossServices(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.test/estate\n\ngo 1.26.3\n",
		appFilename: `application "estate" {}
go_module "application" {
  root = "."
  import_path = "example.test/estate"
}
go_toolchain "application" { version = "1.26.3" }
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	compiled, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	withServices := func(houseHandler string) *Result {
		result := *compiled
		manifest := *compiled.Manifest
		manifest.Resources = append(slices.Clone(compiled.Manifest.Resources),
			Resource{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"implementation": "House"}},
			Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{"handler": houseHandler}},
			Resource{Address: "garden/service/garden", Kind: "scenery.service", Name: "garden", Module: "garden", Spec: map[string]any{"implementation": "Garden"}},
		)
		result.Manifest = &manifest
		return &result
	}
	processes := func(result *Result, houseContract, houseInputs string) []ServiceProcess {
		return []ServiceProcess{
			{Service: result.Manifest.Resources[len(result.Manifest.Resources)-3], Covered: []string{"house/service/house", "house/operation/get"}, ContractRevision: houseContract, InputDigest: houseInputs},
			{Service: result.Manifest.Resources[len(result.Manifest.Resources)-1], Covered: []string{"garden/service/garden"}, ContractRevision: digest("9"), InputDigest: digest("8")},
		}
	}
	revisions := func(result *Result, houseContract, houseInputs string) map[string]string {
		t.Helper()
		values, diagnostics := ServiceProcessImplementationRevisions(result, "development", processes(result, houseContract, houseInputs))
		if hasErrors(diagnostics) || len(values) != 2 {
			t.Fatalf("service process revisions = %#v diagnostics %#v", values, diagnostics)
		}
		return values
	}
	base := revisions(withServices("Get"), digest("1"), digest("2"))
	target, _ := ComputeImplementationRevisions(withServices("Get"), map[string]string{"development": digest("2")})
	if base["house/service/house"] == base["garden/service/garden"] || base["house/service/house"] == target["development"] {
		t.Fatalf("service process revisions are not distinct: %#v, target %s", base, target["development"])
	}
	for name, changed := range map[string]map[string]string{
		"contract": revisions(withServices("Get"), digest("3"), digest("2")),
		"inputs":   revisions(withServices("Get"), digest("1"), digest("4")),
		"handler":  revisions(withServices("Fetch"), digest("1"), digest("2")),
	} {
		if changed["house/service/house"] == base["house/service/house"] || changed["garden/service/garden"] != base["garden/service/garden"] {
			t.Errorf("a house %s change: house %t, garden %t changed; want only house", name, changed["house/service/house"] != base["house/service/house"], changed["garden/service/garden"] != base["garden/service/garden"])
		}
	}
	if _, diagnostics := ServiceProcessImplementationRevisions(withServices("Get"), "development", processes(withServices("Get"), "sha256:short", digest("2"))); !hasErrors(diagnostics) {
		t.Fatal("accepted a non-canonical service contract revision")
	}
}

// Service contract revisions are retained by application contract revision; a
// retained revision equals a fresh projection.
func TestServiceContractRevisionsAreRetainedByApplicationContractRevision(t *testing.T) {
	manifest := func(revision, fields string) *Manifest {
		return &Manifest{ContractRevision: revision, Application: graphmodel.ApplicationIdentity{Name: "estate"}, Resources: []Resource{
			{Address: "house/record/item", Kind: "scenery.record", Name: "item", Module: "house", Spec: map[string]any{"unknown_fields": fields}},
			{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"runtime": "go"}},
		}}
	}
	for _, value := range []*Manifest{manifest("sha256:retained-a", "reject"), manifest("sha256:retained-b", "ignore"), manifest("", "reject")} {
		fresh := graphmodel.ServiceContractRevision(value, value.Resources[1], []string{"house/service/house"})
		if first, second := ServiceContractRevision(value, value.Resources[1], []string{"house/service/house"}), ServiceContractRevision(value, value.Resources[1], []string{"house/service/house"}); first != fresh || second != fresh {
			t.Fatalf("revision %q: retained %s, %s; fresh %s", value.ContractRevision, first, second, fresh)
		}
	}
	if ServiceContractRevision(manifest("sha256:retained-a", "reject"), manifest("", "").Resources[1], []string{"house/service/house"}) == ServiceContractRevision(manifest("sha256:retained-b", "ignore"), manifest("", "").Resources[1], []string{"house/service/house"}) {
		t.Fatal("different service contracts shared a retained revision")
	}
}
