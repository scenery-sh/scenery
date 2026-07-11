package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyTypeScriptGenerationUsesCompiledMigrationWithoutLegacyConfig(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "bridge"), root)
	rewriteFixtureSceneryReplace(t, root)
	if err := os.Remove(filepath.Join(root, ".scenery.json")); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"internal/scenerygen",`, `"internal/scenerygen",
    "clients/generated/public_api",`, 1))
	rootSource = append(rootSource, []byte(`

typescript_client "public_api" {
  gateways    = [http_gateway.public_api]
  package     = "@example/bridge-client"
  module      = "esm"
  runtime     = "fetch"
  output_root = "clients/generated/public_api"
}
`)...)
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenery.migration.scn"), []byte(`migration {
  frontend = "scenery.legacy.v0"

  legacy_gateway "default" {
    target = http_gateway.public_api
  }

  legacy_service "bridge" {
    package   = "./bridge"
    namespace = "bridge"
    target    = go_target.development
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	if _, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{},
	}); err != nil {
		t.Fatalf("changes plan: %v", err)
	}
	if _, err := PlanMigrationTransition(root, MigrationPlanRequest{
		Action: "shadow", Service: "bridge", Caller: "test",
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision,
	}); err != nil {
		t.Fatalf("migration plan: %v", err)
	}
	if _, err := GenerateTypeScriptClients(root, "public_api", false); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateTypeScriptClients(root, "public_api", true); err != nil {
		t.Fatalf("legacy client generate --check: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "clients", "generated", "public_api", "legacy-v0", "client.ts")); err != nil {
		t.Fatalf("legacy client output: %v", err)
	}
}

func TestCompiledLegacyClientAuthUsesCanonicalResourcesOnly(t *testing.T) {
	standard, google := compiledLegacyClientAuth([]Resource{{
		Kind: "scenery.authentication/v1", Spec: map[string]any{"provider": map[string]any{"$ref": "std.provider.standard_auth"}},
	}})
	if !standard || google {
		t.Fatalf("canonical standard auth = %t, google = %t", standard, google)
	}
	standard, google = compiledLegacyClientAuth([]Resource{{
		Kind: "scenery.operation/v1", Spec: map[string]any{"handler": map[string]any{"adapter": "legacy_standard_auth_v0", "method": "GoogleStart"}},
	}})
	if !standard || !google {
		t.Fatalf("canonical Google auth = %t, google = %t", standard, google)
	}
	standard, google = compiledLegacyClientAuth([]Resource{{
		Kind: "scenery.operation/v1", Origin: Origin{Kind: "legacy_v0", LegacySymbol: "GoogleAnalytics", LegacyConstruct: "standard auth lookalike"},
		Spec: map[string]any{"handler": map[string]any{"adapter": "legacy_go_v0", "method": "GoogleStart"}},
	}})
	if standard || google {
		t.Fatalf("free-form legacy metadata enabled auth: standard = %t, google = %t", standard, google)
	}
}

func TestRenderContractPolicyIncludesAuthorizationAndOrderedPipeline(t *testing.T) {
	resources := map[string]Resource{
		"app/http_gateway/public": {Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"cors": map[string]any{"$ref": "std.cors.none"}, "forwarded": map[string]any{"$ref": "std.forwarded_headers.reject"}, "trusted_proxies": map[string]any{"$ref": "std.trusted_proxies.none"}}},
		"app/authorization/member": {Address: "app/authorization/member", Kind: "scenery.authorization/v1", Spec: map[string]any{"strategy": "deny_unless_allowed", "rule": []any{
			map[string]any{"name": "signed_in", "allow": map[string]any{"$expression": `principal.uid != ""`}},
			map[string]any{"name": "enabled", "allow": true},
		}}},
		"app/pipeline/default": {Address: "app/pipeline/default", Kind: "scenery.pipeline/v1", Spec: map[string]any{"step": []any{
			map[string]any{"name": "trace", "use": map[string]any{"$ref": "std.middleware.trace"}},
			map[string]any{"name": "request", "use": map[string]any{"$ref": "std.middleware.request_id"}},
		}}},
	}
	binding := Resource{Address: "house/binding/process", Module: "house", Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "authorization": map[string]any{"$ref": "authorization.member"}, "pipeline": map[string]any{"$ref": "pipeline.default"},
	}}
	policy := renderContractHTTPPolicy(resources, binding, map[string]any{})
	for _, fragment := range []string{`BindingAddress: "house/binding/process"`, `Expression: "principal.uid != \"\""`, `Expression: "true"`, `PipelineSteps: []string{"std.middleware.trace", "std.middleware.request_id"}`} {
		if !strings.Contains(policy, fragment) {
			t.Fatalf("policy missing %q:\n%s", fragment, policy)
		}
	}
}

