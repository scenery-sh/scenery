package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/filemeta"
)

const worktreePrepareKind = "scenery.worktree.prepare"

// worktreePreparedInput is one declared input a linked checkout received from
// the checkout it was created from.
type worktreePreparedInput struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

type worktreePrepareResult struct {
	cliPayloadIdentity
	OK      bool                    `json:"ok"`
	Path    string                  `json:"path"`
	Source  string                  `json:"source,omitempty"`
	Inputs  []worktreePreparedInput `json:"inputs"`
	Message string                  `json:"message,omitempty"`
}

func runWorktreePrepare(ctx context.Context, stdout io.Writer, opts worktreeOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	source, err := mainWorktreeAppRoot(ctx, appRoot)
	if err != nil {
		return err
	}
	result := worktreePrepareResult{cliPayloadIdentity: newCLIPayloadIdentity(worktreePrepareKind), OK: true, Path: appRoot, Source: source, Inputs: []worktreePreparedInput{}}
	if source == "" {
		result.Message = "This is the main checkout; it has nothing to receive."
	} else {
		result.Inputs, err = prepareWorktreeInputs(ctx, appRoot, source, cfg, gitIgnores)
		if err != nil {
			return err
		}
		result.Message = fmt.Sprintf("Received %d declared Git-ignored input(s) from the main checkout.", len(result.Inputs))
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	for _, input := range result.Inputs {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", input.Method, input.Path)
	}
	_, _ = fmt.Fprintln(stdout, result.Message)
	return nil
}

// mainWorktreeAppRoot returns the application root inside the repository's
// main checkout that corresponds to appRoot, or "" when appRoot is in the
// main checkout itself.
func mainWorktreeAppRoot(ctx context.Context, appRoot string) (string, error) {
	top, err := gitCommandOutput(ctx, appRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	worktrees, err := listGitWorktrees(ctx, appRoot)
	if err != nil {
		return "", err
	}
	if len(worktrees) == 0 || worktrees[0].Bare {
		return "", nil
	}
	return correspondingAppRoot(strings.TrimRight(top, "\r\n"), worktrees[0].Path, appRoot)
}

// gitTopLevel returns the root of the checkout holding appRoot, or appRoot
// itself when Git cannot say.
func gitTopLevel(ctx context.Context, appRoot string) string {
	top, err := gitCommandOutput(ctx, appRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return appRoot
	}
	return strings.TrimRight(top, "\r\n")
}

// correspondingAppRoot maps appRoot, which lies in the checkout at top, to
// the same relative location in another checkout of that repository.
func correspondingAppRoot(top, other, appRoot string) (string, error) {
	paths := []*string{&top, &other, &appRoot}
	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(*path)
		if err != nil {
			return "", err
		}
		*path = resolved
	}
	if top == other {
		return "", nil
	}
	relative, err := filepath.Rel(top, appRoot)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", preconditionErrorf("app root %s is outside its Git checkout %s", appRoot, top)
	}
	return filepath.Join(other, relative), nil
}

// prepareWorktreeInputs gives target every declared seed input that it lacks,
// that Git ignores there and that source holds as a regular file. Git never
// carries an ignored file into a new checkout, so without this a linked
// worktree cannot run the seed commands its own configuration declares. The
// input is cloned where the filesystem can, so it costs no disk space until
// either side changes it. Tracked and unignored paths are never created: a
// branch that removed one keeps it removed.
func prepareWorktreeInputs(ctx context.Context, target, source string, cfg appcfg.Config, ignored func(context.Context, string, string) bool) ([]worktreePreparedInput, error) {
	prepared := []worktreePreparedInput{}
	seen := map[string]struct{}{}
	for _, command := range cfg.Database.Seed.Commands {
		for _, raw := range command.Inputs {
			relative, ok := lexicalWorkspacePath(raw)
			if !ok {
				continue
			}
			if _, exists := seen[relative]; exists {
				continue
			}
			seen[relative] = struct{}{}
			destination := filepath.Join(target, relative)
			if _, err := os.Lstat(destination); err == nil {
				continue
			}
			origin := filepath.Join(source, relative)
			if info, err := os.Lstat(origin); err != nil || !info.Mode().IsRegular() || rejectDBSeedSymlinkPath(source, relative) != nil {
				continue
			}
			if !ignored(ctx, target, relative) || symlinkAncestor(target, relative) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return nil, err
			}
			method := "clone"
			if filemeta.Clone(origin, destination) != nil {
				method = "copy"
				if err := copyRegularFile(origin, destination); err != nil {
					return nil, err
				}
			}
			prepared = append(prepared, worktreePreparedInput{Path: filepath.ToSlash(relative), Method: method})
		}
	}
	slices.SortFunc(prepared, func(a, b worktreePreparedInput) int { return strings.Compare(a.Path, b.Path) })
	return prepared, nil
}

// lexicalWorkspacePath cleans a declared workspace-relative path without
// reading the filesystem and rejects one that leaves the workspace.
func lexicalWorkspacePath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || filepath.IsAbs(raw) {
		return "", false
	}
	relative := filepath.Clean(filepath.FromSlash(raw))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func gitIgnores(ctx context.Context, root, relative string) bool {
	return exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", filepath.ToSlash(relative)).Run() == nil
}

// symlinkAncestor reports whether an existing directory above relative is a
// symlink, through which a created file would land outside the checkout.
func symlinkAncestor(root, relative string) bool {
	current := root
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func copyRegularFile(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	_, err = io.Copy(output, input)
	return err
}
