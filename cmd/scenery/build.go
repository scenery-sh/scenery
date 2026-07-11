package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
)

func buildCommand(args []string) error {
	outputPath := ""
	appRootFlag := ""
	targetName := ""
	flags := newCLIFlagSet("build")
	flags.StringVar(&outputPath, "output", "", "")
	flags.StringVar(&outputPath, "o", "", "")
	flags.StringVar(&appRootFlag, "app-root", "", "")
	flags.StringVar(&targetName, "target", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return err
	}

	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	if err := validateVNextRuntimePlan(appRoot); err != nil {
		return err
	}
	if outputPath != "" && !filepath.IsAbs(outputPath) {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			return err
		}
	}
	var result *build.Result
	if _, statErr := os.Stat(filepath.Join(appRoot, "scenery.scn")); statErr == nil {
		result, err = build.AppForVNextTarget(appRoot, cfg, targetName, "artifact")
		if err != nil {
			return err
		}
	} else {
		if targetName != "" {
			return fmt.Errorf("--target requires an edition-2027 scenery.scn")
		}
		var ok bool
		result, ok, err = build.LoadReusableBinary(appRoot, cfg)
		if err != nil {
			return err
		}
		if !ok {
			result, err = build.App(appRoot, cfg)
			if err != nil {
				return err
			}
		} else if err := build.WriteLatestBuildManifest(result, "compiled"); err != nil {
			return err
		}
	}
	if outputPath == "" {
		goos := goruntime.GOOS
		if result.VNextTarget != nil {
			goos = result.VNextTarget.Context.GOOS
		}
		outputPath = filepath.Join(appRoot, defaultBuildBinaryNameForGOOS(cfg.Name, goos))
	}
	if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
		goos := goruntime.GOOS
		if result.VNextTarget != nil {
			goos = result.VNextTarget.Context.GOOS
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
	if result.VNextTarget != nil {
		descriptor := build.VNextRuntimeBundlePath(appRoot, result.VNextTarget.Name)
		if _, err := copyBinary(descriptor, outputPath+".scenery.runtime-bundle.v1.json"); err != nil {
			return err
		}
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

func defaultBuildBinaryName(appName string) string {
	return defaultBuildBinaryNameForGOOS(appName, goruntime.GOOS)
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
