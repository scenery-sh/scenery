package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/assistantcontrol"
	"scenery.sh/internal/spec"
)

func TestAssistantControlSchemaRevisionsMatchDocuments(t *testing.T) {
	t.Parallel()
	root := repoRootForTest(t)
	for name, want := range map[string]string{
		"scenery.assistant.control.request.schema.json":    assistantcontrol.RequestSchemaRevision,
		"scenery.assistant.control.response.schema.json":   assistantcontrol.ResponseSchemaRevision,
		"scenery.assistant.control.event.schema.json":      assistantcontrol.EventSchemaRevision,
		"scenery.assistant.runtime-descriptor.schema.json": assistantcontrol.DescriptorSchemaRevision,
	} {
		data, err := os.ReadFile(filepath.Join(root, "docs", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := spec.SchemaDocumentRevision(data)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s revision = %q, want %q", name, got, want)
		}
	}
}

func TestAssistantControlSchemasValidateCurrentVariants(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	requestPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.control.request.schema.json")
	requests := map[string]map[string]any{
		"create": controlRequest("conversation.create", map[string]any{
			"principal":           "principal-a",
			"conversation_digest": "sha256:conversation",
			"run_id":              "run-a",
			"message":             "hello",
		}),
		"turn": controlRequest("conversation.turn", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
			"message":            "follow up",
		}),
		"resume": controlRequest("events.resume", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"after":              4,
		}),
		"approval": controlRequest("approval.resolve", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
			"approval_id":        "approval-a",
			"decision":           "allow",
		}),
		"cancel": controlRequest("run.cancel", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
		}),
		"health": controlRequest("health", nil),
		"info":   controlRequest("info", nil),
	}
	for name, payload := range requests {
		if diagnostics := validateHarnessJSONSchemaFile(requestPath, payload); len(diagnostics) != 0 {
			t.Errorf("valid %s request diagnostics = %v", name, diagnostics)
		}
	}

	responsePath := filepath.Join(root, "docs", "schemas", "scenery.assistant.control.response.schema.json")
	responses := map[string]map[string]any{
		"created": controlResponse("conversation.created", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
			"data":               map[string]any{"conversation_id": "conv-private"},
		}),
		"turn": controlResponse("conversation.turn.accepted", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
		}),
		"resumed": controlResponse("events.resumed", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"next_sequence":      3,
			"events":             []any{controlEvent("text.delta", 1)},
		}),
		"approval": controlResponse("approval.resolved", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
			"approval_id":        "approval-a",
			"decision":           "deny",
		}),
		"cancel": controlResponse("run.cancelled", map[string]any{
			"private_session_id": "session-a",
			"continuation_token": "continuation-a",
			"run_id":             "run-a",
		}),
		"health": controlResponse("health", map[string]any{
			"health": map[string]any{
				"ready":               true,
				"runtime_revision":    "runtime-a",
				"capability_revision": "capability-a",
				"status":              "ready",
			},
		}),
		"info": controlResponse("info", map[string]any{
			"descriptor": map[string]any{
				"kind":                "scenery.assistant.runtime-descriptor",
				"schema_revision":     assistantcontrol.DescriptorSchemaRevision,
				"assistant_address":   "app/assistant/support",
				"runtime_revision":    "runtime-a",
				"capability_revision": "capability-a",
				"control_protocol":    assistantcontrol.ControlProtocol,
				"mcp_protocol":        "2025-11-25",
			},
		}),
		"error": controlResponse("error", map[string]any{
			"error": map[string]any{
				"code":      "helper_unavailable",
				"message":   "helper unavailable",
				"retryable": true,
			},
		}),
	}
	for name, payload := range responses {
		if diagnostics := validateHarnessJSONSchemaFile(responsePath, payload); len(diagnostics) != 0 {
			t.Errorf("valid %s response diagnostics = %v", name, diagnostics)
		}
	}

	eventPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.control.event.schema.json")
	events := map[string]map[string]any{
		"session": controlEvent("text.delta", 1),
		"proposal": func() map[string]any {
			event := controlEvent("capability.proposal", 2)
			event["capability_name"] = "process_scene"
			event["approval_id"] = "approval-a"
			event["capability_proposal"] = map[string]any{
				"approval_id":       "approval-a",
				"capability_name":   "process_scene",
				"input":             map[string]any{"scene_id": "one"},
				"requires_approval": true,
			}
			return event
		}(),
		"approval": func() map[string]any {
			event := controlEvent("approval.wait", 3)
			event["approval_id"] = "approval-a"
			event["approval_wait"] = map[string]any{"approval_id": "approval-a", "expires_at": "2030-01-01T00:00:00Z"}
			return event
		}(),
		"restart": func() map[string]any {
			event := controlEvent("runtime.restarting", 4)
			event["crash"] = map[string]any{"code": "helper_restarted", "message": "helper restarted", "restartable": true}
			return event
		}(),
		"malformed": func() map[string]any {
			event := controlEvent("protocol.malformed", 5)
			event["private_session_id"] = ""
			event["run_id"] = ""
			event["malformed"] = map[string]any{"code": "malformed_event", "message": "unknown event payload", "observed_type": "provider.event"}
			return event
		}(),
	}
	for name, payload := range events {
		if diagnostics := validateHarnessJSONSchemaFile(eventPath, payload); len(diagnostics) != 0 {
			t.Errorf("valid %s event diagnostics = %v", name, diagnostics)
		}
	}

	descriptorPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.runtime-descriptor.schema.json")
	descriptor := map[string]any{
		"kind":                "scenery.assistant.runtime-descriptor",
		"schema_revision":     assistantcontrol.DescriptorSchemaRevision,
		"assistant_address":   "app/assistant/support",
		"runtime_revision":    "runtime-a",
		"capability_revision": "capability-a",
		"control_protocol":    assistantcontrol.ControlProtocol,
		"mcp_protocol":        "2025-11-25",
	}
	if diagnostics := validateHarnessJSONSchemaFile(descriptorPath, descriptor); len(diagnostics) != 0 {
		t.Fatalf("valid runtime descriptor diagnostics = %v", diagnostics)
	}
}

