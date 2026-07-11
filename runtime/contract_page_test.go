package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"scenery.sh/runtime/shared"
)

func TestContractPageDispatchesTypedInternalLoadAndAction(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	register := func(address, prefix string) {
		t.Helper()
		err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
			Address: address, Visibility: "package", Package: "house", Policy: &ContractHTTPPolicy{AuthorizationStrategy: "public"},
			DecodeInput: func(data []byte) (any, error) {
				var input map[string]string
				return input, json.Unmarshal(data, &input)
			},
			EncodeOutput: func(value any) ([]byte, error) { return json.Marshal(value) },
			Invoke: func(_ context.Context, _ any, input any) (any, error) {
				return map[string]string{"value": prefix + input.(map[string]string)["scene_id"]}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	register("house/binding/load", "loaded:")
	register("house/binding/refresh", "refreshed:")
	if err := RegisterContractPage(ContractPageRegistration{
		Address: "house/page/scene", Package: "house", Path: "/house/scenes/{scene_id}", LoadBinding: "house/binding/load",
		Actions:   map[string]string{"refresh": "house/binding/refresh"},
		Renderers: []ContractRendererRegistration{{Address: "house/renderer/scene_web", Runtime: "web", Module: "ui/Scene.tsx", ImplementationDigest: "sha256:test"}},
	}); err != nil {
		t.Fatal(err)
	}
	state := &requestState{request: shared.Request{InvocationID: "page-1", CallerBinding: "house/page/scene"}, auth: AuthInfo{UID: "user-1"}}
	restoreState := enterState(state)
	defer restoreState()
	ctx := withRuntimeInvocation(withState(context.Background(), state), state)
	for action, want := range map[string]string{"": "loaded:scene-42", "refresh": "refreshed:scene-42"} {
		raw, err := InvokeContractPageJSON(ctx, "house/page/scene", action, []byte(`{"scene_id":"scene-42"}`))
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]string
		if err := json.Unmarshal(raw, &result); err != nil || result["value"] != want {
			t.Fatalf("action %q result = %#v, %v", action, result, err)
		}
	}
	pages := ContractPages()
	if len(pages) != 1 || len(pages[0].Renderers) != 1 || pages[0].Actions["refresh"] == "" {
		t.Fatalf("pages = %#v", pages)
	}
}

func TestContractPageCannotInvokeWithoutRuntimePrincipalContext(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterContractInternalBindingWithPolicy(ContractInternalBindingRegistration{
		Address: "house/binding/load", Visibility: "application", Policy: &ContractHTTPPolicy{AuthorizationStrategy: "public"},
		DecodeInput: func(data []byte) (any, error) { return data, nil }, EncodeOutput: func(value any) ([]byte, error) { return value.([]byte), nil },
		Invoke: func(_ context.Context, _ any, input any) (any, error) { return input, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContractPage(ContractPageRegistration{Address: "house/page/scene", Package: "house", Path: "/scene", LoadBinding: "house/binding/load"}); err != nil {
		t.Fatal(err)
	}
	if _, err := InvokeContractPageJSON(context.Background(), "house/page/scene", "", []byte(`{}`)); err == nil {
		t.Fatal("page invocation without runtime context succeeded")
	}
}
