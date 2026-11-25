package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
)

func buildCommand(out io.Writer, args []string) error {
	outputPath := ""
	appRootFlag := ""
	targetName := ""
	envName := ""
	desktop := false
	development := false
	verifyGeneration := false
	jsonOutput := false
	flags := newCLIFlagSet("build")
	flags.StringVar(&outputPath, "output", "", "")
	registerJSONOutput(flags, &jsonOutput)
	flags.StringVar(&appRootFlag, "app-root", "", "")
	flags.StringVar(&targetName, "target", "", "")
	flags.StringVar(&envName, "env", "", "")
	flags.BoolVar(&desktop, "desktop", false, "")
	flags.BoolVar(&development, "development", false, "")
	flags.BoolVar(&verifyGeneration, "verify-generation", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return fmt.Errorf("invalid_request: %w", err)
	}
	if verifyGeneration && (!development || desktop) {
		return fmt.Errorf("invalid_request: --verify-generation requires a --development application build")
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
	if compiled, compileErr := compiler.Compile(appRoot); compileErr == nil && usesWorkerBundle(compiled) {
		if desktop || verifyGeneration {
			return usageErrorf("worker bundles do not support desktop or Go generation verification")
		}
		role := "artifact"
		if development {
			role = "development"
		}
		if targetName != "" {
			// Fail before any worker is built for a target that does not exist.
			if _, err := compiler.ResolveGoBuildTarget(compiled, targetName, role); err != nil {
				return goTargetSelectionCLIError(err)
			}
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
		if err := writeWorkerBundle(bundle, outputPath); err != nil {
			return err
		}
		if jsonOutput {
			return writeCLIJSON(out, workerBundleBuildResult(outputPath))
		}
		_, err = fmt.Fprintln(out, outputPath)
		return err
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
			return writeCLIJSON(out, desktopBuildPayload(result))
		}
		for _, frontend := range result.Frontends {
			for _, artifact := range frontend.Artifacts {
				_, _ = fmt.Fprintf(out, "scenery: built desktop %s at %s\n", frontend.Name, artifact)
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
	if development {
		result, err = build.AppForTarget(appRoot, cfg, targetName)
	} else {
		result, err = build.BuildArtifactForTarget(appRoot, cfg, targetName)
	}
	if err != nil {
		return goTargetSelectionCLIError(err)
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
	var candidate *build.CandidateIdentity
	verificationModulePath := ""
	verificationModuleDigest := ""
	if verifyGeneration {
		identity, err := build.VerifyCandidate(context.Background(), appRoot, result.Target.Name, descriptorPath)
		if err != nil {
			return fmt.Errorf("failed_precondition: built generation is not verified: %w", err)
		}
		candidate = &identity
		verificationModulePath = outputPath + ".scenery.verify.ts"
		if err := build.WriteRuntimeVerificationModule(verificationModulePath); err != nil {
			return err
		}
		verificationModuleDigest = build.RuntimeVerificationModuleDigest()
	}
	if jsonOutput {
		return writeCLIJSON(out, withCLIPayloadIdentity("scenery.build.result", map[string]any{
			"output_path":                outputPath,
			"descriptor_path":            descriptorPath,
			"copied":                     copied,
			"candidate_identity":         candidate,
			"verification_module_path":   verificationModulePath,
			"verification_module_digest": verificationModuleDigest,
		}))
	}
	_, _ = fmt.Fprintf(out, "scenery: built %s\n", outputPath)
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

// workerBundleBuildResult reports a worker bundle through the declared
// application build result. output_path is the bundle directory, which a
// successful build always writes anew; the runtime descriptor, candidate
// identity and verification module of a single Go executable do not exist.
func workerBundleBuildResult(outputPath string) map[string]any {
	return withCLIPayloadIdentity("scenery.build.result", map[string]any{
		"output_path":                outputPath,
		"descriptor_path":            "",
		"copied":                     true,
		"candidate_identity":         nil,
		"verification_module_path":   "",
		"verification_module_digest": "",
	})
}

func copyBinary(src, dst string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}

	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer func() { _ = in.Close() }()

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
	defer func() { _ = dstFile.Close() }()
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