func TestAssistantControlSchemasRejectStaleIdentitiesAndUnknownNestedFields(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	request := controlRequest("health", nil)
	request["schema_revision"] = "sha256:" + strings.Repeat("0", 64)
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.control.request.schema.json"), request); len(diagnostics) == 0 {
		t.Fatal("request accepted stale schema revision")
	}

	response := controlResponse("health", map[string]any{
		"health": map[string]any{
			"ready":               true,
			"runtime_revision":    "runtime-a",
			"capability_revision": "capability-a",
			"unexpected":          true,
		},
	})
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.control.response.schema.json"), response); len(diagnostics) == 0 {
		t.Fatal("response accepted unknown nested health field")
	}

	event := controlEvent("text.delta", 1)
	event["unknown"] = true
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.control.event.schema.json"), event); len(diagnostics) == 0 {
		t.Fatal("event accepted unknown envelope field")
	}

	descriptor := map[string]any{
		"kind":                "scenery.assistant.runtime-descriptor",
		"schema_revision":     assistantcontrol.DescriptorSchemaRevision,
		"assistant_address":   "app/assistant/support",
		"runtime_revision":    "runtime-a",
		"capability_revision": "capability-a",
		"control_protocol":    assistantcontrol.ControlProtocol,
		"mcp_protocol":        "2026-07-28",
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.runtime-descriptor.schema.json"), descriptor); len(diagnostics) == 0 {
		t.Fatal("runtime descriptor accepted a non-current MCP revision")
	}
}

func controlRequest(typ string, fields map[string]any) map[string]any {
	payload := map[string]any{
		"kind":                "scenery.assistant.control.request",
		"schema_revision":     assistantcontrol.RequestSchemaRevision,
		"type":                typ,
		"request_id":          "req-" + typ,
		"assistant_address":   "app/assistant/support",
		"runtime_revision":    "runtime-a",
		"capability_revision": "capability-a",
	}
	for key, value := range fields {
		payload[key] = value
	}
	return payload
}

func controlResponse(typ string, fields map[string]any) map[string]any {
	payload := map[string]any{
		"kind":                "scenery.assistant.control.response",
		"schema_revision":     assistantcontrol.ResponseSchemaRevision,
		"type":                typ,
		"request_id":          "req-response",
		"assistant_address":   "app/assistant/support",
		"runtime_revision":    "runtime-a",
		"capability_revision": "capability-a",
	}
	for key, value := range fields {
		payload[key] = value
	}
	return payload
}

func controlEvent(typ string, sequence int) map[string]any {
	return map[string]any{
		"kind":                "scenery.assistant.control.event",
		"schema_revision":     assistantcontrol.EventSchemaRevision,
		"type":                typ,
		"assistant_address":   "app/assistant/support",
		"runtime_revision":    "runtime-a",
		"capability_revision": "capability-a",
		"private_session_id":  "session-a",
		"continuation_token":  "continuation-a",
		"run_id":              "run-a",
		"sequence":            sequence,
		"occurred_at":         "2026-08-04T00:00:00Z",
		"data":                map[string]any{"text": "hello"},
	}
}
