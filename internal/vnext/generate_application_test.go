package vnext

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedNativeAndLegacyOperationHandlersCompile(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)

	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http/v1",`, `"scenery.runtime-http/v1",
    "scenery.legacy-bridge/v1",`, 1))
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenery.migration.scn"), []byte(`migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  legacy_gateway "default" {
    target = http_gateway.public_api
  }

  native_service "house" {
    module = module.house
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = []byte(strings.Replace(string(packageSource), `constructor = "NewService"`, `constructor = "NewService"
    root        = "./house"`, 1))
	packageSource = append(packageSource, []byte(`

record "legacy_status_input" {
  field "message" { type = string }
}

record "legacy_status_result" {
  field "message" { type = string }
}

operation "legacy_status" {
  service = service.house
  input   = record.legacy_status_input

  handler {
    method  = "LegacyStatus"
    adapter = "legacy_go_v0"
  }

  result "ok" { type = record.legacy_status_result }
  error "legacy_error" { type = std.type.problem }
}

execution "legacy_status_direct" {
  operation = operation.legacy_status
  mode      = "direct"
  timeout   = "30s"
}

binding "legacy_status_http" {
  gateway   = var.gateway
  operation = operation.legacy_status
  execution = execution.legacy_status_direct
  protocol  = "http"
  delivery  = "call"

  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  http {
    method        = "POST"
    path          = "/house/legacy-status"
    codec_profile = std.codec.http_json_v1

    body {
      codec = "json"
      to    = operation.legacy_status.input
    }

    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec = "json"
        from  = result.ok
      }
    }

    response "legacy_error" {
      when   = error.legacy_error
      status = 500
      body {
        codec = "problem_json"
        from  = error.legacy_error
      }
    }
  }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}

	servicePath := filepath.Join(root, "house", "service.go")
	serviceSource, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	serviceSource = append(serviceSource, []byte(`

type LegacyStatusParams struct {
	Message string `+"`json:\"message\"`"+`
}

type LegacyStatusResponse struct {
	Message string `+"`json:\"message\"`"+`
}

//scenery:api public path=/house/legacy-status method=POST
func (service *Service) LegacyStatus(_ context.Context, input *LegacyStatusParams) (*LegacyStatusResponse, error) {
	return &LegacyStatusResponse{Message: "legacy:" + input.Message}, nil
}
`)...)
	if err := os.WriteFile(servicePath, serviceSource, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile mixed handlers: %v diagnostics=%#v", err, result.Diagnostics)
	}
	status := BuildMigrationStatus(result)
	service, err := status.Service("house")
	if err != nil {
		t.Fatal(err)
	}
	if service.LifecycleAdapter != "native_go_v1" || service.RemainingOperationBridgeCount != 1 || service.AdapterRetirementReady {
		t.Fatalf("mixed handler status = %#v", service)
	}

	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"native.ProcessScene(ctx, input)",
		"SceneryVNextBridgeLegacyStatusWithService(ctx, native, raw)",
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("mixed adapter missing %q:\n%s", fragment, adapter)
		}
	}

	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("mixed generated application compile: %v\n%s", err, output)
	}
}

