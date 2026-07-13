package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestResolveDeploymentAppliesTypedOverlaysWithProvenance(t *testing.T) {
	manifest := deploymentProfileFixtureManifest()
	projection, diagnostics := ResolveDeployment(manifest, "production")
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if projection.Kind != deploymentProjectionKind || projection.Environment != "production" {
		t.Fatalf("projection = %#v", projection)
	}
	module := projection.Resources["app/module/house"]
	if module.Values["inputs"].(map[string]any)["process_concurrency"] != "16" || module.Provenance["/inputs/process_concurrency"] != "deployment:app/deployment/production" {
		t.Fatalf("module projection = %#v", module)
	}
	service := projection.Resources["house/service/house"]
	if service.Values["replicas"] != "3" {
		t.Fatalf("service projection = %#v", service)
	}
	if source := projection.Resources["app/data_source/database"]; source.Values["config"].(map[string]any)["database"] != "house_production" {
		t.Fatalf("data source projection = %#v", source)
	}
	if provider := projection.Resources["app/provider/postgres"]; provider.Kind != "scenery.provider" {
		t.Fatalf("provider baseline is missing: %#v", provider)
	}
}

func TestResolveDeploymentIncludesOnlySelectedEnvironmentFixtures(t *testing.T) {
	manifest := deploymentProfileFixtureManifest()
	manifest.Resources = append(manifest.Resources,
		Resource{Address: "house/record/scene", Module: "house", Name: "scene", Kind: "scenery.record", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}},
		Resource{Address: "house/entity/scene", Module: "house", Name: "scene", Kind: "scenery.entity", Spec: map[string]any{"type": map[string]any{"$ref": "record.scene"}, "data_source": map[string]any{"$ref": "app/data_source/database"}, "mapping": map[string]any{"relation": "scenes"}, "field": map[string]any{"name": "id", "column": "id", "primary_key": true}}},
		Resource{Address: "house/fixture/demo", Module: "house", Name: "demo", Kind: "scenery.fixture", Spec: map[string]any{"entity": map[string]any{"$ref": "entity.scene"}, "environments": []any{"production"}, "mode": "insert", "values": []any{map[string]any{"id": "one"}}}},
		Resource{Address: "app/deployment/development", Module: "app", Name: "development", Kind: "scenery.deployment", Spec: map[string]any{"environment": "development"}},
	)
	production, diagnostics := ResolveDeployment(manifest, "production")
	if hasErrors(diagnostics) || production.Resources["house/fixture/demo"].Kind != "scenery.fixture" {
		t.Fatalf("production projection = %#v, diagnostics = %#v", production, diagnostics)
	}
	development, diagnostics := ResolveDeployment(manifest, "development")
	if hasErrors(diagnostics) {
		t.Fatalf("development diagnostics = %#v", diagnostics)
	}
	if _, exists := development.Resources["house/fixture/demo"]; exists {
		t.Fatal("production fixture leaked into development projection")
	}
}

