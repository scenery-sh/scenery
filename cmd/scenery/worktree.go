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

	appcfg "scenery.sh/internal/app"
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
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	Branch        string         `json:"branch"`
	From          string         `json:"from,omitempty"`
	DBPin         *worktreeDBPin `json:"db_pin,omitempty"`
	NextCommand   string         `json:"next_command"`
	Message       string         `json:"message,omitempty"`
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
	DBPinRemoved  bool   `json:"db_pin_removed"`
	Message       string `json:"message,omitempty"`
}

var ensureDBBranchForWorktreeCreateFn = func(ctx context.Context, cfg appcfg.Config, pin worktreeDBPin) (dbBranchBackendStatus, error) {
	return (postgresBranchProvider{cfg: cfg}).EnsureBranch(ctx, pin)
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
	if len(args) == 0 {
		return worktreeOptions{}, fmt.Errorf("usage: scenery worktree create|list|remove ...")
	}
	opts := worktreeOptions{Command: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return worktreeOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--from":
			i++
			if i >= len(args) {
				return worktreeOptions{}, fmt.Errorf("missing value for --from")
			}
			opts.From = args[i]
		case "--json":
			opts.JSON = true
		case "--db":
			opts.DB = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return worktreeOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.Name != "" {
				return worktreeOptions{}, fmt.Errorf("unexpected argument %q", args[i])
			}
			opts.Name = args[i]
		}
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
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	name := sanitizeDBBranchSegment(opts.Name)
	if name == "" {
		return fmt.Errorf("worktree name is empty after sanitization")
	}
	target := defaultWorktreePath(appRoot, name)
	autoPin := false
	pinTemplate := ""
	if _, svc, ok := managedPostgresDeclared(cfg); ok && postgresServiceUsesBranching(svc) {
		policy := firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), dbBranchDefaultPolicy)
		if policy != "manual" {
			autoPin = true
			pinTemplate = firstNonEmpty(strings.TrimSpace(svc.BranchNameTemplate), dbBranchDefaultNameTemplate)
			if policy == "session" && strings.TrimSpace(svc.BranchNameTemplate) == "" {
				pinTemplate = "{app}/{session}"
			}
		}
	}
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
	if autoPin {
		targetRoot, targetCfg, err := appcfg.DiscoverRoot(target)
		if err != nil {
			rollbackCreatedWorktree(ctx, appRoot, target)
			return err
		}
		pin, err := buildWorktreeDBPinForSession(targetRoot, targetCfg, nil, renderDBBranchTemplate(pinTemplate, targetRoot, targetCfg, nil))
		if err != nil {
			rollbackCreatedWorktree(ctx, appRoot, target)
			return err
		}
		if err := writeWorktreeDBPin(targetRoot, pin); err != nil {
			rollbackCreatedWorktree(ctx, appRoot, target)
			return err
		}
		backendStatus, err := ensureDBBranchForWorktreeCreateFn(ctx, targetCfg, pin)
		if err != nil {
			rollbackCreatedWorktree(ctx, appRoot, target)
			return err
		}
		result.DBPin = &pin
		result.Message = "Git worktree created and local database branch pin written. Postgres branch provider ensure ran; connection becomes usable when backend_status is ready."
		if backendStatus.Status == "ready" {
			result.Message = "Git worktree created and local database branch pin written. Backend Postgres branch is ready."
		}
	} else {
		result.Message = "Git worktree created."
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "created worktree %s at %s\n", name, target)
	if result.DBPin != nil {
		fmt.Fprintf(stdout, "pinned db branch %s\n", result.DBPin.Branch)
	}
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
		Name:          sanitizeDBBranchSegment(opts.Name),
		Path:          target,
	}
	var dbPinPresent bool
	var dbStateBackup string
	if opts.DB {
		stateDir := filepath.Join(target, ".scenery")
		if _, err := os.Stat(worktreeDBPinPath(target)); err == nil {
			dbPinPresent = true
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Stat(stateDir); err == nil {
			backupRoot, err := os.MkdirTemp(filepath.Dir(target), ".scenery-worktree-db-*")
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
	if opts.DB {
		result.DBPinRemoved = dbPinPresent
	}
	result.Message = "Git worktree removed. Backend Postgres branch deletion is not implemented yet."
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
	cleanName := sanitizeDBBranchSegment(name)
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

func rollbackCreatedWorktree(ctx context.Context, appRoot, target string) {
	_ = runGitCommand(ctx, "-C", appRoot, "worktree", "remove", "--force", target)
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
