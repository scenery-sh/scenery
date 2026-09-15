package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/nativebuilddriver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	config, configErr := loadConfig()
	realGo := config.RealGo
	if realGo == "" {
		realGo = "/usr/local/go/bin/go"
	}
	args := os.Args[1:]
	if !isTargetBuild(args) {
		return forward(realGo, args)
	}
	if configErr != nil {
		return configErr
	}
	mode, root := config.Mode, config.Root
	if mode == "" || root == "" {
		return forward(realGo, args)
	}
	output := flagValue(args, "-o")
	if output == "" {
		return fmt.Errorf("target build output is absent")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(cwd, output)
	}
	generation, err := nextGeneration(root)
	if err != nil {
		return err
	}
	buildArgv := append([]string{realGo}, args...)
	switch mode {
	case "bootstrap":
		return bootstrap(realGo, root, cwd, output, generation, args, buildArgv)
	case "driver", "compiler":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		sequence, err := generationSequence(generation)
		if err != nil {
			return err
		}
		socket := config.Socket
		if socket == "" {
			socket = filepath.Join(root, "owner.sock")
		}
		response, buildErr := nativebuilddriver.RequestOwner(ctx, socket, nativebuilddriver.OwnerRequest{
			Protocol: nativebuilddriver.ProtocolVersion, Session: config.Session, Workspace: cwd, Sequence: sequence,
			Build: nativebuilddriver.BuildRequest{Workspace: cwd, Output: output, GenerationRoot: generation, BuildArgv: buildArgv, Environment: envpolicy.Environ(), BuildFlags: buildFlags(args), CaptureMode: map[bool]string{true: "retained", false: "full"}[mode == "compiler"]},
		})
		result := response.Result
		if err := writeJSON(filepath.Join(generation, "result.json"), result); err != nil {
			return err
		}
		if buildErr != nil {
			return buildErr
		}
		if response.Error != "" {
			return fmt.Errorf("owner: %s", response.Error)
		}
		if result.Status != "supported_and_rebuilt" {
			return fmt.Errorf("%s: %s", result.Status, result.Reason)
		}
		_ = pruneGenerations(root, generation, 4)
		return nil
	case "stock":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		capture, err := nativebuilddriver.FullCapture(ctx, realGo, cwd, filepath.Join(generation, "snapshot"), envpolicy.Environ(), buildFlags(args))
		if err != nil {
			return err
		}
		started := time.Now()
		err = forward(realGo, args)
		result := map[string]any{"protocol": nativebuilddriver.ProtocolVersion, "status": "stock_go_build", "capture": capture,
			"artifact_build_ms": float64(time.Since(started).Nanoseconds()) / 1e6, "error": fmt.Sprint(err)}
		if err == nil {
			digest, size, digestErr := nativebuilddriver.FileDigest(output)
			if digestErr != nil {
				return digestErr
			}
			result["artifact_digest"], result["executable_bytes"] = digest, size
		}
		if writeErr := writeJSON(filepath.Join(generation, "result.json"), result); writeErr != nil {
			return writeErr
		}
		_ = pruneGenerations(root, generation, 4)
		return err
	default:
		return fmt.Errorf("unsupported wrapper mode %q", mode)
	}
}

type wrapperConfig struct {
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
	RealGo   string `json:"real_go"`
	Mode     string `json:"mode"`
	Root     string `json:"root"`
	Socket   string `json:"socket"`
	Session  string `json:"session"`
}

func loadConfig() (wrapperConfig, error) {
	var result wrapperConfig
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	root := filepath.Dir(filepath.Dir(executable))
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if result.Protocol != nativebuilddriver.ProtocolVersion || result.Version != nativebuilddriver.ProtocolRevision || filepath.Clean(result.Root) != filepath.Clean(root) {
		return result, fmt.Errorf("wrapper configuration identity mismatch")
	}
	return result, nil
}

