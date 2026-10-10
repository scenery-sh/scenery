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
	"unicode"
)

const worktreeListKind = "scenery.worktree.list"
const worktreeCreateKind = "scenery.worktree.create"
const worktreeRemoveKind = "scenery.worktree.remove"

type worktreeOptions struct {
	Command          string
	Name             string
	AppRoot          string
	From             string
	JSON             bool
	Yes              bool
	ExpectedRevision string
}

type worktreeRecord struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	Bare   bool   `json:"bare,omitempty"`
}

type worktreeCreateResult struct {
	cliPayloadIdentity
	OK          bool   `json:"ok"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	From        string `json:"from,omitempty"`
	NextCommand string `json:"next_command"`
	Message     string `json:"message,omitempty"`
}

type worktreeListResult struct {
	cliPayloadIdentity
	OK        bool             `json:"ok"`
	AppRoot   string           `json:"app_root"`
	Worktrees []worktreeRecord `json:"worktrees"`
}

type worktreeRemoveResult struct {
	cliPayloadIdentity
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

func worktreeCommand(args []string) error {
	return runWorktreeCommand(context.Background(), os.Stdout, args)
}

func runWorktreeCommand(ctx context.Context, stdout io.Writer, args []string) error {
	return runWorktreeCommandWithGit(ctx, stdout, args, runGitCommand, listGitWorktrees)
}

func runWorktreeCommandWithGit(ctx context.Context, stdout io.Writer, args []string, runGit func(context.Context, ...string) error, listWorktrees func(context.Context, string) ([]worktreeRecord, error)) error {
	opts, err := parseWorktreeArgs(args)
	if err != nil {
		return err
	}
	switch opts.Command {
	case "create":
		return runWorktreeCreateWithGit(ctx, stdout, opts, runGit)
	case "list":
		return runWorktreeListWithGit(ctx, stdout, opts, listWorktrees)
	case "remove":
		return runWorktreeRemoveWithGit(ctx, stdout, opts, listWorktrees, runGit)
	case "upgrade":
		return runWorktreeUpgrade(ctx, stdout, opts)
	default:
		return usageErrorf("unknown worktree command %q", opts.Command)
	}
}

func parseWorktreeArgs(args []string) (worktreeOptions, error) {
	opts := worktreeOptions{}
	flags := newCLIFlagSet("worktree")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.From, "from", "", "")
	flags.BoolVar(&opts.Yes, "yes", false, "")
	flags.StringVar(&opts.ExpectedRevision, "expect-revision", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return worktreeOptions{}, err
	}
	if len(positionals) == 0 {
		return worktreeOptions{}, usageErrorf("usage: scenery worktree create|list|remove|upgrade")
	}
	opts.Command = positionals[0]
	if len(positionals) > 1 {
		opts.Name = positionals[1]
	}
	if len(positionals) > 2 {
		return worktreeOptions{}, usageErrorf("unexpected argument %q", positionals[2])
	}
	switch opts.Command {
	case "create", "remove":
		if strings.TrimSpace(opts.Name) == "" {
			return worktreeOptions{}, usageErrorf("scenery worktree %s requires <name>", opts.Command)
		}
	case "list":
		if opts.Name != "" {
			return worktreeOptions{}, usageErrorf("unexpected argument %q", opts.Name)
		}
	case "upgrade":
		if opts.Name != "" || opts.From != "" {
			return worktreeOptions{}, usageErrorf("scenery worktree upgrade accepts --app-root, not a Git worktree name or --from")
		}
		if opts.Yes != (opts.ExpectedRevision != "") {
			return worktreeOptions{}, usageErrorf("scenery worktree upgrade applies only with both --yes and --expect-revision; omit both to preview")
		}
	default:
		return worktreeOptions{}, usageErrorf("unknown worktree command %q", opts.Command)
	}
	if opts.Command != "upgrade" && (opts.Yes || opts.ExpectedRevision != "") {
		return worktreeOptions{}, usageErrorf("--yes and --expect-revision belong only to scenery worktree upgrade")
	}
	return opts, nil
}

func runWorktreeCreateWithGit(ctx context.Context, stdout io.Writer, opts worktreeOptions, runGit func(context.Context, ...string) error) error {
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	name := sanitizeWorktreeName(opts.Name)
	if name == "" {
		return fmt.Errorf("worktree name is empty after sanitization")
	}
	target := defaultWorktreePath(appRoot, name)
	args := []string{"-C", appRoot, "worktree", "add", "-b", name, target}
	if strings.TrimSpace(opts.From) != "" {
		args = append(args, opts.From)
	}
	if err := runGit(ctx, args...); err != nil {
		return err
	}
	result := worktreeCreateResult{
		cliPayloadIdentity: newCLIPayloadIdentity(worktreeCreateKind),
		OK:                 true,
		Name:               name,
		Path:               target,
		Branch:             name,
		From:               strings.TrimSpace(opts.From),
		NextCommand:        "cd " + target + " && scenery up",
	}
	result.Message = "Git worktree created."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	_, _ = fmt.Fprintf(stdout, "created worktree %s at %s\n", name, target)
	_, _ = fmt.Fprintf(stdout, "next: %s\n", result.NextCommand)
	return nil
}

func runWorktreeListWithGit(ctx context.Context, stdout io.Writer, opts worktreeOptions, listWorktrees func(context.Context, string) ([]worktreeRecord, error)) error {
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	worktrees, err := listWorktrees(ctx, appRoot)
	if err != nil {
		return err
	}
	result := worktreeListResult{
		cliPayloadIdentity: newCLIPayloadIdentity(worktreeListKind),
		OK:                 true,
		AppRoot:            appRoot,
		Worktrees:          worktrees,
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	for _, wt := range worktrees {
		_, _ = fmt.Fprintf(stdout, "%s %s\n", wt.Path, wt.Branch)
	}
	return nil
}

func runWorktreeRemoveWithList(ctx context.Context, stdout io.Writer, opts worktreeOptions, listWorktrees func(context.Context, string) ([]worktreeRecord, error)) error {
	return runWorktreeRemoveWithGit(ctx, stdout, opts, listWorktrees, runGitCommand)
}

func runWorktreeRemoveWithGit(ctx context.Context, stdout io.Writer, opts worktreeOptions, listWorktrees func(context.Context, string) ([]worktreeRecord, error), runGit func(context.Context, ...string) error) error {
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	target, err := resolveExistingWorktreeTarget(ctx, appRoot, opts.Name, listWorktrees)
	if err != nil {
		return err
	}
	result := worktreeRemoveResult{
		cliPayloadIdentity: newCLIPayloadIdentity(worktreeRemoveKind),
		OK:                 true,
		Name:               sanitizeWorktreeName(opts.Name),
		Path:               target,
	}
	if err := runGit(ctx, "-C", appRoot, "worktree", "remove", target); err != nil {
		return err
	}
	result.Message = "Git worktree removed; retained managed database resources are unchanged."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	_, _ = fmt.Fprintf(stdout, "removed worktree %s\n", target)
	return nil
}

func defaultWorktreePath(appRoot, name string) string {
	return filepath.Join(filepath.Dir(appRoot), filepath.Base(appRoot)+"-"+name)
}

func resolveExistingWorktreeTarget(ctx context.Context, appRoot, name string, listWorktrees func(context.Context, string) ([]worktreeRecord, error)) (string, error) {
	cleanName := sanitizeWorktreeName(name)
	if cleanName == "" {
		return "", fmt.Errorf("worktree name is empty after sanitization")
	}
	worktrees, err := listWorktrees(ctx, appRoot)
	if err != nil {
		return "", err
	}
	defaultPath := defaultWorktreePath(appRoot, cleanName)
	matches := make(map[string]string)
	for _, wt := range worktrees {
		if cleanAbsPath(wt.Path) == cleanAbsPath(defaultPath) || strings.TrimPrefix(wt.Branch, "refs/heads/") == cleanName || filepath.Base(wt.Path) == cleanName {
			path := cleanAbsPath(wt.Path)
			if _, exists := matches[path]; !exists {
				matches[path] = wt.Path
			}
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("git worktree %q is not registered", cleanName)
	}
	if len(matches) == 1 {
		for _, path := range matches {
			return path, nil
		}
	}
	paths := make([]string, 0, len(matches))
	for path := range matches {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return "", preconditionErrorf("git worktree %q is ambiguous; matching paths: %q", cleanName, paths)
}

func listGitWorktrees(ctx context.Context, appRoot string) ([]worktreeRecord, error) {
	output, err := gitCommandOutput(ctx, appRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseGitWorktrees(output), nil
}

// NUL-delimited attributes keep newlines and other path bytes inside their value.
func parseGitWorktrees(output string) []worktreeRecord {
	var result []worktreeRecord
	var current *worktreeRecord
	for field := range strings.SplitSeq(output, "\x00") {
		if field == "" {
			if current != nil {
				result = append(result, *current)
				current = nil
			}
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" {
			if current != nil {
				result = append(result, *current)
			}
			current = &worktreeRecord{Path: value}
			continue
		}
		if current == nil {
			continue
		}
		switch key {
		case "HEAD":
			current.Head = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			current.Bare = true
		}
	}
	if current != nil {
		result = append(result, *current)
	}
	return result
}

func runGitCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return preconditionErrorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitCommandOutput(ctx context.Context, appRoot string, args ...string) (string, error) {
	all := append([]string{"-C", appRoot}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", preconditionErrorf("git %s: %w: %s", strings.Join(all, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func sanitizeWorktreeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if r == '-' || r == '_' || r == '.' || unicode.IsSpace(r) {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