func TestMixedHandlersCompileWithBridgeLifecycle(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "bridge"), root)
	rewriteFixtureSceneryReplace(t, root)

	packagePath := filepath.Join(root, "bridge", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`

record "native_status_input" {
  field "message" { type = string }
}

record "native_status_result" {
  field "message" { type = string }
}

operation "native_status" {
  service = service.bridge
  input   = record.native_status_input

  handler { method = "NativeStatus" }
  result "ok" { type = record.native_status_result }
}

execution "native_status_direct" {
  operation = operation.native_status
  mode      = "direct"
  timeout   = "30s"
}

binding "native_status_internal" {
  operation = operation.native_status
  execution = execution.native_status_direct
  protocol  = "internal"
  delivery  = "call"

  exposure       = "application"
  authentication = std.authentication.inherit
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  internal {
    visibility = "application"
    principal  = "inherit"
  }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}

	servicePath := filepath.Join(root, "bridge", "service.go")
	serviceSource, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	serviceSource = []byte(strings.Replace(string(serviceSource), `import "context"`, `import (
	"context"

	contract "example.test/bridgeapp/bridge/scenerycontract"
)`, 1))
	serviceSource = append(serviceSource, []byte(`

func (service *Service) NativeStatus(_ context.Context, input contract.NativeStatusInput) (contract.NativeStatusOutcome, error) {
	return contract.NativeStatusOk{Value: contract.NativeStatusResult{Message: "native:" + input.Message}}, nil
}
`)...)
	if err := os.WriteFile(servicePath, serviceSource, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile bridge lifecycle mixed handlers: %v diagnostics=%#v", err, result.Diagnostics)
	}
	status := BuildMigrationStatus(result)
	service, err := status.Service("bridge")
	if err != nil {
		t.Fatal(err)
	}
	if service.LifecycleAdapter != "legacy_go_v0" || service.RemainingOperationBridgeCount != 1 || service.AdapterRetirementReady {
		t.Fatalf("bridge lifecycle status = %#v", service)
	}

	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/bridge_bridge_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"implementation.SceneryVNextBridgeService()",
		"native.NativeStatus(ctx, input)",
		"implementation.SceneryVNextBridgeEcho(ctx, raw)",
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("bridge lifecycle adapter missing %q:\n%s", fragment, adapter)
		}
	}

	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bridge lifecycle generated application compile: %v\n%s", err, output)
	}
}

func TestGeneratedHTTPAdapterBindsSchemaDirectedExactScalarCodecs(t *testing.T) {
	operation := Resource{Address: "house/operation/put_count", Module: "house", Kind: "scenery.operation/v1", Name: "put_count", Spec: map[string]any{"input": map[string]any{"$ref": "int64"}}}
	binding := Resource{Address: "house/binding/put_count_http", Module: "house", Kind: "scenery.binding/v1", Name: "put_count_http"}
	httpSpec := map[string]any{
		"body":          map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.put_count.input"}},
		"request_limit": map[string]any{},
	}
	schema, err := renderContractRequestSchema(map[string]Resource{}, operation, binding, httpSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`Type: "int64"`, `DecodeValue: func(data []byte, target any) error`, `UnmarshalContractValue(data, target, "int64")`} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("generated request schema missing %q:\n%s", fragment, schema)
		}
	}
	response := renderContractResponseOptions(map[string]any{}, "uint64")
	for _, fragment := range []string{`TypeExpression: "uint64"`, `MarshalContractValue(value, "uint64")`} {
		if !strings.Contains(response, fragment) {
			t.Fatalf("generated response options missing %q:\n%s", fragment, response)
		}
	}
}

func TestGenerateGoConstructorInjectsTypedInternalClient(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	internalBinding := Resource{Address: "house/binding/process_scene_internal", Module: "house", Kind: "scenery.binding/v1", Name: "process_scene_internal", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
		"protocol": "internal", "delivery": "call", "exposure": "application",
		"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"internal": map[string]any{"visibility": "application", "principal": "inherit"},
	}}
	result.Manifest.Resources = append(result.Manifest.Resources, internalBinding)
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.service/v1" {
			result.Manifest.Resources[index].Spec["client"] = map[string]any{"name": "self", "binding": map[string]any{"$ref": "binding.process_scene_internal"}}
		}
	}
	module := resourceByKind(result.Manifest.Resources, "scenery.module/v1")
	contractFiles, err := generateModuleContract(result, module)
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
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"type selfInternalClient struct{}",
		`InvokeContractBindingFrom(ctx, "house/binding/process_scene_internal", "house", invocation, copied)`,
		"input.Clients.Self = selfInternalClient{}",
		`RegisterContractInternalBindingWithPolicy(sceneryruntime.ContractInternalBindingRegistration{Address: "house/binding/process_scene_internal"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateApplicationAdapterRegistersExecutablePageContract(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "house/binding/process_scene_internal", Module: "house", Kind: "scenery.binding/v1", Name: "process_scene_internal", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
			"protocol": "internal", "delivery": "call", "exposure": "application", "authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"internal": map[string]any{"visibility": "application", "principal": "inherit"},
		}},
		Resource{Address: "house/page/scene_detail", Module: "house", Kind: "scenery.page/v1", Name: "scene_detail", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"path": "/house/scenes/{scene_id}", "load": map[string]any{"$ref": "binding.process_scene_internal"}, "action": map[string]any{"name": "refresh", "invoke": map[string]any{"$ref": "binding.process_scene_internal"}},
		}},
		Resource{Address: "house/renderer/scene_detail_web", Module: "house", Kind: "scenery.renderer/v1", Name: "scene_detail_web", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"page": map[string]any{"$ref": "page.scene_detail"}, "runtime": "web", "module": "ui/SceneDetail.tsx", "implementation_digest": "sha256:renderer", "config": map[string]any{"theme": "dark"},
		}},
	)
	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`DecodeInput: func(data []byte) (any, error) { return contract.UnmarshalProcessSceneInput(data) }`,
		`EncodeOutput: func(value any) ([]byte, error)`,
		`RegisterContractPage(sceneryruntime.ContractPageRegistration{Address: "house/page/scene_detail"`,
		`LoadBinding: "house/binding/process_scene_internal"`,
		`Actions: map[string]string{"refresh": "house/binding/process_scene_internal"}`,
		`ImplementationDigest: "sha256:renderer"`,
		`ConfigJSON: "{\"theme\":\"dark\"}"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("page adapter missing %q:\n%s", fragment, adapter)
		}
	}
	var descriptor string
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/scenery.generated.v1.json") {
			descriptor = string(file.Bytes)
		}
	}
	if !strings.Contains(descriptor, `house/page/scene_detail`) || !strings.Contains(descriptor, `house/renderer/scene_detail_web`) {
		t.Fatalf("application descriptor does not cover page resources:\n%s", descriptor)
	}
}

func TestGenerateGoInternalClientImportsCrossPackageContract(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.module/v1" && result.Manifest.Resources[index].Name == "house" {
			result.Manifest.Resources[index].Spec["package"] = map[string]any{"go_contract": map[string]any{"import_path": "clean.tech/house"}}
		}
		if result.Manifest.Resources[index].Kind == "scenery.service/v1" {
			result.Manifest.Resources[index].Spec["client"] = map[string]any{"name": "audit", "binding": map[string]any{"$ref": "audit/binding/write_internal"}}
		}
	}
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "app/module/audit", Kind: "scenery.module/v1", Name: "audit", Module: "app", Spec: map[string]any{"source": "./audit", "package": map[string]any{"go_contract": map[string]any{"import_path": "clean.tech/audit"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/service/audit", Kind: "scenery.service/v1", Name: "audit", Module: "audit", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewService"}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/record/write_input", Kind: "scenery.record/v1", Name: "write_input", Module: "audit", Spec: map[string]any{"field": map[string]any{"name": "message", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/operation/write", Kind: "scenery.operation/v1", Name: "write", Module: "audit", Spec: map[string]any{"service": map[string]any{"$ref": "service.audit"}, "input": map[string]any{"$ref": "record.write_input"}, "handler": map[string]any{"method": "Write", "adapter": "legacy_go_v0"}, "result": map[string]any{"name": "written", "type": map[string]any{"$ref": "bool"}}}, Origin: Origin{Kind: "authored"}},
		Resource{Address: "audit/binding/write_internal", Kind: "scenery.binding/v1", Name: "write_internal", Module: "audit", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.write"}, "protocol": "internal", "delivery": "call", "exposure": "application", "authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "internal": map[string]any{"visibility": "application", "principal": "inherit"}}, Origin: Origin{Kind: "authored"}},
	)
	houseModule := resourceByKind(result.Manifest.Resources, "scenery.module/v1")
	contractFiles, err := generateModuleContract(result, houseModule)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := generatedSourceWithSuffix(contractFiles, "/contract.gen.go")
	for _, fragment := range []string{`auditcontract "clean.tech/audit/scenerycontract"`, `Invoke(context.Context, scenery.Invocation, auditcontract.WriteInput) (auditcontract.WriteOutcome, error)`} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("cross-package contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{`auditcontract "clean.tech/audit/scenerycontract"`, `input auditcontract.WriteInput`, `auditcontract.CloneWriteInput`, `value.(auditcontract.WriteOutcome)`, `InvokeContractBindingFrom(ctx, "audit/binding/write_internal", "house"`} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("cross-package adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateApplicationAdapterRegistersAndDispatchesDurableExecution(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{
		Address: "app/execution_engine/tasks", Module: "app", Kind: "scenery.execution-engine/v1", Name: "tasks", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"provider": map[string]any{"$ref": "provider.durable"}, "require_capabilities": []any{"execution.durable/v1"}},
	})
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{
		Address: "house/binding/process_scene_wait", Module: "house", Kind: "scenery.binding/v1", Name: "process_scene_wait", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_durable"},
			"protocol": "internal", "delivery": "wait", "exposure": "application",
			"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"internal": map[string]any{"visibility": "application", "principal": "inherit"},
		},
	})
	for index := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[index]
		switch resource.Kind {
		case "scenery.execution/v1":
			resource.Address = "house/execution/process_scene_durable"
			resource.Name = "process_scene_durable"
			resource.Spec = map[string]any{
				"operation": map[string]any{"$ref": "operation.process_scene"}, "mode": "durable",
				"engine": map[string]any{"$ref": "app/execution_engine/tasks"}, "revision": "3", "timeout": "40m", "lease": "20m", "attempts": "6",
				"external_name": "house.ProcessScene/v1",
				"retry":         map[string]any{"strategy": "exponential", "initial": "10s", "factor": "2", "maximum": "2m"},
				"retention":     map[string]any{"success": "7d", "failure": "30d"},
				"concurrency":   map[string]any{"key": map[string]any{"$expression": "input.scene_id"}, "limit": "2"},
				"deduplication": map[string]any{"retention": "24h", "conflict": "return_existing"},
			}
		case "scenery.operation/v1":
			resource.Spec["idempotency"] = map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.scene_id"}}}
		case "scenery.binding/v1":
			if resource.Spec["protocol"] != "http" {
				continue
			}
			resource.Spec["execution"] = map[string]any{"$ref": "execution.process_scene_durable"}
			resource.Spec["delivery"] = "enqueue"
			httpSpec := resource.Spec["http"].(map[string]any)
			httpSpec["response"] = []any{
				map[string]any{"name": "accepted", "when": map[string]any{"$ref": "dispatch.enqueued"}, "status": "202", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "dispatch.receipt"}}, "header": map[string]any{"name": "x-execution-id", "from": map[string]any{"$ref": "dispatch.receipt.execution_id"}}},
				map[string]any{"name": "rejected", "when": map[string]any{"$ref": "dispatch.rejected"}, "status": "503", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "dispatch.problem"}}},
			}
		}
	}
	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`RegisterContractDurableExecution(sceneryruntime.ContractDurableRegistration{`,
		`Address: "house/execution/process_scene_durable"`,
		`ExternalName: "house.ProcessScene/v1"`,
		`EngineAddress: "app/execution_engine/tasks"`,
		`Revision: 3`,
		`DefaultTimeout: 2400000000000`,
		`DefaultLease: 1200000000000`,
		`MaxAttempts: 6`,
		`RetryInitial: 10000000000`,
		`RetryMax: 120000000000`,
		`RetryBackoff: 2`,
		`MaxConcurrency: 2`,
		`DeduplicationRetention: 86400000000000`,
		`DeduplicationConflict: "return_existing"`,
		`EncodeContractKeyComponent(input.SceneId, "string")`,
		`EncodeContractCompositeKey(component0)`,
		`options.DedupeKey = dedupeKey`,
		`options.ConcurrencyKey = concurrencyKey`,
		`DispatchContractDurableExecutionWithOptions(ctx, "house/execution/process_scene_durable", copied, options)`,
		`DispatchAndWaitContractDurableExecutionWithOptions(ctx, "house/execution/process_scene_durable", copied, options)`,
		`contract.UnmarshalProcessSceneOutcome(data)`,
		`case scenery.ExecutionReceipt:`,
		`sceneryruntime.EncodeContractRepresentationWithOptions(request, 202, typed, "json", []string{"application/json"}, sceneryruntime.ContractResponseOptions`,
		`sceneryruntime.AddContractResponseHeader(&response, "x-execution-id", typed.ExecutionID, sceneryruntime.ContractResponseValueOptions{Encoding: "repeated"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("durable adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateApplicationAdapterRegistersSchedulesConsumersAndEmissions(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "app/event_bus/events", Module: "app", Kind: "scenery.event-bus/v1", Name: "events", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"provider": map[string]any{"$ref": "provider.events"}}},
		Resource{Address: "house/event/scene_processed", Module: "house", Kind: "scenery.event/v1", Name: "scene_processed", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"payload": map[string]any{"$ref": "record.process_scene_result"}, "version": "1"}},
		Resource{Address: "house/binding/process_scene_event", Module: "house", Kind: "scenery.binding/v1", Name: "process_scene_event", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"}, "protocol": "event", "delivery": "call",
			"authentication": map[string]any{"$ref": "std.authentication.service_identity"}, "authorization": map[string]any{"$ref": "std.authorization.application"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"event": map[string]any{"direction": "consume", "bus": map[string]any{"$ref": "app/event_bus/events"}, "channel": "house.process", "contract": map[string]any{"$ref": "event.scene_processed"}, "guarantee": "at_least_once", "broker_retry": map[string]any{"attempts": "5", "backoff": "exponential"}, "dead_letter_channel": "house.process.dead", "map": map[string]any{"from": map[string]any{"$ref": "message.payload"}, "to": map[string]any{"$ref": "operation.process_scene.input"}}},
		}},
		Resource{Address: "house/event_emission/scene_processed", Module: "house", Kind: "scenery.event-emission/v1", Name: "scene_processed", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"bus": map[string]any{"$ref": "app/event_bus/events"}, "channel": "house.processed", "contract": map[string]any{"$ref": "event.scene_processed"}, "guarantee": "at_least_once",
			"ordering_key": map[string]any{"$ref": "result.processed.status"}, "deduplication_key": map[string]any{"$ref": "result.processed.status"},
			"broker_retry": map[string]any{"attempts": "3", "backoff": "fixed"}, "dead_letter_channel": "house.processed.dead",
			"from": map[string]any{"operation": map[string]any{"$ref": "operation.process_scene"}, "when": map[string]any{"$ref": "result.processed"}, "payload": map[string]any{"$ref": "result.processed"}},
		}},
		Resource{Address: "house/schedule/nightly", Module: "house", Kind: "scenery.schedule/v1", Name: "nightly", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"trigger": map[string]any{"cron": "0 2 * * *", "timezone": "Europe/Prague"}, "overlap": "skip", "catchup": map[string]any{"maximum_age": "10m"},
			"invoke": map[string]any{"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"}, "identity": map[string]any{"$ref": "std.workload_identity.scheduler"}, "authorization": map[string]any{"$ref": "std.authorization.scheduled"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "input": map[string]any{"scene_id": "nightly"}},
		}},
	)
	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`RegisterContractSchedule(sceneryruntime.ContractScheduleRegistration{`,
		`TriggerKind: "cron", TriggerValue: "0 2 * * *", Timezone: "Europe/Prague"`,
		`RegisterContractEventConsumer(sceneryruntime.ContractEventConsumerRegistration{`,
		`Channel: "house.process", ContractAddress: "house/event/scene_processed", ContractVersion: 1`,
		`RegisterContractEventEmission(sceneryruntime.ContractEventEmissionRegistration{`,
		`Attempts: 3, Backoff: "fixed", DeadLetterChannel: "house.processed.dead"`,
		`OrderingKey: func(outcome any) (string, error)`,
		`DeduplicationKey: func(outcome any) (string, error)`,
		`PublishContractOperationOutcome(callCtx, "house/operation/process_scene", cloned)`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
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
	base, err := packageABIRevision("clean.tech/house", owned, result.Manifest.Resources)
	if err != nil {
		t.Fatal(err)
	}
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.binding/v1" {
			result.Manifest.Resources[index].Spec["http"].(map[string]any)["path"] = "/different-route"
		}
	}
	routeChanged, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), result.Manifest.Resources)
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
	typeChanged, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), result.Manifest.Resources)
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
		Address: "house/record/private_note", Module: "house", Kind: "scenery.record/v1", Name: "private_note",
		Spec: map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"},
	}
	result.Manifest.Resources = append(result.Manifest.Resources, private)
	owned := moduleResources(result.Manifest.Resources, "house")
	baseABI, err := packageABIRevision("clean.tech/house", owned, result.Manifest.Resources)
	if err != nil {
		t.Fatal(err)
	}
	baseFiles, err := generateModuleContract(result, resourceByKind(result.Manifest.Resources, "scenery.module/v1"))
	if err != nil {
		t.Fatal(err)
	}

	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Address == private.Address {
			result.Manifest.Resources[index].Spec["field"].(map[string]any)["type"] = map[string]any{"$ref": "int64"}
		}
	}
	changedABI, err := packageABIRevision("clean.tech/house", moduleResources(result.Manifest.Resources, "house"), result.Manifest.Resources)
	if err != nil {
		t.Fatal(err)
	}
	changedFiles, err := generateModuleContract(result, resourceByKind(result.Manifest.Resources, "scenery.module/v1"))
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
	source := renderContractTypes([]Resource{{Address: "house/record/empty", Module: "house", Kind: "scenery.record/v1", Name: "empty", Spec: map[string]any{}}})
	for _, fragment := range []string{"type Empty struct", "func (value Empty) MarshalJSON", "func (value *Empty) UnmarshalJSON"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("empty record missing %q:\n%s", fragment, source)
		}
	}
}

