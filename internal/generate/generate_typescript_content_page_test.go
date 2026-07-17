package generate

import (
	"strings"
	"testing"
)

func TestRenderReactContentPageUsesPageShellAndTypedSlots(t *testing.T) {
	operation := Resource{Address: "work/operation/read", Module: "work", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.read_result"}}}}}
	binding := Resource{Address: "work/binding/read_http", Module: "work", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": operation.Address}}}
	contentSlot := Resource{Address: "work/react_component/content", Module: "work", Name: "content", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Content"}}
	actionsSlot := Resource{Address: "work/react_component/actions", Module: "work", Name: "actions", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Actions"}}
	content := Resource{Address: "work/content_page/summary", Module: "work", Name: "summary", Kind: "scenery.content-page", Spec: map[string]any{
		"path":       "/summary",
		"title":      `Say "hi" \ summary`,
		"aria_label": `Summary "page" \ content`,
		"max_width":  960,
		"content":    map[string]any{"component": map[string]any{"$ref": contentSlot.Address}},
		"actions":    map[string]any{"component": map[string]any{"$ref": actionsSlot.Address}},
	}}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: []Resource{operation, binding, contentSlot, actionsSlot, content}}}
	source, err := renderReactContentPage(result, Resource{Name: "public_api"}, "/app/generated/react", reactContentPage{content: content, operation: operation, binding: binding}, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"defineContentPageSlots<ReadResult>",
		"client.read({})",
		"useState<ContentPageState<ReadResult>>",
		`<Page title={"Say \"hi\" \\ summary"}`,
		`ariaLabel={"Summary \"page\" \\ content"}`,
		"maxWidth={960}",
		"actions={<slots.actions {...slotProps} />}",
		"><slots.content {...slotProps} /></Page>",
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated content page missing %q:\n%s", fragment, source)
		}
	}
}