func bootstrap(realGo, root, cwd, output, generation string, args, buildArgv []string) error {
	recordRoot := filepath.Join(root, "bootstrap")
	if _, err := os.Stat(recordRoot); err == nil {
		return fmt.Errorf("bootstrap already exists at %s", recordRoot)
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, dir := range []string{filepath.Join(recordRoot, "actions"), filepath.Join(recordRoot, "cache"), filepath.Join(recordRoot, "tmp")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	toolExec := filepath.Join(root, "bin", "toolexec")
	if _, err := os.Stat(toolExec); err != nil {
		return fmt.Errorf("bootstrap recorder is unavailable: %w", err)
	}
	bootstrapArgs := append([]string{"build", "-a", "-work", "-toolexec=" + toolExec}, args[1:]...)
	env := overrideEnvironment(envpolicy.Environ(), map[string]string{
		"GOCACHE": recordRoot + "/cache",
		"TMPDIR":  recordRoot + "/tmp",
	})
	started := time.Now()
	cmd := exec.Command(realGo, bootstrapArgs...)
	cmd.Dir, cmd.Env = cwd, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	buildMS := float64(time.Since(started).Nanoseconds()) / 1e6
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	capture, err := nativebuilddriver.FullCapture(ctx, realGo, cwd, filepath.Join(recordRoot, "bootstrap-snapshot"), envpolicy.Environ(), buildFlags(args))
	if err != nil {
		return err
	}
	recipe, err := nativebuilddriver.LoadRecordedRecipe(recordRoot, cwd, capture)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "recipe.json"), recipe); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(recordRoot, "cache"), filepath.Join(recordRoot, "tmp")} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	actionRecords, err := filepath.Glob(filepath.Join(recordRoot, "actions", "action-*", "record.json"))
	if err != nil {
		return err
	}
	artifactDigest, executableBytes, err := nativebuilddriver.FileDigest(output)
	if err != nil {
		return err
	}
	result := map[string]any{"protocol": nativebuilddriver.ProtocolVersion, "status": "bootstrap_complete", "bootstrap_build_ms": buildMS, "capture_ms": capture.DurationMS,
		"package_count": len(capture.Packages), "input_count": len(capture.Files), "retained_bytes": recipe.RetainedBytes, "retention_limit": recipe.RetentionLimit,
		"support_artifact_count": len(recipe.Support), "tool_invocations": len(actionRecords), "compiled_packages": len(recipe.Compiles),
		"build_argv": buildArgv, "artifact": output, "artifact_digest": artifactDigest, "executable_bytes": executableBytes}
	return writeJSON(filepath.Join(generation, "result.json"), result)
}

func pruneGenerations(root, current string, keep int) error {
	entries, err := filepath.Glob(filepath.Join(root, "generations", "generation-*"))
	if err != nil {
		return err
	}
	if len(entries) <= keep {
		return nil
	}
	for _, path := range entries[:len(entries)-keep] {
		if filepath.Clean(path) == filepath.Clean(current) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func overrideEnvironment(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[name]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

func generationSequence(path string) (uint64, error) {
	base := filepath.Base(path)
	value, err := strconv.ParseUint(strings.TrimPrefix(base, "generation-"), 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("invalid generation path %s", path)
	}
	return value, nil
}

func isTargetBuild(args []string) bool {
	return len(args) > 1 && args[0] == "build" && args[len(args)-1] == "./scenery_internal_main"
}

func forward(goTool string, args []string) error {
	cmd := exec.Command(goTool, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, envpolicy.Environ()
	return cmd.Run()
}

func nextGeneration(root string) (string, error) {
	if err := os.MkdirAll(filepath.Join(root, "generations"), 0o700); err != nil {
		return "", err
	}
	counter := filepath.Join(root, "counter")
	value := 0
	if data, err := os.ReadFile(counter); err == nil {
		value, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	value++
	if err := os.WriteFile(counter, []byte(strconv.Itoa(value)+"\n"), 0o600); err != nil {
		return "", err
	}
	path := filepath.Join(root, "generations", fmt.Sprintf("generation-%04d", value))
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func buildFlags(args []string) []string {
	var result []string
	for _, arg := range args[1 : len(args)-1] {
		if strings.HasPrefix(arg, "-tags=") || strings.HasPrefix(arg, "-pgo=") || strings.HasPrefix(arg, "-buildmode=") || strings.HasPrefix(arg, "-gcflags=") || strings.HasPrefix(arg, "-asmflags=") {
			result = append(result, arg)
		}
	}
	return result
}

func flagValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
	}
	return ""
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
