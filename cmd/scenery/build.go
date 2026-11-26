package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
)

// buildCommand builds the application's portable Rust host bundle, or with
// --desktop the Tauri desktop shells of its frontends.
func buildCommand(out io.Writer, args []string) error {
	outputPath := ""
	appRootFlag := ""
	targetName := ""
	envName := ""
	desktop := false
	development := false
	jsonOutput := false
	flags := newCLIFlagSet("build")
	flags.StringVar(&outputPath, "output", "", "")
	registerJSONOutput(flags, &jsonOutput)
	flags.StringVar(&appRootFlag, "app-root", "", "")
	flags.StringVar(&targetName, "target", "", "")
	flags.StringVar(&envName, "env", "", "")
	flags.BoolVar(&desktop, "desktop", false, "")
	flags.BoolVar(&development, "development", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if desktop {
		for _, name := range []string{"target", "output", "development"} {
			if cliFlagSet(flags, name) {
				return fmt.Errorf("invalid_request: --desktop cannot be combined with --%s", name)
			}
		}
	} else if cliFlagSet(flags, "env") {
		return fmt.Errorf("invalid_request: --env is only supported with --desktop")
	}

	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return fmt.Errorf("invalid_request: resolve app root: %w", err)
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return fmt.Errorf("failed_precondition: discover app root: %w", err)
	}
	if err := build.VerifyPrebuiltFramework(appRoot, cfg.FrameworkVersion, runningFrameworkVersion()); err != nil {
		return &codedCLIError{err: err, code: 3}
	}
	if desktop {
		return buildDesktopCommand(out, appRoot, cfg, envName, jsonOutput)
	}
	compiled, err := compiler.Compile(appRoot)
	if err != nil {
		return err
	}
	if !compiled.Valid() {
		return invalidGraphError(compiled)
	}
	role := "artifact"
	if development {
		role = "development"
	}
	if err := validateWorkerBundleTargetPlatform(compiled, role, targetName); err != nil {
		return goTargetSelectionCLIError(err)
	}
	bundle, err := buildWorkerBundle(context.Background(), compiled, role, targetName, false)
	if err != nil {
		return goTargetSelectionCLIError(err)
	}
	frontend, err := selectWorkerFrontend(appRoot, cfg, compiled)
	if err != nil {
		return err
	}
	if frontend != nil {
		bundle.Frontend, err = buildWorkerFrontend(context.Background(), compiled, *frontend)
		if err != nil {
			return err
		}
	}
	if outputPath == "" {
		outputPath = filepath.Join(appRoot, "dist", "bundle")
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	if err := writeWorkerBundle(context.Background(), bundle, outputPath); err != nil {
		return err
	}
	if jsonOutput {
		return writeCLIJSON(out, withCLIPayloadIdentity("scenery.build.result", map[string]any{"output_path": outputPath}))
	}
	_, err = fmt.Fprintln(out, outputPath)
	return err
}

// buildDesktopCommand builds each desktop frontend and bundles its Tauri
// shell for the selected environment.
func buildDesktopCommand(out io.Writer, appRoot string, cfg app.Config, envName string, jsonOutput bool) error {
	resolvedEnv, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	cfg.Frontends = resolvedEnv.Frontends
	result, err := buildDesktop(context.Background(), appRoot, cfg, resolvedEnv, os.Stderr)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeCLIJSON(out, desktopBuildPayload(result))
	}
	for _, frontend := range result.Frontends {
		for _, artifact := range frontend.Artifacts {
			_, _ = fmt.Fprintf(out, "scenery: built desktop %s at %s\n", frontend.Name, artifact)
		}
	}
	return nil
}

// goTargetSelectionCLIError reports a go_target the application does not
// declare as an invalid request; every other error passes through.
func goTargetSelectionCLIError(err error) error {
	if _, ok := errors.AsType[*compiler.GoTargetSelectionError](err); ok {
		return &codedCLIError{err: err, code: 2}
	}
	return err
}

var (
	execLookPath = exec.LookPath
)
