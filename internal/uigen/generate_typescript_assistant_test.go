package uigen

import (
	"strings"
	"testing"
)

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
