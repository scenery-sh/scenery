package generate

import (
	"strings"
	"testing"
)

func TestReachableAssistantTypeScriptSurfaceIsTargetScoped(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Module: "app", Name: "public", Kind: "scenery.typescript-client"}
	other := Resource{Address: "app/typescript_client/internal", Module: "app", Name: "internal", Kind: "scenery.typescript-client"}
	resources := []Resource{
		target,
		other,
		{Address: "app/assistant/support", Module: "app", Name: "support", Kind: "scenery.assistant", Spec: map[string]any{
			"surface": map[string]any{"client": map[string]any{"$ref": "typescript_client.public"}, "path": "/assistants/support"},
		}},
		{Address: "app/assistant/hidden", Module: "app", Name: "hidden", Kind: "scenery.assistant", Spec: map[string]any{
			"surface": map[string]any{"client": map[string]any{"$ref": "typescript_client.internal"}, "path": "/assistants/hidden"},
		}},
	}
	assistants := reachableAssistantSurfaces(resources, target)
	if len(assistants) != 1 || assistants[0].Name != "support" {
		t.Fatalf("reachable assistants = %#v", assistants)
	}
	got := renderTypeScriptAssistantFile(target, assistants)
	if !strings.Contains(got, "createConversation") || !strings.Contains(got, "streamEvents") {
		t.Fatalf("assistant client omitted required methods:\n%s", got)
	}
	if !strings.Contains(got, `return new URL(path.replace(/^\//, ""), base).toString();`) {
		t.Fatalf("assistant URL helper still treats surface paths as origin-absolute:\n%s", got)
	}
	if !strings.Contains(got, `if (mediaType === "") throw new Runtime.SceneryClientError("unavailable"`) {
		t.Fatalf("assistant stream client does not retry an empty event content type:\n%s", got)
	}
	for _, forbidden := range []string{"node_modules/", "from \"eve\"", "private_session", "control_token", "mcp_url"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(forbidden)) {
			t.Fatalf("public assistant client contains forbidden provider/private spelling %q", forbidden)
		}
	}
}

func TestRenderedAssistantReactAdapterIsHeadless(t *testing.T) {
	assistants := []Resource{{Address: "app/assistant/support", Module: "app", Name: "support", Kind: "scenery.assistant", Spec: map[string]any{
		"surface": map[string]any{"path": "/assistants/support"},
	}}}
	source := renderReactAssistantAdapter(assistants)
	for _, required := range []string{"useSceneryAssistant", "createConversation", "streamEvents", "AbortController"} {
		if !strings.Contains(source, required) {
			t.Fatalf("react assistant adapter missing %q:\n%s", required, source)
		}
	}
	for _, forbidden := range []string{"@tanstack/react-query", "react-router", "@scenery/ui", "provider"} {
		if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
			t.Fatalf("headless assistant adapter imports or names forbidden dependency %q", forbidden)
		}
	}
}
