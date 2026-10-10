package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseWorktreeArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseWorktreeArgs([]string{"create", "pricing-agent", "--from", "main", "--app-root", "/tmp/app", "-o", "json"})
	if err != nil {
		t.Fatalf("parseWorktreeArgs returned error: %v", err)
	}
	if opts.Command != "create" || opts.Name != "pricing-agent" || opts.From != "main" || opts.AppRoot != "/tmp/app" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseWorktreeArgs([]string{"create"}); err == nil || !strings.Contains(err.Error(), "requires <name>") {
		t.Fatalf("missing name error = %v", err)
	}
}

func TestParseGitWorktrees(t *testing.T) {
	t.Parallel()

	const unusualPath = "/tmp/space tab\tquote\"žluťoučký\r\nbranch refs/heads/fake "
	for _, tc := range []struct {
		name   string
		output string
		want   []worktreeRecord
	}{
		{name: "empty"},
		{
			name: "records and exact values",
			output: "HEAD ignored\x00branch ignored\x00\x00worktree " + unusualPath + "\x00HEAD a\x00branch refs/heads/main\x00locked reason\nbranch fake\x00\x00\x00" +
				"worktree /tmp/detached\x00HEAD b\x00detached\x00prunable reason\x00\x00worktree /tmp/bare\x00bare\x00\x00",
			want: []worktreeRecord{{Path: unusualPath, Head: "a", Branch: "main"}, {Path: "/tmp/detached", Head: "b"}, {Path: "/tmp/bare", Bare: true}},
		},
		{
			name:   "next record without empty attribute",
			output: "worktree /tmp/first\x00branch custom/ref\x00worktree /tmp/second\x00HEAD c\x00",
			want:   []worktreeRecord{{Path: "/tmp/first", Branch: "custom/ref"}, {Path: "/tmp/second", Head: "c"}},
		},
		{
			name:   "final attribute terminator",
			output: "worktree /tmp/final\x00HEAD d\x00",
			want:   []worktreeRecord{{Path: "/tmp/final", Head: "d"}},
		},
		{
			name:   "unterminated final attribute",
			output: "worktree /tmp/final\x00HEAD d",
			want:   []worktreeRecord{{Path: "/tmp/final", Head: "d"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGitWorktrees(tc.output)
			if !slices.Equal(got, tc.want) || (tc.want == nil && got != nil) {
				t.Fatalf("records = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestWorktreeCreateListAndRemoveWithoutDBPinInProcess(t *testing.T) {
	t.Parallel()

	root := filepath.Join(canonicalTestDir(t), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)
	worktrees := []worktreeRecord{{Path: root, Branch: "main", Head: strings.Repeat("a", 40)}}
	runGit := func(_ context.Context, args ...string) error {
		if len(args) >= 7 && args[0] == "-C" && args[1] == root && args[2] == "worktree" && args[3] == "add" && args[4] == "-b" {
			name, target := args[5], args[6]
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			worktrees = append(worktrees, worktreeRecord{Path: target, Branch: name, Head: strings.Repeat("b", 40)})
			return nil
		}
		if len(args) == 5 && args[0] == "-C" && args[1] == root && args[2] == "worktree" && args[3] == "remove" {
			target := args[4]
			worktrees = slices.DeleteFunc(worktrees, func(record worktreeRecord) bool { return record.Path == target })
			return os.RemoveAll(target)
		}
		return fmt.Errorf("unexpected Git arguments: %#v", args)
	}
	listWorktrees := func(_ context.Context, gotRoot string) ([]worktreeRecord, error) {
		if gotRoot != root {
			t.Fatalf("list root = %q, want %q", gotRoot, root)
		}
		return slices.Clone(worktrees), nil
	}
	run := func(output *bytes.Buffer, args []string) error {
		return runWorktreeCommandWithGit(t.Context(), output, args, runGit, listWorktrees)
	}

	var createAOut bytes.Buffer
	if err := run(&createAOut, []string{"create", "pricing-agent", "--from", "main", "--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("runWorktreeCommand create A returned error: %v", err)
	}
	var createdA worktreeCreateResult
	if err := decodeCLIJSON(createAOut.Bytes(), &createdA); err != nil {
		t.Fatalf("decode create A JSON: %v\n%s", err, createAOut.String())
	}
	if !createdA.OK {
		t.Fatalf("created A = %+v", createdA)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.create.schema.json"), createdA); len(diagnostics) != 0 {
		t.Fatalf("create A schema diagnostics = %+v", diagnostics)
	}
	if _, err := os.Stat(filepath.Join(createdA.Path, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
		t.Fatalf("target A pin exists err=%v", err)
	}

	var createBOut bytes.Buffer
	if err := run(&createBOut, []string{"create", "content-agent", "--from", "main", "--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("runWorktreeCommand create B returned error: %v", err)
	}
	var createdB worktreeCreateResult
	if err := decodeCLIJSON(createBOut.Bytes(), &createdB); err != nil {
		t.Fatalf("decode create B JSON: %v\n%s", err, createBOut.String())
	}
	if !createdB.OK {
		t.Fatalf("created B = %+v", createdB)
	}
	if createdA.Path == createdB.Path {
		t.Fatalf("created worktrees are not isolated: A=%+v B=%+v", createdA, createdB)
	}
	if _, err := os.Stat(filepath.Join(createdB.Path, ".scenery", "worktree-db.json")); !os.IsNotExist(err) {
		t.Fatalf("target B pin exists err=%v", err)
	}
	var listOut bytes.Buffer
	if err := run(&listOut, []string{"list", "--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("runWorktreeCommand list returned error: %v", err)
	}
	var listed worktreeListResult
	if err := decodeCLIJSON(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, listOut.String())
	}
	found := map[string]bool{}
	for _, wt := range listed.Worktrees {
		if evalPathForTest(t, wt.Path) == evalPathForTest(t, createdA.Path) && wt.Branch == "pricing-agent" {
			found["pricing-agent"] = true
		}
		if evalPathForTest(t, wt.Path) == evalPathForTest(t, createdB.Path) && wt.Branch == "content-agent" {
			found["content-agent"] = true
		}
	}
	if !found["pricing-agent"] || !found["content-agent"] {
		t.Fatalf("created worktrees not listed: %+v", listed.Worktrees)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.list.schema.json"), listed); len(diagnostics) != 0 {
		t.Fatalf("list schema diagnostics = %+v", diagnostics)
	}

	for _, name := range []string{"pricing-agent", "content-agent"} {
		var removeOut bytes.Buffer
		if err := run(&removeOut, []string{"remove", name, "--app-root", root, "-o", "json"}); err != nil {
			t.Fatalf("runWorktreeCommand remove %s returned error: %v", name, err)
		}
		var removed worktreeRemoveResult
		if err := decodeCLIJSON(removeOut.Bytes(), &removed); err != nil {
			t.Fatalf("decode remove %s JSON: %v\n%s", name, err, removeOut.String())
		}
		if !removed.OK {
			t.Fatalf("removed %s = %+v", name, removed)
		}
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.worktree.remove.schema.json"), removed); len(diagnostics) != 0 {
			t.Fatalf("remove %s schema diagnostics = %+v", name, diagnostics)
		}
	}
	if _, err := os.Stat(createdA.Path); !os.IsNotExist(err) {
		t.Fatalf("target A path after remove err=%v", err)
	}
	if _, err := os.Stat(createdB.Path); !os.IsNotExist(err) {
		t.Fatalf("target B path after remove err=%v", err)
	}
}

func TestWorktreeRemoveRestoresDBStateWhenGitRemoveFailsInProcess(t *testing.T) {
	t.Parallel()

	root := filepath.Join(canonicalTestDir(t), "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)
	target := defaultWorktreePath(root, "dirty-agent")
	const state = `{"database":"dirty-agent","sentinel":true}`
	writeTestAppFile(t, target, ".scenery/worktree-db.json", state)
	wantErr := errors.New("git refuses dirty worktree")
	err := runWorktreeRemoveWithGit(t.Context(), &bytes.Buffer{}, worktreeOptions{Name: "dirty-agent", AppRoot: root, JSON: true}, func(_ context.Context, gotRoot string) ([]worktreeRecord, error) {
		if gotRoot != root {
			t.Fatalf("list root = %q, want %q", gotRoot, root)
		}
		return []worktreeRecord{{Path: root, Branch: "main"}, {Path: target, Branch: "dirty-agent"}}, nil
	}, func(_ context.Context, args ...string) error {
		want := []string{"-C", root, "worktree", "remove", target}
		if !slices.Equal(args, want) {
			t.Fatalf("Git remove arguments = %#v, want %#v", args, want)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("remove error = %v, want Git failure", err)
	}
	data, readErr := os.ReadFile(filepath.Join(target, ".scenery", "worktree-db.json"))
	if readErr != nil || string(data) != state {
		t.Fatalf("database state after failed remove = %q, %v", data, readErr)
	}
}

func TestWorktreeRemoveDoesNotDeleteStateForUnlistedTarget(t *testing.T) {
	t.Parallel()

	root := filepath.Join(canonicalTestDir(t), "demo")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)

	unlisted := defaultWorktreePath(root, "mistyped")
	writeTestAppFile(t, unlisted, ".scenery/worktree-db.json", `{"sentinel":true}`)
	err := runWorktreeRemoveWithList(t.Context(), &bytes.Buffer{}, worktreeOptions{
		Name:    "mistyped",
		AppRoot: root,
		JSON:    true,
	}, func(_ context.Context, gotRoot string) ([]worktreeRecord, error) {
		if gotRoot != root {
			t.Fatalf("list root = %q, want %q", gotRoot, root)
		}
		return []worktreeRecord{{Path: root, Branch: "main"}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("remove error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(unlisted, ".scenery", "worktree-db.json")); err != nil {
		t.Fatalf("unlisted target state was removed: %v", err)
	}
}

func TestWorktreeRemoveRequiresUniqueRegisteredTarget(t *testing.T) {
	t.Parallel()

	root := filepath.Join(canonicalTestDir(t), "demo")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)
	defaultPath := defaultWorktreePath(root, "target")
	branchPath := filepath.Join(filepath.Dir(root), "by-branch")
	basenamePath := filepath.Join(filepath.Dir(root), "target")
	defaultMatch := worktreeRecord{Path: defaultPath, Branch: "other"}
	branchMatch := worktreeRecord{Path: branchPath, Branch: "refs/heads/target"}
	listFailure := errors.New("Git inventory unavailable")
	ambiguity := fmt.Sprintf("git worktree %q is ambiguous; matching paths: %q", "target", []string{branchPath, defaultPath})
	for _, tc := range []struct {
		name      string
		worktrees []worktreeRecord
		listErr   error
		wantPath  string
		wantError string
		ambiguous bool
	}{
		{name: "default path", worktrees: []worktreeRecord{defaultMatch}, wantPath: defaultPath},
		{name: "branch", worktrees: []worktreeRecord{branchMatch}, wantPath: branchPath},
		{name: "basename", worktrees: []worktreeRecord{{Path: basenamePath, Branch: "other"}}, wantPath: basenamePath},
		{name: "one path matches multiple rules", worktrees: []worktreeRecord{{Path: defaultPath, Branch: "target"}}, wantPath: defaultPath},
		{name: "duplicate normalized path", worktrees: []worktreeRecord{branchMatch, {Path: branchPath + "/.", Branch: "target"}}, wantPath: branchPath},
		{name: "ambiguous", worktrees: []worktreeRecord{branchMatch, defaultMatch}, wantError: ambiguity, ambiguous: true},
		{name: "ambiguous reversed", worktrees: []worktreeRecord{defaultMatch, branchMatch}, wantError: ambiguity, ambiguous: true},
		{name: "unregistered", worktrees: []worktreeRecord{{Path: root, Branch: "main"}}, wantError: `git worktree "target" is not registered`},
		{name: "inventory failure", listErr: listFailure, wantError: listFailure.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			calls := 0
			err := runWorktreeRemoveWithGit(t.Context(), &output, worktreeOptions{Name: "target", AppRoot: root, JSON: true},
				func(_ context.Context, gotRoot string) ([]worktreeRecord, error) {
					if gotRoot != root {
						t.Fatalf("inventory root = %q, want %q", gotRoot, root)
					}
					return tc.worktrees, tc.listErr
				}, func(_ context.Context, args ...string) error {
					calls++
					want := []string{"-C", root, "worktree", "remove", tc.wantPath}
					if tc.wantPath == "" || !slices.Equal(args, want) {
						t.Fatalf("unexpected mutation: %#v, want %#v", args, want)
					}
					return nil
				})
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError || calls != 0 || output.Len() != 0 {
					t.Fatalf("refusal = %v, mutations = %d, output = %s", err, calls, &output)
				}
				if tc.ambiguous && (cliExitCode(err) != 3 || cliErrorDiagnostic(err).Code != "SCN8003") {
					t.Fatalf("ambiguity is not a precondition: %v", err)
				}
				if tc.listErr != nil && !errors.Is(err, listFailure) {
					t.Fatalf("inventory error was lost: %v", err)
				}
				return
			}
			var removed worktreeRemoveResult
			if err != nil || calls != 1 {
				t.Fatalf("unique removal = %v, mutations = %d", err, calls)
			}
			if err := decodeCLIJSON(output.Bytes(), &removed); err != nil || !removed.OK || removed.Path != tc.wantPath {
				t.Fatalf("unique result = %+v, decode error = %v", removed, err)
			}
		})
	}
}

func evalPathForTest(t *testing.T, path string) string {
	t.Helper()
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks %s: %v", path, err)
	}
	return evaluated
}
