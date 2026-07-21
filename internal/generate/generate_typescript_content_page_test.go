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
		`import { useQuery } from "@tanstack/react-query"`,
		`const queryKey = ["scenery", "content_page", "work/content_page/summary"] as const`,
		"const query = useQuery({ queryKey, queryFn: load })",
		"const state: ContentPageState<ReadResult> = requestStateFromQuery<{ readonly data: ReadResult }>(query)",
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
	for _, fragment := range []string{"useState<ContentPageState", "void load().then"} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated content page retains manual request state %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactStaticContentPageUsesNoClientOrLoad(t *testing.T) {
	contentSlot := Resource{Address: "work/react_component/content", Module: "work", Name: "content", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Content"}}
	actionsSlot := Resource{Address: "work/react_component/actions", Module: "work", Name: "actions", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Actions"}}
	content := Resource{Address: "work/content_page/privacy", Module: "work", Name: "privacy", Kind: "scenery.content-page", Spec: map[string]any{
		"path":    "/privacy",
		"title":   "Privacy",
		"content": map[string]any{"component": map[string]any{"$ref": contentSlot.Address}},
		"actions": map[string]any{"component": map[string]any{"$ref": actionsSlot.Address}},
	}}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: []Resource{contentSlot, actionsSlot, content}}}
	pages := selectedReactContentPages(result.Manifest.Resources, nil)
	if len(pages) != 1 || pages[0].binding.Address != "" || pages[0].operation.Address != "" {
		t.Fatalf("selected static pages = %#v", pages)
	}
	source, err := renderReactContentPage(result, Resource{Name: "public_api"}, "/app/generated/react", pages[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`import { Page } from "./scenery-ui/index.js"`,
		"export function PrivacyPage()",
		`<Page title={"Privacy"} actions={<SceneryContentSlot2 />}><SceneryContentSlot1 /></Page>`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated static content page missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{
		"PublicApiClient",
		"useQuery",
		"queryKey",
		"requestStateFromQuery",
		"ContentPageState",
		"defineContentPageSlots",
		"client.read",
	} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated static content page contains data-loading fragment %q:\n%s", fragment, source)
		}
	}
}
