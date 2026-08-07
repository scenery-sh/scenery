package deployplan

import "testing"

func TestResolveResourceRefTreatsAssistantRootsAsApplicationScoped(t *testing.T) {
	resource := Resource{Module: "house"}
	for _, test := range []struct {
		kind      string
		reference string
		want      string
	}{
		{kind: "mcp_connection", reference: "mcp_connection.docs", want: "app/mcp_connection/docs"},
		{kind: "mcp_server", reference: "mcp_server.support", want: "app/mcp_server/support"},
		{kind: "assistant", reference: "assistant.support", want: "app/assistant/support"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			if got := resolveResourceRef(resource, test.reference, "resource"); got != test.want {
				t.Fatalf("resolveResourceRef(%q) = %q, want %q", test.reference, got, test.want)
			}
		})
	}
}
