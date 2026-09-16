package nativebuilddriver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAdoptionReplacesAnExistingArchiveWhoseContentDiffers(t *testing.T) {
	root := t.TempDir()
	recorded := filepath.Join(root, "recorded.a")
	if err := os.WriteFile(recorded, []byte("archive-A"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileDigest(recorded)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "store", "artifacts", strings.TrimPrefix(digest, "sha256:")+".a")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	// Different bytes of the same size under the content-addressed name.
	if err := os.WriteFile(target, []byte("archive-B"), 0o600); err != nil {
		t.Fatal(err)
	}
	adopted, err := adoptRetainedFile(recorded, target, RetainedFile{Digest: digest, Bytes: size})
	if err != nil {
		t.Fatal(err)
	}
	if actual, _, err := FileDigest(target); err != nil || actual != digest {
		t.Fatalf("adopted entry holds %s, want %s (%v)", actual, digest, err)
	}
	if err := validateRetainedFile(target, adopted); err != nil {
		t.Fatalf("replaced entry does not validate: %v", err)
	}

	// An entry that already holds the expected content is adopted in place.
	before, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	again, err := adoptRetainedFile(recorded, target, RetainedFile{Digest: digest, Bytes: size})
	if err != nil {
		t.Fatal(err)
	}
	if again.Stamp != fileStamp(before) {
		t.Fatal("an entry with the expected content was replaced")
	}
}

func TestRecordedActionsMustMatchTheCapturedInputs(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "workspace", "a")
	source := filepath.Join(directory, "a.go")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package a // A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	compiled, _, err := FileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "record", "tmp", "go-build1", "b001", "importcfg")
	capture := Capture{
		Files:    map[string]string{source: compiled},
		Packages: map[string]Package{"example/a": {ImportPath: "example/a", Dir: directory, GoFiles: []string{"a.go"}}},
	}
	compile := func(files ...FileCopy) ToolRecord {
		record := ToolRecord{Protocol: ProtocolVersion, Tool: filepath.Join(root, "compile"), Argv: []string{"-p", "example/a", "-importcfg", work}, Files: map[int]FileCopy{3: {Original: work, Digest: "sha256:importcfg"}}}
		for index, file := range files {
			record.Argv = append(record.Argv, file.Original)
			record.Files[4+index] = file
		}
		return record
	}
	selection := newCapturedSelection(capture)
	if err := selection.validate(compile(FileCopy{Original: source, Digest: compiled}), ""); err != nil {
		t.Fatalf("a recording of exactly the captured selection was rejected: %v", err)
	}
	// The workspace was edited after the tool compiled it and before the
	// capture that describes the recipe.
	if err := newCapturedSelection(Capture{Files: map[string]string{source: "sha256:edited"}, Packages: capture.Packages}).validate(compile(FileCopy{Original: source, Digest: compiled}), ""); err == nil || !strings.Contains(err.Error(), "differs from its captured content") {
		t.Fatalf("an archive compiled from other source was accepted: %v", err)
	}
	// A source file was added to the package, compiled, and removed again
	// before the capture was revalidated: every captured stamp and the
	// directory listing are unchanged, but the archive contains it.
	transient := FileCopy{Original: filepath.Join(directory, "transient.go"), Digest: "sha256:transient"}
	if err := selection.validate(compile(FileCopy{Original: source, Digest: compiled}, transient), ""); err == nil || !strings.Contains(err.Error(), "outside its captured source selection") {
		t.Fatalf("a transient source file entered the recording: %v", err)
	}
	// A file added under an embed pattern is read through the embed
	// configuration, not as an argument of its own.
	base := filepath.Join(directory, "assets", "base.txt")
	embedSelection := newCapturedSelection(Capture{Files: map[string]string{source: compiled, base: "sha256:base"}, Packages: capture.Packages})
	embedding := func(embedded ...string) ToolRecord {
		files := map[string]string{}
		for _, path := range embedded {
			files["assets/"+filepath.Base(path)] = path
		}
		encoded, err := json.Marshal(map[string]any{"Patterns": map[string][]string{"assets/*": nil}, "Files": files})
		if err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(root, "record", "tmp", "go-build1", "b001", "embedcfg")
		copied := filepath.Join(root, "record", "actions", "embed-"+strconv.Itoa(len(embedded)))
		if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(copied, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		record := compile(FileCopy{Original: source, Digest: compiled})
		record.Argv = append(record.Argv, "-embedcfg", config)
		record.Files[len(record.Argv)-1] = FileCopy{Original: config, Copy: copied, Digest: "sha256:embedcfg"}
		return record
	}
	if err := embedSelection.validate(embedding(base), ""); err != nil {
		t.Fatalf("a recording embedding exactly the captured files was rejected: %v", err)
	}
	if err := embedSelection.validate(embedding(base, filepath.Join(directory, "assets", "transient.txt")), ""); err == nil || !strings.Contains(err.Error(), "embedded a file outside") {
		t.Fatalf("a transient embedded file entered the recording: %v", err)
	}
	unselected := compile(FileCopy{Original: source, Digest: compiled})
	unselected.Argv[1] = "example/b"
	if err := selection.validate(unselected, ""); err == nil {
		t.Fatal("a compile of an unselected package was accepted")
	}

	recordRoot := filepath.Join(root, "record")
	action := filepath.Join(recordRoot, "actions", "action-1")
	if err := os.MkdirAll(action, 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(compile(FileCopy{Original: source, Digest: compiled}, transient))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(action, "record.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	recipe := &Recipe{Compiles: map[string]*CompileAction{}, ArchiveByOld: map[string]string{}, ToolDigests: map[string]string{}, Retained: map[string]RetainedFile{}, Support: map[string]RetainedFile{}}
	if _, err := mergeRecordedActions(recipe, recordRoot, capture); err == nil {
		t.Fatal("merging a recording that compiled a transient source file succeeded")
	}
}

func TestWorkspaceMembersMatchCanonicalMembership(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{filepath.Join(workspace, "pkg"), outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A link into the workspace and a link out of it resolve by directory.
	if err := os.Symlink(filepath.Join(workspace, "pkg"), filepath.Join(root, "into")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "out")); err != nil {
		t.Fatal(err)
	}
	members := newWorkspaceMembers(workspace)
	for _, path := range []string{
		filepath.Join(workspace, "pkg", "a.go"),
		filepath.Join(root, "into", "a.go"),
		filepath.Join(workspace, "out", "a.go"),
		filepath.Join(outside, "a.go"),
		filepath.Join(workspace, "go.mod"),
	} {
		if err := os.WriteFile(path, nil, 0o600); err != nil && !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
		if got, want := members.regularFile(path), withinWorkspace(workspace, path); got != want {
			t.Errorf("membership of %s = %t, want %t", path, got, want)
		}
	}
}
