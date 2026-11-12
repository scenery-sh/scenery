package build

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetainedBuildInputGraphIsReadOnlyByTheSameToolIdentity(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	state := &retainedBuildInputGraph{
		Key: "process-key", Output: []byte(`{"ImportPath":"example.test/app"}`),
		Directories: map[string]buildInputFileStamp{"/src/app": {Size: 1, Inode: 7}},
		Selection:   map[string]retainedBuildInputSelection{"/src/app/a.go": {Stamp: buildInputFileStamp{Size: 2}, Digest: "digest"}},
	}
	saveRetainedBuildInputGraph(workspace, "tool", state)
	loaded := loadRetainedBuildInputGraph(workspace, "tool", "other-process-key")
	if loaded == nil || loaded.Key != "other-process-key" || string(loaded.Output) != string(state.Output) ||
		loaded.Directories["/src/app"] != state.Directories["/src/app"] || loaded.Selection["/src/app/a.go"] != state.Selection["/src/app/a.go"] {
		t.Fatalf("loaded graph = %#v", loaded)
	}
	if loadRetainedBuildInputGraph(workspace, "other-tool", "key") != nil {
		t.Fatal("a graph of another tool identity was trusted")
	}
	if loadRetainedBuildInputGraph(workspace, "", "key") != nil || loadRetainedBuildInputGraph("relative", "tool", "key") != nil {
		t.Fatal("a graph was loaded without a tool identity or an absolute workspace")
	}
	path := filepath.Join(workspace, buildInputGraphFile)
	for name, contents := range map[string]string{"truncated": `{"Version":1,"ToolKey":"tool","Graph":{"Out`, "another version": `{"Version":2,"ToolKey":"tool","Graph":{"Output":"e30=","Directories":{},"Selection":{}}}`, "no listing": `{"Version":1,"ToolKey":"tool","Graph":{"Directories":{},"Selection":{}}}`} {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if loadRetainedBuildInputGraph(workspace, "tool", "key") != nil {
			t.Fatalf("a %s document was trusted", name)
		}
	}
	saveRetainedBuildInputGraph(workspace, "", state)
	if loadRetainedBuildInputGraph(workspace, "tool", "key") != nil {
		t.Fatal("saving without a tool identity replaced the document")
	}
}

func TestGoToolIdentityFollowsTheSelectedGoCommand(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	for _, directory := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(directory, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := func(search string, extra ...string) []string {
		return append([]string{"PATH=" + search, "GOFLAGS=-mod=mod", "SESSION_TOKEN=volatile"}, extra...)
	}
	args := []string{"list", "-deps", "./..."}
	key := goToolIdentityKey(args, env(first))
	if key == "" || key != goToolIdentityKey(args, env(first, "SESSION_TOKEN=other", "UNRELATED=1")) {
		t.Fatal("a variable the Go tool does not read changed the identity")
	}
	changed := map[string]string{
		"arguments":   goToolIdentityKey([]string{"list", "-deps", "./cmd/..."}, env(first)),
		"go variable": goToolIdentityKey(args, []string{"PATH=" + first, "GOFLAGS=-mod=vendor"}),
		"go command":  goToolIdentityKey(args, env(second+string(os.PathListSeparator)+first)),
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(first, "go"), later, later); err != nil {
		t.Fatal(err)
	}
	changed["replaced go command"] = goToolIdentityKey(args, env(first))
	for name, other := range changed {
		if other == "" || other == key {
			t.Errorf("a changed %s kept the identity", name)
		}
	}
	if goToolIdentityKey(args, []string{"PATH=" + t.TempDir()}) != "" {
		t.Fatal("an unresolved go command has an identity")
	}
}