func nativeApplicationGenerationFixture(root string) *Result {
	module := Resource{Address: "app/module/house", Kind: "scenery.module/v1", Name: "house", Module: "app", Spec: map[string]any{"source": "./house"}, Origin: Origin{Kind: "authored"}}
	service := Resource{Address: "house/service/house", Kind: "scenery.service/v1", Name: "house", Module: "house", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewService"}}, Origin: Origin{Kind: "authored"}}
	operation := Resource{Address: "house/operation/process_scene", Kind: "scenery.operation/v1", Name: "process_scene", Module: "house", Spec: map[string]any{
		"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "record.process_scene_input"},
		"handler": map[string]any{"method": "ProcessScene"}, "result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.process_scene_result"}},
	}, Origin: Origin{Kind: "authored"}}
	execution := Resource{Address: "house/execution/process_scene_direct", Kind: "scenery.execution/v1", Name: "process_scene_direct", Module: "house", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process_scene"}, "mode": "direct"}, Origin: Origin{Kind: "authored"}}
	binding := Resource{Address: "house/binding/process_scene_http", Kind: "scenery.binding/v1", Name: "process_scene_http", Module: "house", Spec: map[string]any{
		"gateway": map[string]any{"$ref": "var.gateway"}, "operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
		"protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"http": map[string]any{"method": "POST", "path": "/house/process", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
			"body":     map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.process_scene.input"}},
			"response": map[string]any{"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.processed"}}},
		},
	}, Origin: Origin{Kind: "authored"}}
	input := Resource{Address: "house/record/process_scene_input", Kind: "scenery.record/v1", Name: "process_scene_input", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}}
	output := Resource{Address: "house/record/process_scene_result", Kind: "scenery.record/v1", Name: "process_scene_result", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "status", "type": map[string]any{"$ref": "string"}}}, Origin: Origin{Kind: "authored"}}
	goModule := Resource{Address: "app/go_module/application", Kind: "scenery.go-module/v1", Name: "application", Module: "app", Spec: map[string]any{"root": ".", "import_path": "clean.tech"}, Origin: Origin{Kind: "authored"}}
	packageBlock := &Block{Type: "package", Labels: []string{"house"}, Attributes: map[string]Expression{"version": {Kind: "literal", Value: "1.0.0"}}, Blocks: []*Block{{Type: "go_contract", Attributes: map[string]Expression{"import_path": {Kind: "literal", Value: "clean.tech/house"}}}}}
	return &Result{
		Root: root, ContractStatus: "valid", Manifest: &Manifest{ContractRevision: "sha256:contract", Application: ApplicationIdentity{Name: "clean_tech"}, Resources: []Resource{goModule, module, service, input, output, operation, execution, binding}},
		Sources: []*Source{{Relative: "house/scenery.package.scn", Blocks: []*Block{packageBlock}}},
	}
}
