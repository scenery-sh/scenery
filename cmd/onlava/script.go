package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/envpolicy"
)

const (
	scriptLangGo         = "go"
	scriptLangTypeScript = "typescript"
	scriptRunUsage       = "usage: onlava task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]"
)

type scriptTarget struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
}

type scriptCandidate struct {
	Target scriptTarget `json:"target"`
	Lang   string       `json:"lang"`
	Layout string       `json:"layout"`
	Path   string       `json:"path"`
}

type scriptOptions struct {
	AppRoot    string
	Env        string
	EnvOverlay map[string]string
	Lang       string
	JSON       bool
	Target     string
	Args       []string
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
}

type scriptInspectOutput struct {
	Target    scriptTarget      `json:"target"`
	Candidate scriptCandidate   `json:"candidate"`
	Searched  []scriptCandidate `json:"searched"`
}

type scriptListOutput struct {
	Scripts []scriptCandidate `json:"scripts"`
}

var scriptCommandContext = commandTreeContext

func runCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(scriptRunUsage)
	}
	switch args[0] {
	case "list":
		opts, err := parseScriptListArgs(args[1:])
		if err != nil {
			return err
		}
		return runOnlavaScriptList(context.Background(), os.Stdout, opts)
	case "inspect":
		opts, err := parseScriptInspectArgs(args[1:])
		if err != nil {
			return err
		}
		return runOnlavaScriptInspect(context.Background(), os.Stdout, opts)
	default:
		return runScriptCommand(args)
	}
}

func runScriptCommand(args []string) error {
	opts, err := parseScriptRunArgs(args)
	if err != nil {
		return err
	}
	return runOnlavaScript(context.Background(), opts)
}

func parseScriptListArgs(args []string) (scriptOptions, error) {
	var opts scriptOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return scriptOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseScriptInspectArgs(args []string) (scriptOptions, error) {
	var opts scriptOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--lang":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --lang")
			}
			lang, err := normalizeScriptLang(args[i])
			if err != nil {
				return scriptOptions{}, err
			}
			opts.Lang = lang
		case "--json":
			opts.JSON = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return scriptOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.Target != "" {
				return scriptOptions{}, fmt.Errorf("unexpected argument %q", args[i])
			}
			opts.Target = args[i]
		}
	}
	if opts.Target == "" {
		return scriptOptions{}, fmt.Errorf("usage: onlava task inspect <domain>:<name> [--app-root <path>] [--lang go|typescript] [--json]")
	}
	if _, err := parseScriptTarget(opts.Target); err != nil {
		return scriptOptions{}, err
	}
	return opts, nil
}

func parseScriptRunArgs(args []string) (scriptOptions, error) {
	var opts scriptOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--env":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --env")
			}
			opts.Env = strings.TrimSpace(args[i])
			if opts.Env == "" {
				return scriptOptions{}, fmt.Errorf("--env must not be empty")
			}
		case "--lang":
			i++
			if i >= len(args) {
				return scriptOptions{}, fmt.Errorf("missing value for --lang")
			}
			lang, err := normalizeScriptLang(args[i])
			if err != nil {
				return scriptOptions{}, err
			}
			opts.Lang = lang
		default:
			if strings.HasPrefix(args[i], "-") {
				return scriptOptions{}, fmt.Errorf("unknown flag %q before script target", args[i])
			}
			opts.Target = args[i]
			opts.Args = append([]string(nil), args[i+1:]...)
			if len(opts.Args) > 0 && opts.Args[0] == "--" {
				opts.Args = opts.Args[1:]
			}
			if _, err := parseScriptTarget(opts.Target); err != nil {
				return scriptOptions{}, err
			}
			return opts, nil
		}
	}
	return scriptOptions{}, fmt.Errorf(scriptRunUsage)
}

func normalizeScriptLang(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "", nil
	case "go", "golang":
		return scriptLangGo, nil
	case "typescript", "ts":
		return scriptLangTypeScript, nil
	default:
		return "", fmt.Errorf("--lang must be go or typescript")
	}
}

func parseScriptTarget(value string) (scriptTarget, error) {
	value = strings.TrimSpace(value)
	domain, name, ok := strings.Cut(value, ":")
	if !ok || strings.Contains(name, ":") {
		return scriptTarget{}, fmt.Errorf("invalid code task target %q; expected <domain>:<name>", value)
	}
	target := scriptTarget{Domain: strings.TrimSpace(domain), Name: strings.TrimSpace(name)}
	if !validScriptSegment(target.Domain) || !validScriptSegment(target.Name) {
		return scriptTarget{}, fmt.Errorf("invalid code task target %q; domain and name must match [A-Za-z0-9_][A-Za-z0-9_-]*", value)
	}
	return target, nil
}

