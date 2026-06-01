package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/clientgen"
	"github.com/pbrazdil/onlava/internal/parse"
)

type genClientOptions struct {
	AppRoot string
	Target  string
	Lang    string
	Output  string
}

func genCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n  onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]")
	}
	switch args[0] {
	case "client":
		return genClientCommand(args[1:])
	default:
		return fmt.Errorf("unknown gen subcommand %q", args[0])
	}
}

func genClientCommand(args []string) error {
	opts, err := parseGenClientArgs(args)
	if err != nil {
		return err
	}
	if opts.Output == "" {
		return fmt.Errorf("missing required --output")
	}
	lang := strings.ToLower(strings.TrimSpace(opts.Lang))
	switch lang {
	case "typescript", "ts":
	default:
		return fmt.Errorf("unsupported client language %q", opts.Lang)
	}

	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	outputPath, err := writeTypeScriptClient(appRoot, cfg, opts.Target, opts.Output)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "onlava: generated TypeScript client at %s\n", outputPath)
	return nil
}

func discoverConfiguredApp(appRootOpt string) (string, app.Config, error) {
	start, err := resolveAppRoot(appRootOpt)
	if err != nil {
		return "", app.Config{}, err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return "", app.Config{}, err
	}
	return appRoot, cfg, nil
}

func writeTypeScriptClient(appRoot string, cfg app.Config, target, outputPath string) (string, error) {
	if target != "" && target != cfg.ID && target != cfg.Name {
		return "", fmt.Errorf("client target %q does not match local app %q", target, cfg.Name)
	}
	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return "", err
	}
	output, err := clientgen.GenerateTypeScript(model, clientgen.TypeScriptOptions{
		AppSlug:      firstNonEmpty(cfg.ID, cfg.Name),
		StandardAuth: cfg.Auth.Enabled,
	})
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(appRoot, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outputPath, output, 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

func parseGenClientArgs(args []string) (genClientOptions, error) {
	var opts genClientOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--app-root":
			i++
			if i >= len(args) {
				return genClientOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case strings.HasPrefix(arg, "--app-root="):
			opts.AppRoot = strings.TrimPrefix(arg, "--app-root=")
		case arg == "--lang":
			i++
			if i >= len(args) {
				return genClientOptions{}, fmt.Errorf("missing value for --lang")
			}
			opts.Lang = args[i]
		case strings.HasPrefix(arg, "--lang="):
			opts.Lang = strings.TrimPrefix(arg, "--lang=")
		case arg == "--output" || arg == "-o":
			i++
			if i >= len(args) {
				return genClientOptions{}, fmt.Errorf("missing value for %s", arg)
			}
			opts.Output = args[i]
		case strings.HasPrefix(arg, "--output="):
			opts.Output = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			opts.Output = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-"):
			return genClientOptions{}, fmt.Errorf("unknown flag %q", arg)
		default:
			if opts.Target != "" {
				return genClientOptions{}, fmt.Errorf("unexpected argument %q", arg)
			}
			opts.Target = arg
		}
	}
	return opts, nil
}
