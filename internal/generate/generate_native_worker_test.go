package generate

import (
	"bytes"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestNativeWorkerRetainsAllMethodsAndRealConstructorInjection(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	files, err := RenderNativeWorkerWorkspaceFiles(result, "house/binding/process_scene_http")
	if err != nil {
		t.Fatal(err)
	}
	var adapter string
	for path, source := range files {
		if strings.Contains(path, "/nativeworker/") {
			adapter += string(source)
		}
	}
	for _, fragment := range []string{`"scenery.sh/runtime/worker"`, `implementation.NewService(ctx, input)`, `service.ProcessScene(ctx, copied)`, `contract.UnmarshalProcessSceneInput(data)`, `contract.CloneProcessSceneOutcome(outcome)`} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("native adapter missing %q", fragment)
		}
	}
	if strings.Contains(adapter, `"scenery.sh/runtime/host"`) {
		t.Fatal("native adapter links the host")
	}
	main := string(files["scenery_native_worker/main.go"])
	if !strings.Contains(main, "registry.Seal()") || !strings.Contains(main, "RequiredAddresses:") {
		t.Fatal("native composition has no complete admission transaction")
	}
	if _, err := RenderNativeWorkerWorkspaceFiles(result, "unknown"); err == nil {
		t.Fatal("unknown binding was admitted")
	}
}

func TestNativeKernelRetainsPolicyWithoutApplicationImports(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	for i := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[i]
		if resource.Kind == "scenery.operation" {
			resource.Spec["result"] = map[string]any{"name": "processed", "type": map[string]any{"$ref": "json"}}
		}
		if resource.Kind == "scenery.binding" {
			httpSpec := resource.Spec["http"].(map[string]any)
			delete(httpSpec, "body")
		}
	}
	files, err := RenderNativeKernelWorkspaceFiles(result, appcfg.Config{Name: "fixture"}, "house/binding/process_scene_http")
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["scenery_framework_kernel/main.go"])
	for _, fragment := range []string{"VerifyLinkedContractBundle(", "ContractPolicy:", "DecodeContractInput[json.RawMessage]", "client.Invoke(ctx,", "EncodeContractPreparedRepresentationWithOptions"} {
		if !strings.Contains(main, fragment) {
			t.Fatalf("kernel missing %q", fragment)
		}
	}
	if strings.Contains(main, `"clean.tech/`) {
		t.Fatal("kernel imported native application code")
	}
	for i := range result.Manifest.Resources {
		if result.Manifest.Resources[i].Kind == "scenery.binding" {
			result.Manifest.Resources[i].Spec["delivery"] = "stream"
		}
	}
	if _, err := RenderNativeKernelWorkspaceFiles(result, appcfg.Config{Name: "fixture"}, "house/binding/process_scene_http"); err == nil {
		t.Fatal("stream was silently buffered")
	}
}

func TestNativeWorkerColdMetadataDoesNotRenderOrdinaryAdapters(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	input := newProjectionInput(result)
	_, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		t.Fatal(err)
	}
	key, err := input.key("go-application-adapters", generatedImport)
	if err != nil {
		t.Fatal(err)
	}
	adapterProjections.Lock()
	_, found := adapterProjections.entries[key]
	adapterProjections.Unlock()
	if found {
		t.Fatal("fixture must start without ordinary adapter cache")
	}
	publicBefore, err := renderGoPackageProjection(result, input)
	if err != nil {
		t.Fatal(err)
	}
	files, err := RenderNativeWorkerWorkspaceFiles(result, "house/binding/process_scene_http")
	if err != nil {
		t.Fatal(err)
	}
	adapterProjections.Lock()
	_, found = adapterProjections.entries[key]
	adapterProjections.Unlock()
	if found {
		t.Fatal("worker populated ordinary adapter sources")
	}
	for path := range files {
		if path != "scenery_native_worker/main.go" && !strings.Contains(path, "/nativeworker/") {
			t.Fatalf("unexpected private output %s", path)
		}
	}
	publicAfter, err := renderGoPackageProjection(result, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicBefore) != len(publicAfter) {
		t.Fatal("public membership changed")
	}
	for i, before := range publicBefore {
		if before.Path != publicAfter[i].Path || !bytes.Equal(before.Bytes, publicAfter[i].Bytes) {
			t.Fatal("public projection changed")
		}
	}
	ordinary, err := renderApplicationAdapters(result, newResourceIndex(result.Manifest.Resources), generatedImport)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary) != 1 || len(ordinary[0].Source) == 0 || !strings.Contains(string(ordinary[0].Source), "scenery.sh/runtime/host") {
		t.Fatal("ordinary renderer lost host source")
	}
}
