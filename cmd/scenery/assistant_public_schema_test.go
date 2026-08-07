package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/assistantapi"
)

func TestAssistantPublicSchemasValidateExactProviderNeutralShapes(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	valid := map[string]any{
		"scenery.assistant.public.request.schema.json": map[string]any{
			"message": map[string]any{"role": "user", "content": "Hello"},
		},
		"scenery.assistant.public.response.schema.json": map[string]any{
			"conversation_id": "conv1_0123456789abcdef",
			"run_id":          "run_0123456789abcdef",
			"events_url":      "/assistants/support/v1/conversations/conv1_0123456789abcdef/events",
		},
		"scenery.assistant.public-event.schema.json": map[string]any{
			"type":            "assistant.message.delta",
			"assistant":       "support",
			"conversation_id": "conv1_0123456789abcdef",
			"run_id":          "run_0123456789abcdef",
			"sequence":        1,
			"occurred_at":     "2026-08-04T00:00:00Z",
			"data":            map[string]any{"text": "Hello"},
		},
		"scenery.assistant.public-error.schema.json": map[string]any{
			"code":    "unavailable",
			"message": "assistant runtime unavailable",
		},
	}

	for name, payload := range valid {
		name, payload := name, payload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			schemaPath := filepath.Join(root, "docs", "schemas", name)
			if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
				t.Fatalf("valid payload diagnostics = %v", diagnostics)
			}
			assertAssistantSchemaHasNoProviderSignatures(t, schemaPath)
		})
	}
}

func TestAssistantPublicEventSchemaHasOnlyPublicEnvelopeFields(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	schemaPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.public-event.schema.json")
	encoded, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"assistant", "conversation_id", "data", "occurred_at", "run_id", "sequence", "type"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public event properties = %v, want %v", got, want)
	}

	valid := map[string]any{
		"type":            "assistant.run.started",
		"assistant":       "support",
		"conversation_id": "conv1_0123456789abcdef",
		"run_id":          "run_0123456789abcdef",
		"sequence":        1,
		"occurred_at":     "2026-08-04T00:00:00Z",
		"data":            map[string]any{},
	}
	valid["kind"] = "scenery.assistant.control.event"
	valid["schema_revision"] = "private"
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, valid); len(diagnostics) == 0 {
		t.Fatal("public event accepted private envelope fields")
	}
}

func TestAssistantPublicRequestAndResponseSchemasRejectUnknownBodies(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	requestPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.public.request.schema.json")
	request := map[string]any{
		"message":            map[string]any{"role": "user", "content": "Hello"},
		"private_session_id": "session-1",
	}
	if diagnostics := validateHarnessJSONSchemaFile(requestPath, request); len(diagnostics) == 0 {
		t.Fatal("public request accepted an unknown private field")
	}

	responsePath := filepath.Join(root, "docs", "schemas", "scenery.assistant.public.response.schema.json")
	response := map[string]any{
		"run_id":             "run_0123456789abcdef",
		"private_session_id": "session-1",
	}
	if diagnostics := validateHarnessJSONSchemaFile(responsePath, response); len(diagnostics) == 0 {
		t.Fatal("public response accepted an unknown private field")
	}
}

func TestAssistantPublicResponseSchemaValidatesApprovalAndCancellation(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	schemaPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.public.response.schema.json")
	for name, payload := range map[string]any{
		"approval": map[string]any{
			"approval_id": "appr1_0123456789abcdef",
			"decision":    "approve",
		},
		"cancellation": map[string]any{
			"run_id": "run_0123456789abcdef",
			"state":  "cancelled",
		},
	} {
		if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
			t.Fatalf("valid %s response diagnostics = %v", name, diagnostics)
		}
	}
	for name, payload := range map[string]any{
		"approval": map[string]any{
			"approval_id": "approval-1",
			"decision":    "approve",
		},
		"cancellation": map[string]any{
			"run_id": "run_0123456789abcdef",
			"state":  "stopped",
		},
	} {
		if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) == 0 {
			t.Fatalf("invalid %s response was accepted", name)
		}
	}
	errorPath := filepath.Join(root, "docs", "schemas", "scenery.assistant.public-error.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(errorPath, map[string]any{
		"code":    "internal",
		"message": strings.Repeat("x", 8193),
	}); len(diagnostics) == 0 {
		t.Fatal("public error accepted a message larger than 8192 characters")
	}
}

func TestAssistantPublicEventDataLimitMatchesAssistantAPI(t *testing.T) {
	t.Parallel()

	data := append([]byte(`{"text":"`), bytes.Repeat([]byte("x"), assistantapi.MaxEventDataBytes)...)
	data = append(data, []byte(`"}`)...)
	event := assistantapi.Event{
		Type:           assistantapi.EventMessageDelta,
		Assistant:      "support",
		ConversationID: "conv1_0123456789abcdef",
		RunID:          "run_0123456789abcdef",
		Sequence:       1,
		OccurredAt:     time.Unix(1_700_000_000, 0).UTC(),
		Data:           data,
	}
	if err := event.Validate(); err == nil {
		t.Fatal("assistant event accepted data larger than MaxEventDataBytes")
	}
}

func assertAssistantSchemaHasNoProviderSignatures(t *testing.T, schemaPath string) {
	t.Helper()
	encoded, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, signature := range []string{
		`(?i)(^|[^a-z0-9])eve([^a-z0-9]|$)`,
		`(?i)/eve/v1`,
		`(?i)eve[_-]`,
		`(?i)node_modules/eve`,
		`(?i)from "eve`,
		`(?i)@vercel/connect/eve`,
	} {
		if regexp.MustCompile(signature).MatchString(text) {
			t.Fatalf("schema contains forbidden provider signature %q", signature)
		}
	}
}
