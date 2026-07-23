package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/librarybuild"
)

func buildCommand(args []string) error {
	outputPath := ""
	appRootFlag := ""
	targetName := ""
	libraryName := ""
	libraryVersion := ""
	libraryPlatforms := ""
	envName := ""
	desktop := false
	jsonOutput := false
	flags := newCLIFlagSet("build")
	flags.StringVar(&outputPath, "output", "", "")
	registerJSONOutput(flags, &jsonOutput)
	flags.StringVar(&appRootFlag, "app-root", "", "")
	flags.StringVar(&targetName, "target", "", "")
	flags.StringVar(&libraryName, "lib", "", "")
	flags.StringVar(&libraryVersion, "version", "", "")
	flags.StringVar(&libraryPlatforms, "platform", "", "")
	flags.StringVar(&envName, "env", "", "")
	flags.BoolVar(&desktop, "desktop", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if desktop {
		for _, name := range []string{"target", "lib", "version", "platform", "output"} {
			if cliFlagSet(flags, name) {
				return fmt.Errorf("invalid_request: --desktop cannot be combined with --%s", name)
			}
		}
	} else if cliFlagSet(flags, "env") {
		return fmt.Errorf("invalid_request: --env is only supported with --desktop")
	} else {
		if cliFlagSet(flags, "lib") && strings.TrimSpace(libraryName) == "" {
			return fmt.Errorf("invalid_request: --lib requires a non-empty selector")
		}
		if cliFlagSet(flags, "version") && !cliFlagSet(flags, "lib") {
			return fmt.Errorf("invalid_request: --version requires --lib")
		}
		if cliFlagSet(flags, "platform") && !cliFlagSet(flags, "lib") {
			return fmt.Errorf("invalid_request: --platform requires --lib")
		}
		if cliFlagSet(flags, "lib") && cliFlagSet(flags, "target") {
			return fmt.Errorf("invalid_request: --lib cannot be combined with --target")
		}
	}

	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return fmt.Errorf("invalid_request: resolve app root: %w", err)
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return fmt.Errorf("failed_precondition: discover app root: %w", err)
	}
	if err := validateRuntimePlan(appRoot); err != nil {
		return fmt.Errorf("failed_precondition: validate runtime plan: %w", err)
	}
	if desktop {
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
			return writeCLIJSON(os.Stdout, desktopBuildPayload(result))
		}
		for _, frontend := range result.Frontends {
			for _, artifact := range frontend.Artifacts {
				fmt.Fprintf(os.Stdout, "scenery: built desktop %s at %s\n", frontend.Name, artifact)
			}
		}
		return nil
	}
	if outputPath != "" && !filepath.IsAbs(outputPath) {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			return err
		}
	}
	var result *build.Result
	if libraryName != "" {
		result, err = build.Prepare(appRoot, nil, cfg)
	} else {
		result, err = build.AppForTarget(appRoot, cfg, targetName, "artifact")
	}
	if err != nil {
		return err
	}
	if libraryName != "" {
		specs, err := generate.LibraryBuildSpecs(result.Contract)
		if err != nil {
			return err
		}
		var selected *generate.LibraryBuildSpec
		for index := range specs {
			if specs[index].Name == libraryName || specs[index].Address == libraryName || specs[index].Artifact == libraryName {
				if selected != nil {
					return fmt.Errorf("library selector %q is ambiguous", libraryName)
				}
				selected = &specs[index]
			}
		}
		if selected == nil {
			return fmt.Errorf("library selector %q did not match a declared Go library", libraryName)
		}
		version := strings.TrimSpace(libraryVersion)
		if version == "" {
			version = selected.Version
		}
		if outputPath == "" {
			outputPath = filepath.Join(appRoot, "dist", "libraries", selected.Artifact, version)
		}
		var platforms []string
		if value := strings.TrimSpace(libraryPlatforms); value != "" && value != "all" {
			for _, platform := range strings.Split(value, ",") {
				platforms = append(platforms, strings.TrimSpace(platform))
			}
		}
		libraryResult, err := librarybuild.Build(context.Background(), librarybuild.Options{
			Workspace: result.Dir, OutputDir: outputPath, Spec: *selected,
			Version: version, Platforms: platforms,
		})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.library.build.result", map[string]any{
				"library": selected.Name, "version": version,
				"manifest_path": libraryResult.ManifestPath, "artifacts": libraryResult.Manifest.Artifacts,
			}))
		}
		fmt.Fprintf(os.Stdout, "scenery: built library %s at %s\n", selected.Name, libraryResult.ManifestPath)
		return nil
	}
	if outputPath == "" {
		goos := goruntime.GOOS
		if result.Target != nil {
			goos = result.Target.Context.GOOS
		}
		outputPath = filepath.Join(appRoot, defaultBuildBinaryNameForGOOS(cfg.Name, goos))
	}
	if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
		goos := goruntime.GOOS
		if result.Target != nil {
			goos = result.Target.Context.GOOS
		}
		outputPath = filepath.Join(outputPath, defaultBuildBinaryNameForGOOS(cfg.Name, goos))
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	copied, err := copyBinary(result.Binary, outputPath)
	if err != nil {
		return err
	}
	if copied {
		if err := signBuiltBinaryIfNeeded(outputPath); err != nil {
			return err
		}
	}
	descriptorPath := ""
	if result.Target != nil {
		descriptor := build.RuntimeBundlePath(appRoot, result.Target.Name)
		descriptorPath = outputPath + ".scenery.runtime-bundle.json"
		if _, err := copyBinary(descriptor, descriptorPath); err != nil {
			return err
		}
	}
	if jsonOutput {
		return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.build.result", map[string]any{
			"output_path":     outputPath,
			"descriptor_path": descriptorPath,
			"copied":          copied,
		}))
	}
	fmt.Fprintf(os.Stdout, "scenery: built %s\n", outputPath)
	return nil
}

