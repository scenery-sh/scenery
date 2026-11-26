package generate

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	generateapi "scenery.sh/internal/generate/api"
)

func TestGenerateGoConstructorInjectsTypedInternalClient(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	internalBinding := Resource{Address: "house/binding/process_scene_internal", Module: "house", Kind: "scenery.binding", Name: "process_scene_internal", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
		"protocol": "internal", "delivery": "call", "exposure": "application",
		"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"internal": map[string]any{"visibility": "application", "principal": "inherit"},
	}}
	result.Manifest.Resources = append(result.Manifest.Resources, internalBinding)
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.service" {
			result.Manifest.Resources[index].Spec["client"] = map[string]any{"name": "self", "binding": map[string]any{"$ref": "binding.process_scene_internal"}}
		}
	}
	module := resourceByKind(result.Manifest.Resources, "scenery.module")
	contractFiles, err := generateModuleContract(result, newResourceIndex(result.Manifest.Resources), module)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := generatedSourceWithSuffix(contractFiles, "/contract.gen.go")
	for _, fragment := range []string{
		"type SelfInternalClient interface",
		"Invoke(context.Context, scenery.Invocation, ProcessSceneInput) (ProcessSceneOutcome, error)",
		"Self SelfInternalClient",
	} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"type selfInternalClient struct{}",
		`sceneryruntime.InvokeWorkerBinding(ctx, invocation, "self", false, raw)`,
		`outcome, err := contract.UnmarshalProcessSceneOutcome(output)`,
		"input.Clients.Self = selfInternalClient{}",
		`sceneryruntime.RegisterWorkerOperation("house/operation/process_scene"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateApplicationDescriptorCoversPageResources(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "house/binding/process_scene_internal", Module: "house", Kind: "scenery.binding", Name: "process_scene_internal", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
			"protocol": "internal", "delivery": "call", "exposure": "application", "authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"internal": map[string]any{"visibility": "application", "principal": "inherit"},
		}},
		Resource{Address: "house/page/scene_detail", Module: "house", Kind: "scenery.page", Name: "scene_detail", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"path": "/house/scenes/{scene_id}", "load": map[string]any{"$ref": "binding.process_scene_internal"}, "action": map[string]any{"name": "refresh", "invoke": map[string]any{"$ref": "binding.process_scene_internal"}},
		}},
		Resource{Address: "house/renderer/scene_detail_web", Module: "house", Kind: "scenery.renderer", Name: "scene_detail_web", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"page": map[string]any{"$ref": "page.scene_detail"}, "runtime": "web", "module": "ui/SceneDetail.tsx", "implementation_digest": "sha256:renderer", "config": map[string]any{"theme": "dark"},
		}},
	)
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor string
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/scenery.generated.json") {
			descriptor = string(file.Bytes)
		}
	}
	if !strings.Contains(descriptor, `house/page/scene_detail`) || !strings.Contains(descriptor, `house/renderer/scene_detail_web`) {
		t.Fatalf("application descriptor does not cover page resources:\n%s", descriptor)
	}
}

func TestGenerateGoInternalClientProjectsCrossPackageContract(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.module" && result.Manifest.Resources[index].Name == "house" {
			result.Manifest.Resources[index].Spec["package"] = map[string]any{"go_contract": map[string]any{"import_path": "clean.tech/house"}}
		}
		if result.Manifest.Resources[index].Kind == "scenery.service" {
			result.Manifest.Resources[index].Spec["client"] = map[string]any{"name": "audit", "binding": map[string]any{"$ref": "audit/binding/write_internal"}}
		}
	}
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "app/module/audit", Kind: "scenery.module", Name: "audit", Module: "app", Spec: map[string]any{"source": "./audit", "package": map[string]any{"go_contract": map[string]any{"import_path": "clean.tech/audit"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/service/audit", Kind: "scenery.service", Name: "audit", Module: "audit", Spec: map[string]any{"runtime": "go"}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/record/write_input", Kind: "scenery.record", Name: "write_input", Module: "audit", Spec: map[string]any{"field": map[string]any{"name": "message", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/operation/write", Kind: "scenery.operation", Name: "write", Module: "audit", Spec: map[string]any{"service": map[string]any{"$ref": "service.audit"}, "input": map[string]any{"$ref": "audit/record/write_input"}, "handler": map[string]any{"method": "Write"}, "result": map[string]any{"name": "written", "type": map[string]any{"$ref": "bool"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/binding/write_internal", Kind: "scenery.binding", Name: "write_internal", Module: "audit", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.write"}, "protocol": "internal", "delivery": "call", "exposure": "application", "authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "internal": map[string]any{"visibility": "application", "principal": "inherit"}}, Origin: Origin{Kind: "authored"}},
	)
	houseModule := resourceByKind(result.Manifest.Resources, "scenery.module")
	contractFiles, err := generateModuleContract(result, newResourceIndex(result.Manifest.Resources), houseModule)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := generatedSourceWithSuffix(contractFiles, "/contract.gen.go")
	for _, fragment := range []string{`Invoke(context.Context, scenery.Invocation, AuditClientInput) (AuditClientOutcome, error)`, `type AuditClientInput = AuditClientWriteInput`, `type AuditClientWritten struct`} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("cross-package contract missing %q:\n%s", fragment, contractSource)
		}
	}
	if strings.Contains(contractSource, "clean.tech/audit/scenerycontract") {
		t.Fatalf("caller contract imports callee code:\n%s", contractSource)
	}
	applicationFiles, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{`input contract.AuditClientInput`, `sceneryruntime.InvokeWorkerBinding(ctx, invocation, "audit", false, raw)`, `outcome, err := contract.UnmarshalAuditClientOutcome(output)`} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("cross-package adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestServiceProcessCoversItsSchedules(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "house/schedule/nightly", Module: "house", Kind: "scenery.schedule", Name: "nightly", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"trigger": map[string]any{"every": "1d"}, "overlap": "skip", "catchup": map[string]any{"maximum_age": "10m"},
			"invoke": map[string]any{"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"}, "identity": map[string]any{"$ref": "std.workload_identity.scheduler"}, "authorization": map[string]any{"$ref": "std.authorization.scheduled"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "input": map[string]any{"scene_id": "nightly"}},
		}},
	)
	// The service's own worker process registers this adapter, so it covers
	// the service's schedules.
	plan, err := BuildRuntimeIntegrationPlan(result)
	if err != nil {
		t.Fatal(err)
	}
	house := slices.IndexFunc(plan.Services, func(service generateapi.ServiceProcessPlan) bool { return service.Name == "house_house" })
	if house < 0 {
		t.Fatalf("plan has no house service process: %#v", plan.Services)
	}
	for _, address := range []string{"house/schedule/nightly"} {
		if !slices.Contains(plan.Services[house].RequiredAddresses, address) {
			t.Fatalf("house service process does not require %s: %v", address, plan.Services[house].RequiredAddresses)
		}
	}
}

func resourceByKind(resources []Resource, kind string) Resource {
	for _, resource := range resources {
		if resource.Kind == kind {
			return resource
		}
	}
	return Resource{}
}

func generatedSourceWithSuffix(files []generatedFile, suffix string) string {
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), suffix) {
			return string(file.Bytes)
		}
	}
	return ""
}

func TestPackageABIExcludesRoutesAndIncludesReachableTypes(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	owned := moduleResources(result.Manifest.Resources, "house")
	base, err := packageABIRevision("clean.tech/house", owned, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.binding" {
			result.Manifest.Resources[index].Spec["http"].(map[string]any)["path"] = "/different-route"
		}
	}
	routeChanged, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	if routeChanged != base {
		t.Fatalf("route changed package ABI: %s != %s", routeChanged, base)
	}
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Address == "house/record/process_scene_input" {
			result.Manifest.Resources[index].Spec["field"].(map[string]any)["type"] = map[string]any{"$ref": "int64"}
		}
	}
	typeChanged, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	if typeChanged == base {
		t.Fatal("reachable input type did not change package ABI")
	}
}

func TestPackageABIAndGeneratedContractExcludeUnreachablePrivateTypes(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	private := Resource{
		Address: "house/record/private_note", Module: "house", Kind: "scenery.record", Name: "private_note",
		Spec: map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"},
	}
	result.Manifest.Resources = append(result.Manifest.Resources, private)
	owned := moduleResources(result.Manifest.Resources, "house")
	baseABI, err := packageABIRevision("clean.tech/house", owned, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	baseFiles, err := generateModuleContract(result, newResourceIndex(result.Manifest.Resources), resourceByKind(result.Manifest.Resources, "scenery.module"))
	if err != nil {
		t.Fatal(err)
	}

	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Address == private.Address {
			result.Manifest.Resources[index].Spec["field"].(map[string]any)["type"] = map[string]any{"$ref": "int64"}
		}
	}
	changedABI, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	changedFiles, err := generateModuleContract(result, newResourceIndex(result.Manifest.Resources), resourceByKind(result.Manifest.Resources, "scenery.module"))
	if err != nil {
		t.Fatal(err)
	}
	if changedABI != baseABI {
		t.Fatalf("unreachable private type changed package ABI: %s != %s", changedABI, baseABI)
	}
	if before, after := generatedSourceWithSuffix(baseFiles, "types.gen.go"), generatedSourceWithSuffix(changedFiles, "types.gen.go"); before != after {
		t.Fatalf("unreachable private type changed exported Go types:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestRenderEmptyRecordProducesUsableGoValue(t *testing.T) {
	source := renderContractTypes([]Resource{{Address: "house/record/empty", Module: "house", Kind: "scenery.record", Name: "empty", Spec: map[string]any{}}})
	for _, fragment := range []string{"type Empty struct", "func (value Empty) MarshalJSON", "func (value *Empty) UnmarshalJSON"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("empty record missing %q:\n%s", fragment, source)
		}
	}
}

func nativeApplicationGenerationFixture(root string) *Result {
	module := Resource{Address: "app/module/house", Kind: "scenery.module", Name: "house", Module: "app", Spec: map[string]any{"source": "./house"}, Origin: Origin{Kind: "authored"}}
	service := Resource{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewService"}}, Origin: Origin{Kind: "authored"}}
	operation := Resource{Address: "house/operation/process_scene", Kind: "scenery.operation", Name: "process_scene", Module: "house", Spec: map[string]any{
		"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "record.process_scene_input"},
		"handler": map[string]any{"method": "ProcessScene"}, "result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.process_scene_result"}},
	}, Origin: Origin{Kind: "authored"}}
	execution := Resource{Address: "house/execution/process_scene_direct", Kind: "scenery.execution", Name: "process_scene_direct", Module: "house", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process_scene"}, "mode": "direct"}, Origin: Origin{Kind: "authored"}}
	binding := Resource{Address: "house/binding/process_scene_http", Kind: "scenery.binding", Name: "process_scene_http", Module: "house", Spec: map[string]any{
		"gateway": map[string]any{"$ref": "var.gateway"}, "operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
		"protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"http": map[string]any{"method": "POST", "path": "/house/process", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
			"body":     map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.process_scene.input"}},
			"response": map[string]any{"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.processed"}}},
		},
	}, Origin: Origin{Kind: "authored"}}
	input := Resource{Address: "house/record/process_scene_input", Kind: "scenery.record", Name: "process_scene_input", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}}
	output := Resource{Address: "house/record/process_scene_result", Kind: "scenery.record", Name: "process_scene_result", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "status", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}}
	goModule := Resource{Address: "app/go_module/application", Kind: "scenery.go-module", Name: "application", Module: "app", Spec: map[string]any{"root": ".", "import_path": "clean.tech"}, Origin: Origin{Kind: "authored"}}
	packageBlock := &Block{Type: "package", Labels: []string{"house"}, Attributes: map[string]Expression{"version": {Kind: "literal", Value: "1.0.0"}}, Blocks: []*Block{{Type: "go_contract", Attributes: map[string]Expression{"import_path": {Kind: "literal", Value: "clean.tech/house"}}}}}
	return &Result{
		Root: root, ContractStatus: "valid", Manifest: &Manifest{ContractRevision: "sha256:contract", Application: ApplicationIdentity{Name: "clean_tech"}, Resources: []Resource{goModule, module, service, input, output, operation, execution, binding}},
		Sources: []*Source{{Relative: "house/" + testPackageFilename, Blocks: []*Block{packageBlock}}},
	}
}

func moduleResources(resources []Resource, module string) []Resource {
	var out []Resource
	for _, resource := range resources {
		if resource.Module == module {
			out = append(out, resource)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}
