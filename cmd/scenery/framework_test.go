package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkSelectionArgsAndExplicitModuleRewrite(t *testing.T) {
	options, err := parseFrameworkArgs([]string{"use", "--source", "../scenery", "--app-root", "/app", "-o", "json"})
	if err != nil || options.Command != "use" || options.Source != "../scenery" || !options.JSON {
		t.Fatalf("options=%+v err=%v", options, err)
	}
	if _, err := parseFrameworkArgs([]string{"inspect", "--source", "../scenery"}); err == nil {
		t.Fatal("read-only inspection accepted source selection")
	}
	root := t.TempDir()
	snapshot := filepath.Join(root, ".scenery", "framework", "source", "content")
	before := []byte("module example.test/app\n\ngo 1.27.0\nrequire scenery.sh v0.3.7\nreplace scenery.sh v0.3.7 => ../mutable\nreplace example.test/shared => ../shared\n")
	after, err := selectFrameworkSnapshotModule(before, root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"require scenery.sh v0.3.7", "replace scenery.sh => ./.scenery/framework/source/content", "replace example.test/shared => ../shared"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("missing %q in selected module:\n%s", want, after)
		}
	}
	if strings.Contains(string(after), "../mutable") || strings.Contains(string(after), "replace scenery.sh v0.3.7") {
		t.Fatalf("mutable version-scoped replacement survived selection:\n%s", after)
	}
	if _, err := selectFrameworkSnapshotModule(before, root, filepath.Dir(root)); err == nil {
		t.Fatal("framework snapshot escaped the app root")
	}
}