func copyBinary(src, dst string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}

	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return false, err
	}
	if same, err := sameFileContent(in, info, dst); err != nil {
		return false, err
	} else if same {
		if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return false, err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return false, err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	return true, os.Chmod(dst, info.Mode().Perm())
}

func sameFileContent(src *os.File, srcInfo os.FileInfo, dst string) (bool, error) {
	dstFile, err := os.Open(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer dstFile.Close()
	dstInfo, err := dstFile.Stat()
	if err != nil {
		return false, err
	}
	if dstInfo.Size() != srcInfo.Size() {
		return false, nil
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	equal, err := readersEqual(src, dstFile)
	if err != nil {
		return false, err
	}
	return equal, nil
}

func readersEqual(left, right io.Reader) (bool, error) {
	leftBuf := make([]byte, 256*1024)
	rightBuf := make([]byte, 256*1024)
	for {
		leftN, leftErr := left.Read(leftBuf)
		rightN, rightErr := right.Read(rightBuf)
		if leftN != rightN || !bytes.Equal(leftBuf[:leftN], rightBuf[:rightN]) {
			return false, nil
		}
		if leftErr == io.EOF && rightErr == io.EOF {
			return true, nil
		}
		if leftErr != nil && leftErr != io.EOF {
			return false, leftErr
		}
		if rightErr != nil && rightErr != io.EOF {
			return false, rightErr
		}
	}
}

func defaultBuildBinaryNameForGOOS(appName, goos string) string {
	if appName == "" {
		appName = "scenery-app"
	}
	if goos == "windows" && filepath.Ext(appName) != ".exe" {
		return appName + ".exe"
	}
	return appName
}

func signBuiltBinaryIfNeeded(path string) error {
	if currentGOOS() != "darwin" {
		return nil
	}
	cmdPath, err := execLookPath("codesign")
	if err != nil {
		return fmt.Errorf("scenery: codesign not available for macOS binary signing: %w", err)
	}
	cmd := execCommand(cmdPath, "--force", "--sign", "-", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(output)
		if msg == "" {
			return fmt.Errorf("scenery: failed to codesign built binary: %w", err)
		}
		return fmt.Errorf("scenery: failed to codesign built binary: %w\n%s", err, msg)
	}
	return nil
}

var (
	currentGOOS  = func() string { return goruntime.GOOS }
	execLookPath = exec.LookPath
	execCommand  = func(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
)
