package spec

import (
	"strings"
	"testing"
)

func TestDeclarativeTableResourceMetadataIsComplete(t *testing.T) {
	for _, kind := range []string{"scenery.crud", "scenery.react-component", "scenery.status-map", "scenery.form-dialog", "scenery.table-page", "scenery.split-page", "scenery.content-page"} {
		if !resourceCreateKindSupported(kind) {
			t.Fatalf("%s is not advertised as a creatable resource kind", kind)
		}
		blockType := strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
		schema, ok := authoredResourceSourceSchema(blockType)
		if !ok {
			t.Fatalf("%s source schema is unavailable", kind)
		}
		var incomplete []string
		var inspect func(*authoredBlockSchema)
		inspect = func(current *authoredBlockSchema) {
			for name, attribute := range current.Attributes {
				if attribute.MetadataStatus != "exact" || len(attribute.Type) == 0 || attribute.Phase == "" || attribute.RevisionDomain == "" {
					incomplete = append(incomplete, current.Revision+"."+name)
				}
			}
			for _, child := range current.Children {
				inspect(child.Schema)
			}
		}
		inspect(schema)
		if len(incomplete) > 0 {
			t.Fatalf("%s metadata incomplete: %v", kind, incomplete)
		}
	}

	table, _ := authoredResourceSourceSchema("table_page")
	if child, ok := table.Children["group"]; !ok || !child.Repeatable || child.Schema.Labels != 1 {
		t.Errorf("table_page group must be a repeated labeled block: %#v", child)
	}
	if child, ok := table.Children["pagination"]; !ok || child.Repeatable || child.Schema.Labels != 0 {
		t.Errorf("table_page pagination must be an unlabeled singleton block: %#v", child)
	} else {
		for _, name := range []string{"page", "page_size", "total"} {
			if !child.Schema.Required[name] {
				t.Errorf("table_page pagination must require %s", name)
			}
		}
	}
	if child, ok := table.Children["predicate"]; !ok || !child.Repeatable || child.Schema.Labels != 1 || !child.Schema.Required["value"] {
		t.Errorf("table_page predicate must be a repeated labeled block requiring value: %#v", child)
	} else if child.Schema.Attributes["value"].Type["$ref"] != "scenery.value" {
		t.Errorf("table_page predicate value must accept typed literals: %#v", child.Schema.Attributes["value"])
	}
	if child, ok := table.Children["query"]; !ok || child.Repeatable || child.Schema.Labels != 0 {
		t.Errorf("table_page query must be an unlabeled singleton block: %#v", child)
	}
	if _, ok := table.Children["filter"].Schema.Attributes["input"]; !ok {
		t.Error("table_page filter does not advertise explicit input mapping")
	}
	rowDetail := table.Children["row_detail"].Schema
	for _, name := range []string{"presentation", "panel_width"} {
		if _, ok := rowDetail.Attributes[name]; !ok {
			t.Errorf("table_page row_detail does not advertise %s", name)
		}
	}
	for _, name := range []string{"toolbar", "footer", "empty", "row_action"} {
		if child, ok := table.Children[name]; !ok || child.Repeatable || child.Schema.Labels != 0 {
			t.Errorf("table_page %s must be an unlabeled singleton block: %#v", name, child)
		} else if len(child.Schema.Attributes) != 1 || !child.Schema.Required["component"] {
			t.Errorf("table_page %s must use the component-only slot contract: %#v", name, child.Schema)
		}
	}

	split, _ := authoredResourceSourceSchema("split_page")
	for _, name := range []string{"sidebar", "detail", "sidebar_actions", "detail_header"} {
		if child, ok := split.Children[name]; !ok || child.Repeatable || child.Schema.Labels != 0 {
			t.Errorf("split_page %s must be an unlabeled singleton block: %#v", name, child)
		}
	}
	for _, legacy := range []string{"pane", "pane_actions"} {
		if _, ok := split.Children[legacy]; ok {
			t.Errorf("split_page still advertises legacy child %s", legacy)
		}
	}
	if _, ok := split.Attributes["sidebar_label"]; !ok {
		t.Error("split_page does not advertise sidebar_label")
	}
	if _, ok := split.Attributes["pane_label"]; ok {
		t.Error("split_page still advertises legacy pane_label")
	}

	content, _ := authoredResourceSourceSchema("content_page")
	for _, name := range []string{"content", "actions"} {
		if child, ok := content.Children[name]; !ok || child.Repeatable || child.Schema.Labels != 0 {
			t.Errorf("content_page %s must be an unlabeled singleton block: %#v", name, child)
		}
	}
	if _, ok := content.Attributes["max_width"]; !ok {
		t.Error("content_page does not advertise max_width")
	}
	if content.Required["source"] {
		t.Error("content_page still requires source")
	}
	page, ok := resourceSchemas["scenery.page"]
	if !ok {
		t.Fatal("scenery.page schema is unavailable")
	}
	for _, required := range page.Required {
		if required == "load" {
			t.Error("scenery.page still requires load")
		}
	}
	for name, schema := range map[string]*authoredBlockSchema{
		"table_page":   table,
		"split_page":   split,
		"content_page": content,
	} {
		search, ok := schema.Children["search"]
		if !ok || !search.Repeatable || search.Schema.Labels != 1 {
			t.Errorf("%s search must be a repeated labeled block: %#v", name, search)
		}
		for _, attribute := range []string{"nav_group", "nav_order", "nav_label", "nav_icon", "nav_active_paths"} {
			if _, ok := schema.Attributes[attribute]; !ok {
				t.Errorf("%s does not advertise %s", name, attribute)
			}
		}
	}
}

