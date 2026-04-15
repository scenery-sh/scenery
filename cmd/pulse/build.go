package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	"pulse.dev/internal/app"
	"pulse.dev/internal/build"
)

func buildCommand(args []string) error {
	outputPath := ""
	appRootFlag := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for %s", args[i-1])
			}
			outputPath = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return fmt.Errorf("missing value for --app-root")
			}
			appRootFlag = args[i]
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	if outputPath == "" {
		outputPath = filepath.Join(appRoot, defaultBuildBinaryName(cfg.Name))
	} else if !filepath.IsAbs(outputPath) {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			return err
		}
	}
	if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
		outputPath = filepath.Join(outputPath, defaultBuildBinaryName(cfg.Name))
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	result, err := build.App(appRoot, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(result.Dir)
	}()

	if err := copyBinary(result.Binary, outputPath); err != nil {
		return err
	}
	if err := signBuiltBinaryIfNeeded(outputPath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "pulse: built %s\n", outputPath)
	return nil
}

func copyBinary(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(dst, info.Mode().Perm())
}

func defaultBuildBinaryName(appName string) string {
	if appName == "" {
		appName = "pulse-app"
	}
	if goruntime.GOOS == "windows" && filepath.Ext(appName) != ".exe" {
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
		return fmt.Errorf("pulse: codesign not available for macOS binary signing: %w", err)
	}
	cmd := execCommand(cmdPath, "--force", "--sign", "-", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(output)
		if msg == "" {
			return fmt.Errorf("pulse: failed to codesign built binary: %w", err)
		}
		return fmt.Errorf("pulse: failed to codesign built binary: %w\n%s", err, msg)
	}
	return nil
}

var (
	currentGOOS  = func() string { return goruntime.GOOS }
	execLookPath = exec.LookPath
	execCommand  = func(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
)
