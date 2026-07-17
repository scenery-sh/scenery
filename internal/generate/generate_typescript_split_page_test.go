package generate

import (
	"strings"
	"testing"
)

func TestRenderReactSplitPageUsesTypedSlots(t *testing.T) {
	operation := Resource{Address: "work/operation/read", Module: "work", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.read_result"}}}}}
	binding := Resource{Address: "work/binding/read_http", Module: "work", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": operation.Address}}}
	sidebar := Resource{Address: "work/react_component/sidebar", Module: "work", Name: "sidebar", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Sidebar"}}
	detail := Resource{Address: "work/react_component/detail", Module: "work", Name: "detail", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Detail"}}
	split := Resource{Address: "work/split_page/work", Module: "work", Name: "work", Kind: "scenery.split-page", Spec: map[string]any{"path": "/work", "title": `Say "hi" \ work`, "aria_label": `Split "work" \ page`, "sidebar_label": `Work "list" \ sidebar`, "sidebar": map[string]any{"component": map[string]any{"$ref": sidebar.Address}}, "detail": map[string]any{"component": map[string]any{"$ref": detail.Address}}}}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: []Resource{operation, binding, sidebar, detail, split}}}
	source, err := renderReactSplitPage(result, Resource{Name: "public_api"}, "/app/generated/react", reactSplitPage{split: split, operation: operation, binding: binding}, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"defineSplitPageSlots<ReadResult>",
		"client.read({})",
		`import { useQuery } from "@tanstack/react-query"`,
		`const queryKey = ["scenery", "split_page", "work/split_page/work"] as const`,
		"const query = useQuery({ queryKey, queryFn: load })",
		"const state: SplitPageState<ReadResult> = requestStateFromQuery<{ readonly data: ReadResult }>(query)",
		"useState<string | null>",
		`syncSelectionFromURL(); globalThis.addEventListener("popstate", syncSelectionFromURL)`,
		`globalThis.removeEventListener("popstate", syncSelectionFromURL)`,
		"nextURL.searchParams.delete(queryParameter)",
		`<SplitPage sidebarTitle={"Say \"hi\" \\ work"}`,
		`ariaLabel={"Split \"work\" \\ page"}`,
		`sidebarLabel={"Work \"list\" \\ sidebar"}`,
		"sidebar={<slots.sidebar",
		"detail={<slots.detail",
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated split page missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{"useState<SplitPageState", "void load().then"} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated split page retains manual request state %q:\n%s", fragment, source)
		}
	}
	delete(split.Spec, "aria_label")
	delete(split.Spec, "sidebar_label")
	source, err = renderReactSplitPage(result, Resource{Name: "public_api"}, "/app/generated/react", reactSplitPage{split: split, operation: operation, binding: binding}, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"ariaLabel=", "sidebarLabel="} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated split page includes defaulted %q:\n%s", fragment, source)
		}
	}
}

func TestHumanLabelPreservesUTF8(t *testing.T) {
	if got, want := humanLabel("žlutý_kůň"), "Žlutý Kůň"; got != want {
		t.Fatalf("humanLabel() = %q, want %q", got, want)
	}
}
