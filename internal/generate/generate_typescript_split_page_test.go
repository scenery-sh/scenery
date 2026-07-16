package generate

import (
	"strings"
	"testing"
)

func TestRenderReactSplitPageUsesTypedSlots(t *testing.T) {
	operation := Resource{Address: "work/operation/read", Module: "work", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.read_result"}}}}}
	binding := Resource{Address: "work/binding/read_http", Module: "work", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": operation.Address}}}
	pane := Resource{Address: "work/react_component/pane", Module: "work", Name: "pane", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Pane"}}
	detail := Resource{Address: "work/react_component/detail", Module: "work", Name: "detail", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Detail"}}
	split := Resource{Address: "work/split_page/work", Module: "work", Name: "work", Kind: "scenery.split-page", Spec: map[string]any{"path": "/work", "title": "Work", "pane": map[string]any{"component": map[string]any{"$ref": pane.Address}}, "detail": map[string]any{"component": map[string]any{"$ref": detail.Address}}}}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: []Resource{operation, binding, pane, detail, split}}}
	source, err := renderReactSplitPage(result, Resource{Name: "public_api"}, "/app/generated/react", reactSplitPage{split: split, operation: operation, binding: binding}, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"defineSplitPageSlots<ReadResult>", "client.read({})", "<slots.pane", "<slots.detail"} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated split page missing %q:\n%s", fragment, source)
		}
	}
}
