package graph

import (
	"strings"
	"testing"
	"time"
)

func TestListAtPagesWithBoundCursors(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract"}
	for _, name := range []string{"a", "b", "c"} {
		manifest.Resources = append(manifest.Resources, Resource{Address: "app/record/" + name, Kind: "scenery.record", Name: name, Module: "app"})
	}
	manifest.Resources = append(manifest.Resources, Resource{Address: "app/enum/mode", Kind: "scenery.enum", Name: "mode", Module: "app"})
	now := time.Unix(1_800_000_000, 0)
	options := ListOptions{Kind: "record", View: "effective", Limit: 2, Summary: true}
	first, err := ListAt(manifest, "sha256:workspace", options, now)
	if err != nil {
		t.Fatal(err)
	}
	summaries, ok := first.Resources.([]ResourceSummary)
	if !ok || len(summaries) != 2 || summaries[0].Address != "app/record/a" || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	options.Cursor = first.NextCursor
	second, err := ListAt(manifest, "sha256:workspace", options, now)
	if err != nil {
		t.Fatal(err)
	}
	if summaries, ok := second.Resources.([]ResourceSummary); !ok || len(summaries) != 1 || summaries[0].Address != "app/record/c" || second.Truncated || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
	for _, test := range []struct {
		name      string
		workspace string
		options   ListOptions
		now       time.Time
		want      string
	}{
		{"changed snapshot", "sha256:other", options, now, "snapshot is unavailable"},
		{"other query", "sha256:workspace", ListOptions{Kind: "enum", View: "effective", Limit: 2, Summary: true, Cursor: first.NextCursor}, now, "does not match the query"},
		{"expired", "sha256:workspace", options, now.Add(ContextTokenTTL + time.Second), "expired"},
		{"forged", "sha256:workspace", ListOptions{Kind: "record", View: "effective", Limit: 2, Summary: true, Cursor: "e30.AAAA"}, now, "invalid cursor"},
		{"limit", "sha256:workspace", ListOptions{Limit: AgentMaxResources + 1}, now, "limit must be between"},
	} {
		if _, err := ListAt(manifest, test.workspace, test.options, test.now); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: error = %v, want %q", test.name, err, test.want)
		}
	}
}
