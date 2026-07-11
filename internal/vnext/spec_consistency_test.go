package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangePlanSchemaRequiresNormalizedOperationIdentity(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "scenery.change-plan.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	definitions, _ := schema["$defs"].(map[string]any)
	operation, _ := definitions["operation"].(map[string]any)
	requiredValues, _ := operation["required"].([]any)
	required := map[string]bool{}
	for _, value := range requiredValues {
		required[stringValue(value)] = true
	}
	for _, field := range []string{"op", "address", "expected_kind", "expected_schema_revision", "view"} {
		if !required[field] {
			t.Errorf("normalized operation schema does not require %s", field)
		}
	}
	properties, _ := operation["properties"].(map[string]any)
	view, _ := properties["view"].(map[string]any)
	if view["const"] != "source" {
		t.Fatalf("normalized operation view schema = %#v", view)
	}
}

func TestUmbrellaProfileAndGoABISummariesMatchImplementedCompanions(t *testing.T) {
	umbrella := readVNextSpec(t, "SCENERY_LANGUAGE_SPEC.md")
	goCompanion := readVNextSpec(t, "SCENERY_GO_IMPLEMENTATION_V1.md")

	profileRows := markdownTableRows(markdownSection(umbrella, "## 26. Conformance profiles", "### 26.1 Profile rules"))
	for profile := range SupportedProfiles {
		row, ok := profileRows[profile]
		if !ok {
			t.Errorf("umbrella profile table omits %s", profile)
			continue
		}
		gotDependencies := []string{}
		if len(row) >= 3 && row[2] != "none" {
			for _, dependency := range strings.Split(row[2], ",") {
				gotDependencies = append(gotDependencies, strings.TrimSpace(dependency))
			}
		}
		if !semanticEqual(canonicalStrings(gotDependencies), canonicalStrings(ProfileDependencies[profile])) {
			t.Errorf("umbrella dependencies for %s = %#v, want %#v", profile, gotDependencies, ProfileDependencies[profile])
		}
	}
	if len(profileRows) != len(SupportedProfiles) {
		t.Errorf("umbrella profile rows = %d, supported profiles = %d", len(profileRows), len(SupportedProfiles))
	}

	umbrellaMappings := markdownTableRows(markdownSection(umbrella, "### 24.3 Scenery-to-Go types", "### 24.4 Service constructor and dependencies"))
	companionMappings := markdownTableRows(markdownSection(goCompanion, "## 9. Scenery-to-Go type mapping", "## 10. Service constructor input"))
	if !semanticEqual(umbrellaMappings, companionMappings) {
		t.Errorf("umbrella Go mappings drifted from companion:\numbrella=%#v\ncompanion=%#v", umbrellaMappings, companionMappings)
	}
	constructor := markdownSection(umbrella, "### 24.4 Service constructor and dependencies", "### 24.5 Lifecycle")
	if !strings.Contains(constructor, "housecontract.HouseConstructorInput") || strings.Contains(constructor, "housecontract.HouseDependencies") {
		t.Errorf("umbrella constructor summary is stale:\n%s", constructor)
	}
}

func readVNextSpec(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "specs", "vnext", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func markdownSection(document, start, end string) string {
	startIndex := strings.Index(document, start)
	if startIndex < 0 {
		return ""
	}
	section := document[startIndex+len(start):]
	if endIndex := strings.Index(section, end); endIndex >= 0 {
		section = section[:endIndex]
	}
	return section
}

func markdownTableRows(section string) map[string][]string {
	rows := map[string][]string{}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for index := range cells {
			cells[index] = strings.Trim(strings.TrimSpace(cells[index]), "`")
		}
		if len(cells) < 2 || cells[0] == "Profile" || cells[0] == "Scenery" {
			continue
		}
		rows[cells[0]] = cells
	}
	return rows
}
