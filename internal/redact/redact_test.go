package redact

import (
	"net/http"
	"strings"
	"testing"
)

func TestValueRedactsOnlavaSensitiveFields(t *testing.T) {
	type nested struct {
		Token string `json:"token" onlava:"sensitive"`
	}
	type payload struct {
		Password string `json:"password" onlava:"sensitive"`
		Nested   nested `json:"nested"`
		Name     string `json:"name"`
	}

	got := Value(payload{
		Password: "secret",
		Nested:   nested{Token: "abc"},
		Name:     "visible",
	}).(map[string]any)

	if got["password"] != Placeholder {
		t.Fatalf("password = %#v, want %q", got["password"], Placeholder)
	}
	nestedValue := got["nested"].(map[string]any)
	if nestedValue["token"] != Placeholder {
		t.Fatalf("token = %#v, want %q", nestedValue["token"], Placeholder)
	}
	if got["name"] != "visible" {
		t.Fatalf("name = %#v, want %q", got["name"], "visible")
	}
}

func TestHeadersRedactsSensitiveKeys(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer secret"},
		"X-Test":        []string{"ok"},
	}
	got := Headers(headers)
	if got["Authorization"] != Placeholder {
		t.Fatalf("Authorization = %q, want %q", got["Authorization"], Placeholder)
	}
	if got["X-Test"] != "ok" {
		t.Fatalf("X-Test = %q, want %q", got["X-Test"], "ok")
	}
}

func TestStringRedactsSensitiveAssignments(t *testing.T) {
	got := String("token=abc password:secret Authorization=Bearer123 ok=value")
	for _, wantGone := range []string{"abc", "secret", "Bearer123"} {
		if got == "" || got == wantGone || contains(got, wantGone) {
			t.Fatalf("String(...) leaked %q in %q", wantGone, got)
		}
	}
	if !contains(got, Placeholder) {
		t.Fatalf("String(...) = %q, want placeholder", got)
	}
}

func TestURLRedactsSensitiveQueryAndUserinfo(t *testing.T) {
	got, ok := URL("https://user:pass@example.com/path?token=abc&x=1")
	if !ok {
		t.Fatal("URL(...) should parse")
	}
	if contains(got, "pass") || contains(got, "abc") {
		t.Fatalf("URL(...) leaked secret in %q", got)
	}
	if got != "https://user:%5Bredacted%5D@example.com/path?token=%5Bredacted%5D&x=1" {
		t.Fatalf("URL(...) = %q", got)
	}
}

func contains(value, sub string) bool {
	return strings.Contains(value, sub)
}
