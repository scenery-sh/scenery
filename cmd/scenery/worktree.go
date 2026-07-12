package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

const worktreeListSchemaVersion = "scenery.worktree.list.v1"
const worktreeCreateSchemaVersion = "scenery.worktree.create.v1"
const worktreeRemoveSchemaVersion = "scenery.worktree.remove.v1"

type worktreeOptions struct {
	Command string
	Name    string
	AppRoot string
	From    string
	JSON    bool
	DB      bool
}

type worktreeRecord struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	Bare   bool   `json:"bare,omitempty"`
}

type worktreeCreateResult struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	From          string `json:"from,omitempty"`
	NextCommand   string `json:"next_command"`
	Message       string `json:"message,omitempty"`
}

type worktreeListResult struct {
	SchemaVersion string           `json:"schema_version"`
	OK            bool             `json:"ok"`
	AppRoot       string           `json:"app_root"`
	Worktrees     []worktreeRecord `json:"worktrees"`
}

type worktreeRemoveResult struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Message       string `json:"message,omitempty"`
}

func worktreeCommand(args []string) error {
	return runWorktreeCommand(context.Background(), os.Stdout, args)
}

func runWorktreeCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseWorktreeArgs(args)
	if err != nil {
		return err
	}
	switch opts.Command {
	case "create":
		return runWorktreeCreate(ctx, stdout, opts)
	case "list":
		return runWorktreeList(ctx, stdout, opts)
	case "remove":
		return runWorktreeRemove(ctx, stdout, opts)
	default:
		return fmt.Errorf("unknown worktree command %q", opts.Command)
	}
}

func parseWorktreeArgs(args []string) (worktreeOptions, error) {
	opts := worktreeOptions{}
	flags := newCLIFlagSet("worktree")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.From, "from", "", "")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.DB, "db", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return worktreeOptions{}, err
	}
	if len(positionals) == 0 {
		return worktreeOptions{}, fmt.Errorf("usage: scenery worktree create|list|remove ...")
	}
	opts.Command = positionals[0]
	if len(positionals) > 1 {
		opts.Name = positionals[1]
	}
	if len(positionals) > 2 {
		return worktreeOptions{}, fmt.Errorf("unexpected argument %q", positionals[2])
	}
	switch opts.Command {
	case "create", "remove":
		if strings.TrimSpace(opts.Name) == "" {
			return worktreeOptions{}, fmt.Errorf("scenery worktree %s requires <name>", opts.Command)
		}
	case "list":
	default:
		return worktreeOptions{}, fmt.Errorf("unknown worktree command %q", opts.Command)
	}
	return opts, nil
}

func runWorktreeCreate(ctx context.Context, stdout io.Writer, opts worktreeOptions) error {
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
	if err := runGitCommand(ctx, args...); err != nil {
		return err
	}
	result := worktreeCreateResult{
		SchemaVersion: worktreeCreateSchemaVersion,
		OK:            true,
		Name:          name,
		Path:          target,
		Branch:        name,
		From:          strings.TrimSpace(opts.From),
		NextCommand:   "cd " + target + " && scenery up",
	}
	result.Message = "Git worktree created."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "created worktree %s at %s\n", name, target)
	fmt.Fprintf(stdout, "next: %s\n", result.NextCommand)
	return nil
}

func runWorktreeList(ctx context.Context, stdout io.Writer, opts worktreeOptions) error {
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	worktrees, err := listGitWorktrees(ctx, appRoot)
	if err != nil {
		return err
	}
	result := worktreeListResult{
		SchemaVersion: worktreeListSchemaVersion,
		OK:            true,
		AppRoot:       appRoot,
		Worktrees:     worktrees,
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	for _, wt := range worktrees {
		fmt.Fprintf(stdout, "%s %s\n", wt.Path, wt.Branch)
	}
	return nil
}

func runWorktreeRemove(ctx context.Context, stdout io.Writer, opts worktreeOptions) error {
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	target, err := resolveExistingWorktreeTarget(ctx, appRoot, opts.Name)
	if err != nil {
		return err
	}
	result := worktreeRemoveResult{
		SchemaVersion: worktreeRemoveSchemaVersion,
		OK:            true,
		Name:          sanitizeWorktreeName(opts.Name),
		Path:          target,
	}
	var dbStateBackup string
	if opts.DB {
		stateDir := filepath.Join(target, ".scenery")
		if _, err := os.Stat(stateDir); err == nil {
			backupRoot, err := os.MkdirTemp(filepath.Dir(target), ".scenery-worktree-state-*")
			if err != nil {
				return err
			}
			dbStateBackup = filepath.Join(backupRoot, ".scenery")
			if err := os.Rename(stateDir, dbStateBackup); err != nil {
				_ = os.RemoveAll(backupRoot)
				return err
			}
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := runGitCommand(ctx, "-C", appRoot, "worktree", "remove", target); err != nil {
		if dbStateBackup != "" {
			_ = os.Rename(dbStateBackup, filepath.Join(target, ".scenery"))
			_ = os.RemoveAll(filepath.Dir(dbStateBackup))
		}
		return err
	}
	if dbStateBackup != "" {
		_ = os.RemoveAll(filepath.Dir(dbStateBackup))
	}
	result.Message = "Git worktree removed."
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "removed worktree %s\n", target)
	return nil
}

func defaultWorktreePath(appRoot, name string) string {
	return filepath.Join(filepath.Dir(appRoot), filepath.Base(appRoot)+"-"+name)
}

func resolveExistingWorktreeTarget(ctx context.Context, appRoot, name string) (string, error) {
	cleanName := sanitizeWorktreeName(name)
	if cleanName == "" {
		return "", fmt.Errorf("worktree name is empty after sanitization")
	}
	worktrees, err := listGitWorktrees(ctx, appRoot)
	if err != nil {
		return "", err
	}
	defaultPath := defaultWorktreePath(appRoot, cleanName)
	for _, wt := range worktrees {
		if cleanAbsPath(wt.Path) == cleanAbsPath(defaultPath) || strings.TrimPrefix(wt.Branch, "refs/heads/") == cleanName || filepath.Base(wt.Path) == cleanName {
			return wt.Path, nil
		}
	}
	return "", fmt.Errorf("git worktree %q is not registered", cleanName)
}

func listGitWorktrees(ctx context.Context, appRoot string) ([]worktreeRecord, error) {
	output, err := gitCommandOutput(ctx, appRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []worktreeRecord
	var current *worktreeRecord
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current != nil {
				result = append(result, *current)
				current = nil
			}
			continue
		}
		key, value, _ := strings.Cut(line, " ")
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
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		result = append(result, *current)
	}
	return result, nil
}

func runGitCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitCommandOutput(ctx context.Context, appRoot string, args ...string) (string, error) {
	all := append([]string{"-C", appRoot}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(all, " "), err, strings.TrimSpace(string(output)))
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