func validScriptSegment(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func runOnlavaScriptList(ctx context.Context, stdout io.Writer, opts scriptOptions) error {
	root, _, err := discoverScriptApp(opts.AppRoot)
	if err != nil {
		return err
	}
	scripts, err := listScriptCandidates(root)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(scriptListOutput{Scripts: scripts})
	}
	for _, script := range scripts {
		if _, err := fmt.Fprintf(stdout, "%s:%s\t%s\t%s\t%s\n", script.Target.Domain, script.Target.Name, script.Lang, script.Layout, script.Path); err != nil {
			return err
		}
	}
	_ = ctx
	return nil
}

func runOnlavaScriptInspect(ctx context.Context, stdout io.Writer, opts scriptOptions) error {
	root, _, err := discoverScriptApp(opts.AppRoot)
	if err != nil {
		return err
	}
	target, err := parseScriptTarget(opts.Target)
	if err != nil {
		return err
	}
	candidate, searched, err := resolveScriptCandidate(root, target, opts.Lang)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(scriptInspectOutput{Target: target, Candidate: candidate, Searched: searched})
	}
	_, err = fmt.Fprintf(stdout, "%s:%s\n  lang: %s\n  layout: %s\n  path: %s\n", target.Domain, target.Name, candidate.Lang, candidate.Layout, candidate.Path)
	_ = ctx
	return err
}

func runOnlavaScript(ctx context.Context, opts scriptOptions) error {
	root, cfg, err := discoverScriptApp(opts.AppRoot)
	if err != nil {
		return err
	}
	target, err := parseScriptTarget(opts.Target)
	if err != nil {
		return err
	}
	candidate, _, err := resolveScriptCandidate(root, target, opts.Lang)
	if err != nil {
		return err
	}
	switch candidate.Lang {
	case scriptLangGo:
		return runGoScript(ctx, root, cfg, candidate, opts)
	case scriptLangTypeScript:
		return runTypeScriptScript(ctx, root, cfg, candidate, opts)
	default:
		return fmt.Errorf("unsupported script language %q", candidate.Lang)
	}
}

func discoverScriptApp(appRootFlag string) (string, app.Config, error) {
	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return "", app.Config{}, err
	}
	return app.DiscoverRoot(start)
}

func scriptCandidateSearch(root string, target scriptTarget) []scriptCandidate {
	base := filepath.Join(target.Domain, "tasks")
	return []scriptCandidate{
		{Target: target, Lang: scriptLangGo, Layout: "go-file", Path: filepath.ToSlash(filepath.Join(base, target.Name+".task.go"))},
		{Target: target, Lang: scriptLangTypeScript, Layout: "typescript-file", Path: filepath.ToSlash(filepath.Join(base, target.Name+".task.ts"))},
		{Target: target, Lang: scriptLangGo, Layout: "go-dir", Path: filepath.ToSlash(filepath.Join(base, target.Name, "main.go"))},
		{Target: target, Lang: scriptLangTypeScript, Layout: "typescript-dir", Path: filepath.ToSlash(filepath.Join(base, target.Name, "index.ts"))},
	}
}

func resolveScriptCandidate(root string, target scriptTarget, lang string) (scriptCandidate, []scriptCandidate, error) {
	searched := scriptCandidateSearch(root, target)
	var matches []scriptCandidate
	for _, candidate := range searched {
		if lang != "" && candidate.Lang != lang {
			continue
		}
		if fileExists(filepath.Join(root, filepath.FromSlash(candidate.Path))) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return scriptCandidate{}, searched, missingScriptError(target, searched, lang)
	case 1:
		return matches[0], searched, nil
	default:
		return scriptCandidate{}, searched, ambiguousScriptError(target, matches)
	}
}

func missingScriptError(target scriptTarget, searched []scriptCandidate, lang string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "code task %q not found", scriptTargetString(target))
	if lang != "" {
		fmt.Fprintf(&b, " for language %s", lang)
	}
	b.WriteString("; searched:\n")
	for _, candidate := range searched {
		if lang == "" || candidate.Lang == lang {
			fmt.Fprintf(&b, "  %s\n", candidate.Path)
		}
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

func ambiguousScriptError(target scriptTarget, matches []scriptCandidate) error {
	var b strings.Builder
	fmt.Fprintf(&b, "code task %q is ambiguous:\n", scriptTargetString(target))
	for _, candidate := range matches {
		fmt.Fprintf(&b, "  %s\n", candidate.Path)
	}
	b.WriteString("\nUse --lang go or --lang typescript when language is ambiguous, or remove duplicate task layouts.")
	return errors.New(b.String())
}

func listScriptCandidates(root string) ([]scriptCandidate, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []scriptCandidate
	for _, entry := range entries {
		if !entry.IsDir() || !validScriptSegment(entry.Name()) {
			continue
		}
		tasksDir := filepath.Join(root, entry.Name(), "tasks")
		if info, err := os.Stat(tasksDir); err != nil || !info.IsDir() {
			continue
		}
		candidates, err := listDomainScriptCandidates(root, entry.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i].Target.Domain + ":" + out[i].Target.Name + ":" + out[i].Lang + ":" + out[i].Layout
		b := out[j].Target.Domain + ":" + out[j].Target.Name + ":" + out[j].Lang + ":" + out[j].Layout
		return a < b
	})
	return out, nil
}

