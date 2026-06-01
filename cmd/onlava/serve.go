package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
)

type serveOptions struct {
	Listen    string
	Port      int
	AppRoot   string
	Env       string
	LogFormat string
}

func serveCommand(args []string) error {
	opts, err := parseServeArgs(args)
	if err != nil {
		return err
	}
	addr := resolveListenAddr(opts.Listen, opts.Port)
	return serveHeadlessFunc(addr, opts)
}

var (
	runWithWatchFunc   = runWithWatch
	runDetachedDevFunc = runDetachedDev
	serveHeadlessFunc  = serveHeadless
)

func parseServeArgs(args []string) (serveOptions, error) {
	opts := serveOptions{Port: 4000, LogFormat: "text"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port", "-p":
			i++
			if i >= len(args) {
				return serveOptions{}, fmt.Errorf("missing value for --port")
			}
			value, err := parsePort(args[i])
			if err != nil {
				return serveOptions{}, err
			}
			opts.Port = value
		case "--listen":
			i++
			if i >= len(args) {
				return serveOptions{}, fmt.Errorf("missing value for --listen")
			}
			opts.Listen = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return serveOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--env":
			i++
			if i >= len(args) {
				return serveOptions{}, fmt.Errorf("missing value for --env")
			}
			opts.Env = strings.TrimSpace(args[i])
			if opts.Env == "" {
				return serveOptions{}, fmt.Errorf("--env must not be empty")
			}
		case "--log-format":
			i++
			if i >= len(args) {
				return serveOptions{}, fmt.Errorf("missing value for --log-format")
			}
			switch args[i] {
			case "text", "json":
				opts.LogFormat = args[i]
			default:
				return serveOptions{}, fmt.Errorf("invalid --log-format %q", args[i])
			}
		case "--verbose", "-v", "--json", "--dashboard", "--watch", "--proxy":
			return serveOptions{}, fmt.Errorf("%s is a development flag; use `onlava dev`", args[i])
		default:
			return serveOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

func serveHeadless(addr string, opts serveOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(opts.Env), "production") {
		result, ok, err := build.LoadReusableBinary(root, cfg)
		if err != nil {
			return err
		}
		if ok {
			if err := build.WriteLatestBuildManifest(result, "compiled"); err != nil {
				return err
			}
			return startHeadlessApp(root, cfg, result.Binary, addr, opts)
		}
	}
	appModel, err := parse.App(root, cfg.Name)
	if err != nil {
		return err
	}
	if err := validateHeadlessProductionSecrets(root, appModel, opts); err != nil {
		return err
	}
	result, err := build.Prepare(root, appModel, cfg, build.PrepareOptions{})
	if err != nil {
		return err
	}
	if err := build.Compile(result); err != nil {
		if result.Ephemeral {
			_ = os.RemoveAll(result.Dir)
		}
		return err
	}
	return startHeadlessApp(root, cfg, result.Binary, addr, opts)
}

func startHeadlessApp(root string, cfg app.Config, binary, addr string, opts serveOptions) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cmd := commandTreeContext(ctx, binary)
	cmd.Dir = root
	env, err := appProcessEnv(root, cfg, opts.LogFormat, opts.Env, "ONLAVA_LISTEN_ADDR="+addr, "ONLAVA_ROLE="+headlessRuntimeRole(cfg))
	if err != nil {
		return err
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("onlava app exited: %w", err)
	}
	return nil
}

func headlessRuntimeRole(cfg app.Config) string {
	_ = cfg
	return "api"
}

func validateHeadlessProductionSecrets(root string, appModel *model.App, opts serveOptions) error {
	if !strings.EqualFold(strings.TrimSpace(opts.Env), "production") {
		return nil
	}
	env, err := appEnvWithDotEnv(os.Environ(), root)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	var missing []string
	for _, field := range collectAppSecretFields(appModel) {
		keys := headlessSecretEnvKeys(field)
		found := false
		for _, key := range keys {
			if _, ok := values[key]; ok {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf("%s (%s)", field, strings.Join(keys, ", ")))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("runtime: missing required secrets for production: %s", strings.Join(missing, "; "))
}

func collectAppSecretFields(appModel *model.App) []string {
	if appModel == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, pkg := range appModel.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok || len(value.Names) != 1 || value.Names[0].Name != "secrets" {
						continue
					}
					if structType, ok := value.Type.(*ast.StructType); ok {
						collectSecretStructFields(structType, seen)
					}
					if len(value.Values) != 1 {
						continue
					}
					lit, ok := value.Values[0].(*ast.CompositeLit)
					if !ok {
						continue
					}
					if structType, ok := lit.Type.(*ast.StructType); ok {
						collectSecretStructFields(structType, seen)
					}
				}
			}
		}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func collectSecretStructFields(structType *ast.StructType, seen map[string]bool) {
	if structType == nil || structType.Fields == nil {
		return
	}
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if ast.IsExported(name.Name) {
				seen[name.Name] = true
			}
		}
	}
}

func headlessSecretEnvKeys(fieldName string) []string {
	keys := []string{fieldName}
	alt := headlessSecretEnvKey(fieldName)
	if alt != "" && alt != fieldName {
		keys = append(keys, alt)
	}
	return keys
}

func headlessSecretEnvKey(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && shouldInsertSecretUnderscore(runes[i-1], r, nextSecretRune(runes, i)) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

func nextSecretRune(runes []rune, index int) rune {
	if index+1 >= len(runes) {
		return 0
	}
	return runes[index+1]
}

func shouldInsertSecretUnderscore(prev, current, next rune) bool {
	if !unicode.IsUpper(current) {
		return false
	}
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)
}

func appProcessEnv(root string, cfg app.Config, logFormat string, envName string, extra ...string) ([]string, error) {
	envLoader := appEnvWithRequiredDotEnv
	if strings.EqualFold(strings.TrimSpace(envName), "production") {
		envLoader = appEnvWithDotEnv
	}
	baseEnv, err := envLoader(os.Environ(), root)
	if err != nil {
		return nil, err
	}
	overrides := []string{
		"ONLAVA_APP_ID=" + cfg.AppID(),
		"ONLAVA_APP_ROOT=" + root,
		"ONLAVA_LOCAL_PROXY=0",
		"ONLAVA_LOG_FORMAT=" + logFormat,
		"ONLAVA_PARENT_MONITOR=1",
		fmt.Sprintf("ONLAVA_PARENT_MONITOR_PID=%d", os.Getpid()),
	}
	overrides = append(overrides, extra...)
	if envName != "" {
		overrides = append(overrides, "ONLAVA_ENV="+envName, "ONLAVA_RUNTIME_ENV="+envName)
	}
	return envWithOverrides(baseEnv, overrides...), nil
}

func envWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, item := range overrides {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys[key] = struct{}{}
		}
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := keys[key]; replace {
				continue
			}
		}
		env = append(env, item)
	}
	return append(env, overrides...)
}
