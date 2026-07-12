package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/toolchain"
)

type toolchainOptions struct {
	Command            string
	JSON               bool
	All                bool
	Tool               string
	Platform           toolchain.Platform
	Images             bool
	Strict             bool
	IncludeSourceLocks bool
}

func toolchainCommand(args []string) error {
	return runToolchain(context.Background(), os.Stdout, args)
}

func runToolchain(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseToolchainArgs(args)
	if err != nil {
		return err
	}
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(cwd), manifest)
	if err != nil {
		return err
	}
	store.RootDir = cwd
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	store.Platform = toolchain.CurrentPlatform()
	common := toolchain.Options{
		RootDir:  cwd,
		Platform: opts.Platform,
		Tool:     opts.Tool,
		Strict:   opts.Strict,
		Images:   opts.Images,
	}
	switch opts.Command {
	case "list":
		status, err := store.List(ctx, common)
		if err != nil {
			return err
		}
		return renderToolchainStatus(stdout, opts.JSON, opts.All, status)
	case "verify":
		status, err := store.Verify(ctx, common)
		if err != nil {
			return err
		}
		if opts.Strict {
			for _, artifact := range status.Artifacts {
				for _, image := range artifact.Images {
					if image.Status == "invalid" {
						err = fmt.Errorf("toolchain image %s is invalid: %s", image.Ref, image.Message)
						break
					}
				}
			}
		}
		if renderErr := renderToolchainStatus(stdout, opts.JSON, opts.All, status); renderErr != nil {
			return renderErr
		}
		return err
	case "sync":
		status, err := store.Sync(ctx, common)
		if err != nil {
			return err
		}
		return renderToolchainStatus(stdout, opts.JSON, opts.All, status)
	case "path":
		if opts.Tool == "" {
			return fmt.Errorf("scenery system toolchain path requires --tool <name>")
		}
		if _, ok := manifest.Artifact(opts.Tool); !ok {
			return fmt.Errorf("unknown toolchain artifact %q", opts.Tool)
		}
		status, err := store.Path(ctx, opts.Tool, opts.Platform)
		if err != nil && !opts.JSON {
			return err
		}
		if opts.JSON {
			return writeCLIJSON(stdout, status)
		}
		if status.ManagedPath == "" {
			return err
		}
		_, printErr := fmt.Fprintln(stdout, status.ManagedPath)
		return printErr
	default:
		return fmt.Errorf("unknown toolchain command %q", opts.Command)
	}
}

func parseToolchainArgs(args []string) (toolchainOptions, error) {
	opts := toolchainOptions{}
	platformName := ""
	flags := newCLIFlagSet("system toolchain")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.All, "all", false, "")
	flags.BoolVar(&opts.Images, "images", false, "")
	flags.BoolVar(&opts.Strict, "strict", false, "")
	flags.BoolVar(&opts.IncludeSourceLocks, "include-source-locks", false, "")
	flags.StringVar(&opts.Tool, "tool", "", "")
	flags.StringVar(&platformName, "platform", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return toolchainOptions{}, err
	}
	if len(positionals) == 0 {
		return toolchainOptions{}, fmt.Errorf("usage: scenery system toolchain list|sync|verify|path [-o json]")
	}
	opts.Command = positionals[0]
	if len(positionals) > 1 {
		return toolchainOptions{}, fmt.Errorf("unknown argument %q", positionals[1])
	}
	if platformName != "" {
		opts.Platform, err = toolchain.ParsePlatform(platformName)
		if err != nil {
			return toolchainOptions{}, err
		}
	}
	switch opts.Command {
	case "list", "sync", "verify", "path":
	default:
		return toolchainOptions{}, fmt.Errorf("unknown toolchain command %q", opts.Command)
	}
	if opts.Command == "path" && opts.Tool == "" {
		return toolchainOptions{}, fmt.Errorf("scenery system toolchain path requires --tool <name>")
	}
	return opts, nil
}

func renderToolchainStatus(stdout io.Writer, jsonMode bool, includeAll bool, status toolchain.Status) error {
	if jsonMode {
		return writeCLIJSON(stdout, status)
	}
	fmt.Fprintf(stdout, "toolchain %s\n", status.ManifestSHA256)
	fmt.Fprintf(stdout, "store: %s\n", status.StoreDir)
	fmt.Fprintf(stdout, "platform: %s\n", status.Platform)
	for _, artifact := range status.Artifacts {
		if artifact.Kind == "plugin" && !includeAll {
			continue
		}
		line := strings.TrimSpace(artifact.Name + " " + artifact.Version + " " + artifact.Status)
		if includeAll && artifact.ManagedPath != "" {
			line += " " + artifact.ManagedPath
		}
		fmt.Fprintln(stdout, line)
	}
	return nil
}