func TestDeploymentValidationRejectsContractAndImplementationOverlay(t *testing.T) {
	manifest := deploymentProfileFixtureManifest()
	deployment := manifest.Resources[len(manifest.Resources)-1]
	deployment.Spec["module"] = map[string]any{"target": map[string]any{"$ref": "module.house"}, "inputs": map[string]any{"build_mode": "race"}}
	deployment.Spec["http_gateway"] = map[string]any{"target": map[string]any{"$ref": "http_gateway.public"}, "base_path": "/changed"}
	manifest.Resources[len(manifest.Resources)-1] = deployment
	_, diagnostics := ResolveDeployment(manifest, "production")
	for _, code := range []string{"SCN2802", "SCN2804"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestDeploymentValueMatchesWrappedTypesRecursively(t *testing.T) {
	tests := []struct {
		value    any
		typeName string
		want     bool
	}{
		{value: "42", typeName: "optional(int)", want: true},
		{value: map[string]any{"not": "an integer"}, typeName: "optional(int)", want: false},
		{value: nil, typeName: "optional(int)", want: false},
		{value: nil, typeName: "nullable(int)", want: true},
		{value: "42", typeName: "nullable(int)", want: true},
		{value: map[string]any{"not": "an integer"}, typeName: "nullable(int)", want: false},
		{value: nil, typeName: "optional(nullable(int))", want: false},
		{value: map[string]any{"not": "an integer"}, typeName: "optional(nullable(int))", want: false},
	}
	for _, test := range tests {
		if got := deploymentValueMatchesType(test.value, test.typeName); got != test.want {
			t.Errorf("deploymentValueMatchesType(%#v, %q) = %t, want %t", test.value, test.typeName, got, test.want)
		}
	}
}

func TestDeploymentRevisionRequiresImplementationAndProviderPlan(t *testing.T) {
	base := deploymentProfileFixtureManifest()
	baseContract, err := contractRevision(base.Resources, "app")
	if err != nil {
		t.Fatal(err)
	}
	base.ContractRevision = baseContract
	if revisions := computeDeploymentRevisions(base, nil, nil); len(revisions) != 0 {
		t.Fatalf("revision without implementation or provider plan = %#v", revisions)
	}
	implementation := map[string]string{"application": testDeploymentDigest("implementation")}
	if revisions := computeDeploymentRevisions(base, implementation, nil); len(revisions) != 0 {
		t.Fatalf("revision without provider plan = %#v", revisions)
	}
	if revisions := computeDeploymentRevisions(base, nil, map[string][]string{"production": {testDeploymentDigest("provider")}}); len(revisions) != 0 {
		t.Fatalf("revision without implementation = %#v", revisions)
	}
}

func TestDeploymentRevisionChangesForDeploymentImplementationAndProviderPlan(t *testing.T) {
	base := deploymentProfileFixtureManifest()
	target := deploymentProfileFixtureManifest()
	target.Resources[len(target.Resources)-1].Spec["service"].(map[string]any)["replicas"] = "5"
	base.ContractRevision = "sha256:contract"
	target.ContractRevision = base.ContractRevision
	implementation := map[string]string{"application": testDeploymentDigest("implementation")}
	plans := map[string][]string{"production": {testDeploymentDigest("provider-a")}}
	baseRevision := computeDeploymentRevisions(base, implementation, plans)["production"]
	targetRevision := computeDeploymentRevisions(target, implementation, plans)["production"]
	implementationRevision := computeDeploymentRevisions(base, map[string]string{"application": testDeploymentDigest("implementation-2")}, plans)["production"]
	providerRevision := computeDeploymentRevisions(base, implementation, map[string][]string{"production": {testDeploymentDigest("provider-b")}})["production"]
	if baseRevision == "" || baseRevision == targetRevision || baseRevision == implementationRevision || baseRevision == providerRevision {
		t.Fatalf("base=%q target=%q implementation=%q provider=%q", baseRevision, targetRevision, implementationRevision, providerRevision)
	}
}

func testDeploymentDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func deploymentProfileFixtureManifest() *Manifest {
	return &Manifest{Application: ApplicationIdentity{Name: "app"}, Resources: []Resource{
		{Address: "app/module/house", Module: "app", Name: "house", Kind: "scenery.module", Spec: map[string]any{
			"source": "./house", "interface_inputs": map[string]any{
				"process_concurrency": map[string]any{"type": "uint32", "phase": "deployment", "default": "4"},
				"build_mode":          map[string]any{"type": "string", "phase": "implementation", "default": "normal"},
			},
		}},
		{Address: "app/provider/postgres", Module: "app", Name: "postgres", Kind: "scenery.provider", Spec: map[string]any{"source": "registry/postgres", "config_schema": map[string]any{"database": map[string]any{"deployment_bindable": true}}}},
		{Address: "app/data_source/database", Module: "app", Name: "database", Kind: "scenery.data-source", Spec: map[string]any{"provider": map[string]any{"$ref": "provider.postgres"}, "lifecycle": "managed", "config": map[string]any{"database": "dev"}}},
		{Address: "app/http_gateway/public", Module: "app", Name: "public", Kind: "scenery.http-gateway", Spec: map[string]any{"exposure": "internet", "base_path": "/", "cors": map[string]any{"$ref": "std.cors.none"}, "trusted_proxies": map[string]any{"$ref": "std.trusted_proxies.none"}, "forwarded": map[string]any{"$ref": "std.forwarded_headers.reject"}}},
		{Address: "house/service/house", Module: "house", Name: "house", Kind: "scenery.service", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewService"}}},
		{Address: "app/deployment/production", Module: "app", Name: "production", Kind: "scenery.deployment", Spec: map[string]any{
			"environment":  "production",
			"module":       map[string]any{"target": map[string]any{"$ref": "module.house"}, "inputs": map[string]any{"process_concurrency": "16"}},
			"data_source":  map[string]any{"target": map[string]any{"$ref": "data_source.database"}, "config": map[string]any{"database": "house_production"}},
			"service":      map[string]any{"target": map[string]any{"$ref": "house/service/house"}, "replicas": "3", "resources": map[string]any{"cpu": "2", "memory": "4GiB"}},
			"http_gateway": map[string]any{"target": map[string]any{"$ref": "http_gateway.public"}, "listener": map[string]any{"host": "api.example.test", "port": "443", "tls": "required"}},
		}},
	}}
}