func TestCurrentCatalogUsesUnversionedKindsAndContentRevisions(t *testing.T) {
	catalog := Current()
	if len(catalog.ResourceSchemas) == 0 || len(catalog.StructuralSchemas) != 6 || len(catalog.DiagnosticRules) == 0 {
		t.Fatalf("incomplete current catalog: resources=%d structural=%d diagnostics=%d", len(catalog.ResourceSchemas), len(catalog.StructuralSchemas), len(catalog.DiagnosticRules))
	}
	for kind, schema := range catalog.ResourceSchemas {
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
	for name, schema := range catalog.StructuralSchemas {
		if revision, _ := schema["schema_revision"].(string); !canonicalDigest(revision) {
			t.Errorf("structural schema %s revision = %q", name, revision)
		}
	}
	semantics, err := MarshalCanonical(catalog.Semantics)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(semantics), "sha256:") != 8 {
		t.Fatalf("semantic revision gates = %s", semantics)
	}
}

func TestCurrentRevisionIsDeterministicCanonicalCatalogDigest(t *testing.T) {
	first := RevisionOf(Current())
	second := CurrentRevision()
	if first != second || !canonicalDigest(string(first)) {
		t.Fatalf("catalog revisions = %q and %q", first, second)
	}
}

func TestCatalogAccessorsDoNotExposeMutableSpecificationStorage(t *testing.T) {
	want := CurrentRevision()

	resources := ResourceSchemas()
	provider := resources["scenery.provider"]
	provider.Attributes[0] = "mutated"
	provider.CanonicalOnly["locked_integrity"] = "mutated"
	delete(resources, "scenery.record")

	structural := StructuralSourceSchemas()
	structural["workspace"].Revision = "mutated"
	delete(structural, "application")

	children := ResourceSourceChildren()
	children["record"]["field"].Schema.Revision = "mutated"
	dynamic := DynamicResourceRevisionDomains()
	delete(dynamic["scenery.service"], "config")
	overrides := AuthoredFieldOverrides()
	for key, override := range overrides {
		override.Constraints["mutated"] = true
		overrides[key] = override
		break
	}

	if got := CurrentRevision(); got != want {
		t.Fatalf("mutating exported catalog copies changed current revision: %s -> %s", want, got)
	}
	if ResourceSchemas()["scenery.provider"].Attributes[0] == "mutated" || StructuralSourceSchemas()["workspace"].Revision == "mutated" {
		t.Fatal("an exported schema accessor returned live specification storage")
	}
	if ResourceSourceChildren()["record"]["field"].Schema.Revision == "mutated" || DynamicResourceRevisionDomains()["scenery.service"]["config"].SchemaField == "" {
		t.Fatal("an exported nested schema accessor returned live specification storage")
	}
}

func TestDiagnosticExplanatoryTextIsSeparateFromSemanticRuleIdentity(t *testing.T) {
	encoded, err := MarshalCanonical(Current().DiagnosticRules)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "meaning") || strings.Contains(string(encoded), "documentation") {
		t.Fatalf("semantic diagnostic rules include explanatory prose: %s", encoded)
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
