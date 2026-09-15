package nativebuilddriver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInputStateAdvancesSnapshotsWithoutRehashingUnchangedBytes(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workspace, "service.go")
	if err := os.WriteFile(source, []byte("package service\nfunc Value() string { return \"a\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digestA, _, err := FileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	capture := Capture{
		Protocol: ProtocolVersion, Workspace: workspace, Digest: "sha256:a", GoVersion: "go fixture", GoToolDigest: "sha256:go",
		Packages: map[string]Package{"example/service": {ImportPath: "example/service", Name: "service", Dir: workspace, GoFiles: []string{"service.go"}}},
		Files:    map[string]string{source: digestA}, Syntax: map[string]string{source: "sha256:syntax"}, SnapshotFiles: map[string]string{source: source},
		FileStamps: map[string]FileStamp{}, Directories: map[string]string{}, Environment: map[string]string{}, RequestEnv: map[string]string{},
	}
	root := filepath.Join(t.TempDir(), "retained")
	state, first, err := NewInputState(capture, root)
	if err != nil {
		t.Fatal(err)
	}
	if first.FilesHashed != 1 || first.BytesHashed == 0 {
		t.Fatalf("first commit stats = %+v", first)
	}
	unchanged, reused, err := state.Advance(state.Current, root)
	if err != nil {
		t.Fatal(err)
	}
	if reused.FilesHashed != 0 || reused.BytesHashed != 0 || reused.FilesReused != 1 || reused.BytesReused == 0 {
		t.Fatalf("unchanged commit stats = %+v", reused)
	}

	changed := cloneCaptureValue(unchanged.Current)
	changedSource := filepath.Join(t.TempDir(), "service.go")
	if err := os.WriteFile(changedSource, []byte("package service\nfunc Value() string { return \"b\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digestB, _, err := FileDigest(changedSource)
	if err != nil {
		t.Fatal(err)
	}
	changed.Digest = "sha256:b"
	changed.Files[source] = digestB
	changed.SnapshotFiles[source] = changedSource
	if packages, reason := unchanged.CheckEligibility(changed); reason != "" || !reflect.DeepEqual(packages, []string{"example/service"}) {
		t.Fatalf("changed eligibility packages=%v reason=%q", packages, reason)
	}
	next, committed, err := unchanged.Advance(changed, root)
	if err != nil {
		t.Fatal(err)
	}
	if committed.FilesHashed != 1 || committed.BytesHashed == 0 || next.Current.Files[source] != digestB {
		t.Fatalf("changed commit state=%+v stats=%+v", next.Current, committed)
	}
}

func TestCaptureValidateCurrentStampsRejectsRestoredInput(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.go")
	contents := []byte("package input\nconst Value = \"A\"\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileStamp(info).ChangeTimeNano == 0 {
		t.Skip("platform does not expose a change timestamp")
	}
	capture := Capture{FileStamps: map[string]FileStamp{path: fileStamp(info)}}
	if err := capture.ValidateCurrentStamps(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package input\nconst Value = \"B\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileStamp(after).ChangeTimeNano == fileStamp(info).ChangeTimeNano {
		time.Sleep(time.Millisecond)
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	if err := capture.ValidateCurrentStamps(); err == nil {
		t.Fatal("restored input retained its stale capture")
	}
}