func TestGenerateGoConstructorInjectsStableSQLCapability(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	dataSource := Resource{
		Address: "app/data_source/house_database", Module: "app", Kind: "scenery.data-source/v1", Name: "house_database", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"require_capabilities": []any{"sql.query/v1", "sql.transaction/v1"}, "config": map[string]any{"database": "house"}},
	}
	result.Manifest.Resources = append(result.Manifest.Resources, dataSource)
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.service/v1" {
			result.Manifest.Resources[index].Spec["dependency"] = map[string]any{"name": "database", "instance": map[string]any{"$ref": "data_source.house_database"}}
		}
	}
	module := result.Manifest.Resources[1]
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.module/v1" {
			module = resource
		}
	}
	contractFiles, err := generateModuleContract(result, module)
	if err != nil {
		t.Fatal(err)
	}
	var contractSource string
	for _, file := range contractFiles {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/contract.gen.go") {
			contractSource = string(file.Bytes)
		}
	}
	for _, fragment := range []string{`datasource "scenery.sh/datasource"`, `Database datasource.SQL`} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	var adapter string
	for _, file := range applicationFiles {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/house_house_adapter/adapter.gen.go") {
			adapter = string(file.Bytes)
		}
	}
	for _, fragment := range []string{`scenerydb "scenery.sh/db"`, `scenerydb.Get(ctx, "house")`, `input.Dependencies.Database = dependency0`, `implementation.NewService(ctx, input)`} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateGoConstructorInjectsTypedConfigAndSecretReferences(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{
		Address: "app/secret/provider_token", Module: "app", Kind: "scenery.secret/v1", Name: "provider_token", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"store": map[string]any{"$ref": "secret_store.production"}, "key": "provider/token"},
	})
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind != "scenery.service/v1" {
			continue
		}
		result.Manifest.Resources[index].Spec["config"] = map[string]any{
			"model_path": map[string]any{"$scalar": "relative_path", "value": "models/roof"},
			"workers":    "4",
			"token":      map[string]any{"$ref": "secret.provider_token"},
		}
		result.Manifest.Resources[index].Spec["config_schema"] = []any{
			map[string]any{"name": "model_path", "type": "relative_path"},
			map[string]any{"name": "token", "type": `resource_ref("secret")`, "sensitive": true},
			map[string]any{"name": "workers", "type": "uint32"},
		}
	}
	module := resourceByKind(result.Manifest.Resources, "scenery.module/v1")
	contractFiles, err := generateModuleContract(result, module)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := generatedSourceWithSuffix(contractFiles, "/contract.gen.go")
	for _, fragment := range []string{"ModelPath scenery.RelativePath", "Token     scenery.SecretRef", "Workers   uint32"} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`UnmarshalContractValue([]byte("\"models/roof\"")`,
		`&input.Config.ModelPath, "relative_path"`,
		`input.Config.Token = scenery.SecretRef{Address: "app/secret/provider_token"}`,
		`&input.Config.Workers, "uint32"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}