func listDomainScriptCandidates(root, domain string) ([]scriptCandidate, error) {
	tasksDir := filepath.Join(root, domain, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []scriptCandidate
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.Type().IsRegular() && strings.HasSuffix(name, ".task.go"):
			scriptName := strings.TrimSuffix(name, ".task.go")
			target := scriptTarget{Domain: domain, Name: scriptName}
			if validScriptSegment(scriptName) && !seen[domain+":"+scriptName+":go-file"] {
				out = append(out, scriptCandidate{Target: target, Lang: scriptLangGo, Layout: "go-file", Path: filepath.ToSlash(filepath.Join(domain, "tasks", name))})
				seen[domain+":"+scriptName+":go-file"] = true
			}
		case entry.Type().IsRegular() && strings.HasSuffix(name, ".task.ts"):
			scriptName := strings.TrimSuffix(name, ".task.ts")
			target := scriptTarget{Domain: domain, Name: scriptName}
			if validScriptSegment(scriptName) && !seen[domain+":"+scriptName+":typescript-file"] {
				out = append(out, scriptCandidate{Target: target, Lang: scriptLangTypeScript, Layout: "typescript-file", Path: filepath.ToSlash(filepath.Join(domain, "tasks", name))})
				seen[domain+":"+scriptName+":typescript-file"] = true
			}
		case entry.IsDir() && validScriptSegment(name):
			target := scriptTarget{Domain: domain, Name: name}
			for _, candidate := range scriptCandidateSearch(root, target)[2:] {
				if fileExists(filepath.Join(root, filepath.FromSlash(candidate.Path))) {
					out = append(out, candidate)
				}
			}
		}
	}
	return out, nil
}

func runGoScript(ctx context.Context, root string, cfg app.Config, candidate scriptCandidate, opts scriptOptions) error {
	var args []string
	switch candidate.Layout {
	case "go-file":
		if err := validateGoScriptBuildTag(filepath.Join(root, filepath.FromSlash(candidate.Path))); err != nil {
			return err
		}
		args = []string{"run", "./" + candidate.Path}
	case "go-dir":
		args = []string{"run", "./" + filepath.ToSlash(filepath.Dir(candidate.Path))}
	default:
		return fmt.Errorf("unsupported Go script layout %q", candidate.Layout)
	}
	args = append(args, opts.Args...)
	return runScriptProcess(ctx, root, cfg, "go", args, opts)
}

func runTypeScriptScript(ctx context.Context, root string, cfg app.Config, candidate scriptCandidate, opts scriptOptions) error {
	program, args, err := typeScriptScriptCommand(candidate.Path)
	if err != nil {
		return err
	}
	args = append(args, opts.Args...)
	return runScriptProcess(ctx, root, cfg, program, args, opts)
}

func typeScriptScriptCommand(path string) (string, []string, error) {
	if bun, err := execLookPath("bun"); err == nil {
		return bun, []string{filepath.ToSlash(path)}, nil
	}
	if node, err := execLookPath("node"); err == nil {
		return node, []string{"--import", "tsx", filepath.ToSlash(path)}, nil
	}
	return "", nil, fmt.Errorf("onlava task run requires bun or node in PATH for TypeScript code tasks")
}

func validateGoScriptBuildTag(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if strings.TrimSpace(strings.TrimPrefix(first, "\ufeff")) != "//go:build ignore" {
		return fmt.Errorf("single-file Go code task %s must start with //go:build ignore", filepath.ToSlash(path))
	}
	return nil
}

func runScriptProcess(ctx context.Context, root string, cfg app.Config, program string, args []string, opts scriptOptions) error {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	cmd := scriptCommandContext(ctx, program, args...)
	cmd.Dir = root
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root, ".env", ".env.local")
	if err != nil {
		return err
	}
	env = overlayEnv(env, opts.EnvOverlay)
	extra := []string{
		"ONLAVA_APP_ID=" + cfg.AppID(),
		"ONLAVA_APP_ROOT=" + root,
	}
	if opts.Env != "" {
		extra = append(extra, "ONLAVA_ENV="+opts.Env, "ONLAVA_RUNTIME_ENV="+opts.Env)
	}
	cmd.Env = envWithOverrides(env, extra...)
	cmd.Stdout = firstNonNilWriter(opts.Stdout, os.Stdout)
	cmd.Stderr = firstNonNilWriter(opts.Stderr, os.Stderr)
	cmd.Stdin = firstNonNilReader(opts.Stdin, os.Stdin)
	if err := cmd.Start(); err != nil {
		return err
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("onlava task run exited: %w", err)
	}
	return nil
}

func firstNonNilWriter(items ...io.Writer) io.Writer {
	for _, item := range items {
		if item != nil {
			return item
		}
	}
	return io.Discard
}

func firstNonNilReader(items ...io.Reader) io.Reader {
	for _, item := range items {
		if item != nil {
			return item
		}
	}
	return nil
}

func scriptTargetString(target scriptTarget) string {
	return target.Domain + ":" + target.Name
}
