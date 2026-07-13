package spec

import (
	"strings"
	"testing"
)

func TestCurrentCatalogUsesUnversionedKindsAndContentRevisions(t *testing.T) {
	catalog := Current()
	if len(catalog.Resources) == 0 || len(catalog.Diagnostics) == 0 {
		t.Fatalf("incomplete current catalog: resources=%d diagnostics=%d", len(catalog.Resources), len(catalog.Diagnostics))
	}
	for kind, schema := range catalog.Resources {
		if !strings.HasPrefix(string(kind), "scenery.") || strings.Contains(string(kind), "/") {
			t.Errorf("resource kind %q is not an unversioned logical kind", kind)
		}
		if schema["kind"] != string(kind) {
			t.Errorf("schema kind = %#v, want %q", schema["kind"], kind)
		}
		for _, field := range []string{"schema_revision", "source_schema_revision"} {
			if revision, _ := schema[field].(string); !canonicalDigest(revision) {
				t.Errorf("%s %s = %q", kind, field, revision)
			}
		}
	}
}

func TestCurrentRevisionIsDeterministicCanonicalCatalogDigest(t *testing.T) {
	first := RevisionOf(Current())
	second := CurrentRevision()
	if first != second || !canonicalDigest(string(first)) {
		t.Fatalf("catalog revisions = %q and %q", first, second)
	}
}

func TestSourceSchemaRevisionsIdentifyConcreteContent(t *testing.T) {
	operation, ok := ResourceSourceSchema("operation")
	if !ok {
		t.Fatal("operation source schema is unavailable")
	}
	revision := SourceSchemaRevision(operation)
	if !canonicalDigest(string(revision)) {
		t.Fatalf("source schema revision = %q", revision)
	}
	public, ok := AuthoredPublicSchema(string(revision))
	if !ok || public["schema_revision"] != string(revision) {
		t.Fatalf("public source schema = %#v", public)
	}
}

func canonicalDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
