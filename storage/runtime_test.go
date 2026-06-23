package storage

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/storageconfig"
)

func TestDefaultUsesRuntimeConfigEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, `{
		"schema_version": "`+storageconfig.RuntimeSchemaVersion+`",
		"default": "app",
		"stores": {
			"app": {"kind": "local", "root": `+quoteJSON(filepath.Join(root, "app"))+`}
		}
	}`)
	store, err := Default(context.Background())
	if err != nil {
		t.Fatalf("Default returned error: %v", err)
	}
	obj, err := store.Put(context.Background(), "docs/a.txt", strings.NewReader("alpha"), PutOptions{})
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if obj.Key != "docs/a.txt" || obj.SizeBytes != 5 {
		t.Fatalf("object = %+v", obj)
	}
	body, got, err := store.Get(context.Background(), "docs/a.txt", GetOptions{})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "alpha" || got.Key != "docs/a.txt" {
		t.Fatalf("data = %q object = %+v", data, got)
	}
}

func TestDefaultWithoutRuntimeConfigReturnsNotConfigured(t *testing.T) {
	t.Setenv(storageconfig.RuntimeConfigEnv, "")
	if _, err := Default(context.Background()); err == nil {
		t.Fatal("Default returned nil error")
	} else if _, ok := err.(*NotConfiguredError); !ok {
		t.Fatalf("Default error = %T %[1]v, want NotConfiguredError", err)
	}
}

func quoteJSON(value string) string {
	out := strings.Builder{}
	out.WriteByte('"')
	for _, r := range value {
		if r == '\\' || r == '"' {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}
