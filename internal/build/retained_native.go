package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/nativebuilddriver"
)

const (
	retainedNativeStateProtocol = "scenery.retained-go-compiler"
	retainedNativeStateRevision = 1
	retainedNativeCacheVersion  = "v1"
)

type retainedNativeCurrent struct {
	Protocol           string `json:"protocol"`
	Revision           int    `json:"revision"`
	Workspace          string `json:"workspace"`
	RecipePath         string `json:"recipe_path"`
	RecorderExecutable string `json:"recorder_executable"`
	RecorderDigest     string `json:"recorder_digest"`
}

var retainedNativeExecutable = func() (string, bool) {
	path, err := os.Executable()
	if err != nil || strings.HasSuffix(filepath.Base(path), ".test") {
		return "", false
	}
	path, err = filepath.EvalSymlinks(path)
	return path, err == nil
}

var runRetainedNativeCompiler = runRetainedNativeCompilerContext

func compileApplicationBinaryContext(ctx context.Context, result *Result) error {
	if shouldUseRetainedNativeCompiler(result) {
		if executable, ok := retainedNativeExecutable(); ok {
			return runRetainedNativeCompiler(ctx, result, executable)
		}
	}
	return runSharedGoBuildContext(ctx, result)
}

func shouldUseRetainedNativeCompiler(result *Result) bool {
	return result != nil && result.Target != nil && result.Target.Role == "development" && !result.Ephemeral && !result.ProductionAssets
}

func runRetainedNativeCompilerContext(ctx context.Context, result *Result, recorderExecutable string) error {
	root, err := retainedNativeRoot(result.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	recipe, loadErr := loadRetainedNativeRecipe(root, result.Dir, recorderExecutable)
	bootstrapReason := "missing_recipe"
	if loadErr == nil {
		buildResult, err := runRetainedNativeRecipe(ctx, root, result, recipe)
		if err != nil {
			return err
		}
		if buildResult.Status == "supported_and_rebuilt" {
			return nil
		}
		if buildResult.Status != "needs_rebootstrap" {
			return fmt.Errorf("retained compiler returned %s: %s", buildResult.Status, buildResult.Reason)
		}
		RecordStep(ctx, Step{Name: "build.backend", StartedAt: time.Now(), Cache: "miss", Reason: "retained_recipe_" + buildResult.Reason, OK: true})
		bootstrapReason = "incompatible_recipe"
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		bootstrapReason = "invalid_recipe"
	}
	return bootstrapRetainedNativeRecipe(ctx, root, result, recorderExecutable, bootstrapReason)
}

func runRetainedNativeRecipe(ctx context.Context, root string, result *Result, recipe *nativebuilddriver.Recipe) (nativebuilddriver.BuildResult, error) {
	started := time.Now()
	generationRoot := filepath.Join(root, "generations")
	if err := os.MkdirAll(generationRoot, 0o700); err != nil {
		return nativebuilddriver.BuildResult{}, err
	}
	if err := pruneRetainedNativeDirectories(generationRoot, "", 0); err != nil {
		return nativebuilddriver.BuildResult{}, err
	}
	generation, err := os.MkdirTemp(generationRoot, "generation-")
	if err != nil {
		return nativebuilddriver.BuildResult{}, err
	}
	candidate := filepath.Join(generation, "candidate")
	defer func() { _ = os.RemoveAll(generation) }()
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return nativebuilddriver.BuildResult{}, err
	}
	buildResult, buildErr := recipe.Build(ctx, nativebuilddriver.BuildRequest{
		Workspace:      result.Dir,
		Output:         candidate,
		GenerationRoot: generation,
		BuildArgv:      append([]string{"go"}, goBuildArgs(result.Binary, effectiveGoBuildFlags(result))...),
		Environment:    result.GoEnvironment,
		BuildFlags:     append([]string(nil), result.GoBuildFlags...),
		CaptureMode:    "retained",
	})
	releaseSlot()
	recordRetainedNativeSteps(ctx, started, buildResult, buildErr)
	if buildErr != nil || buildResult.Status != "supported_and_rebuilt" {
		return buildResult, buildErr
	}
	if err := publishRetainedNativeBinary(candidate, result.Binary); err != nil {
		return buildResult, fmt.Errorf("publish retained application binary: %w", err)
	}
	if err := recordBuildArtifact(ctx, result.Binary, "retained_recipe"); err != nil {
		return buildResult, err
	}
	RecordStep(ctx, Step{Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "hit", Reason: "retained_compiler", OK: true, Actions: buildResult.ToolInvocations, PackagesRebuilt: buildResult.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: buildResult.ExecutableBytes})
	return buildResult, nil
}

func bootstrapRetainedNativeRecipe(ctx context.Context, root string, result *Result, recorderExecutable, reason string) error {
	started := time.Now()
	recipesRoot := filepath.Join(root, "recipes")
	generationsRoot := filepath.Join(root, "generations")
	for _, directory := range []string{recipesRoot, generationsRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	if err := pruneRetainedNativeDirectories(generationsRoot, "", 0); err != nil {
		return err
	}
	recipeRoot, err := os.MkdirTemp(recipesRoot, "recipe-")
	if err != nil {
		return err
	}
	keepRecipe := false
	defer func() {
		if !keepRecipe {
			_ = os.RemoveAll(recipeRoot)
		}
	}()
	recordRoot := filepath.Join(recipeRoot, "bootstrap")
	for _, directory := range []string{filepath.Join(recordRoot, "actions"), filepath.Join(recordRoot, "cache"), filepath.Join(recordRoot, "tmp")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	generation, err := os.MkdirTemp(generationsRoot, "bootstrap-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	candidate := filepath.Join(generation, "candidate")
	args := goBuildArgs(candidate, effectiveGoBuildFlags(result))
	args = append([]string{"build", "-a", "-work", "-toolexec=" + retainedNativeToolExecCommand(recorderExecutable, recordRoot)}, args[1:]...)
	environment := retainedNativeEnvironment(result.GoEnvironment, map[string]string{"GOCACHE": filepath.Join(recordRoot, "cache"), "TMPDIR": filepath.Join(recordRoot, "tmp")})
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return err
	}
	commandStarted := time.Now()
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir, command.Env = result.Dir, environment
	output, runErr := command.CombinedOutput()
	releaseSlot()
	RecordStep(ctx, Step{Name: "go.command", StartedAt: commandStarted, Duration: time.Since(commandStarted), Cache: "miss", Reason: "retained_bootstrap", OK: runErr == nil, Actions: 1})
	if runErr != nil {
		return fmt.Errorf("bootstrap retained compiler recipe: %w\n%s", runErr, output)
	}
	captureStarted := time.Now()
	capture, err := nativebuilddriver.FullCapture(ctx, "go", result.Dir, filepath.Join(recordRoot, "bootstrap-snapshot"), result.GoEnvironment, result.GoBuildFlags)
	RecordStep(ctx, Step{Name: "go.recipe_capture", StartedAt: captureStarted, Duration: time.Since(captureStarted), Cache: "miss", Reason: "stock_package_loading_and_input_snapshot", OK: err == nil, Actions: len(capture.Packages)})
	if err != nil {
		return err
	}
	recipeStarted := time.Now()
	recipe, err := nativebuilddriver.LoadRecordedRecipe(recordRoot, result.Dir, capture)
	actions := 0
	if recipe != nil {
		actions = len(recipe.Compiles) + 1
	}
	RecordStep(ctx, Step{Name: "go.action_recipe", StartedAt: recipeStarted, Duration: time.Since(recipeStarted), Cache: "miss", Reason: "captured_stock_tool_actions", OK: err == nil, Actions: actions})
	if err != nil {
		return err
	}
	recipePath := filepath.Join(recipeRoot, "recipe.json")
	encoded, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(recipePath, append(encoded, '\n'), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	if err := publishRetainedNativeBinary(candidate, result.Binary); err != nil {
		return fmt.Errorf("publish bootstrap application binary: %w", err)
	}
	recorderDigest, _, err := nativebuilddriver.FileDigest(recorderExecutable)
	if err != nil {
		return err
	}
	current := retainedNativeCurrent{Protocol: retainedNativeStateProtocol, Revision: retainedNativeStateRevision, Workspace: result.Dir, RecipePath: recipePath, RecorderExecutable: recorderExecutable, RecorderDigest: recorderDigest}
	encoded, err = json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(root, "current.json"), append(encoded, '\n'), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	keepRecipe = true
	_ = os.RemoveAll(filepath.Join(recordRoot, "cache"))
	_ = os.RemoveAll(filepath.Join(recordRoot, "tmp"))
	_ = pruneRetainedNativeDirectories(recipesRoot, recipeRoot, 1)
	if err := recordBuildArtifact(ctx, result.Binary, "retained_bootstrap"); err != nil {
		return err
	}
	RecordStep(ctx, Step{Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "miss", Reason: reason, OK: true, Actions: len(recipe.Compiles) + 1, PackagesRebuiltAvailable: true})
	return nil
}

func loadRetainedNativeRecipe(root, workspace, recorderExecutable string) (*nativebuilddriver.Recipe, error) {
	data, err := os.ReadFile(filepath.Join(root, "current.json"))
	if err != nil {
		return nil, err
	}
	var current retainedNativeCurrent
	if err := decodeRetainedNativeJSON(data, &current); err != nil {
		return nil, fmt.Errorf("decode retained compiler state: %w", err)
	}
	if current.Protocol != retainedNativeStateProtocol || current.Revision != retainedNativeStateRevision || !sameRetainedNativePath(current.Workspace, workspace) || !sameRetainedNativePath(current.RecorderExecutable, recorderExecutable) {
		return nil, fmt.Errorf("retained compiler state identity changed")
	}
	relative, err := filepath.Rel(root, current.RecipePath)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("retained compiler recipe escapes its state root")
	}
	digest, _, err := nativebuilddriver.FileDigest(recorderExecutable)
	if err != nil || digest != current.RecorderDigest {
		return nil, fmt.Errorf("retained compiler recorder identity changed: %v", err)
	}
	data, err = os.ReadFile(current.RecipePath)
	if err != nil {
		return nil, err
	}
	var recipe nativebuilddriver.Recipe
	if err := decodeRetainedNativeJSON(data, &recipe); err != nil {
		return nil, fmt.Errorf("decode retained compiler recipe: %w", err)
	}
	if !sameRetainedNativePath(recipe.Workspace, workspace) {
		return nil, fmt.Errorf("retained compiler recipe workspace changed")
	}
	if err := recipe.Validate(); err != nil {
		return nil, err
	}
	return &recipe, nil
}

func decodeRetainedNativeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func recordRetainedNativeSteps(ctx context.Context, started time.Time, result nativebuilddriver.BuildResult, err error) {
	offset := time.Duration(0)
	ok := err == nil && result.Status == "supported_and_rebuilt"
	record := func(name string, milliseconds float64, cache, reason string) {
		duration := time.Duration(milliseconds * float64(time.Millisecond))
		RecordStep(ctx, Step{Name: name, StartedAt: started.Add(offset), Duration: duration, Cache: cache, Reason: reason, OK: ok})
		offset += duration
	}
	record("go.retained_archive", result.ArchiveMS, "content_stamp", "retained_archive_identity")
	record("go.retained_support", result.SupportMS, "content_stamp", "retained_importcfg_identity")
	record("go.input_discovery", result.CaptureMS, "retained", "complete_retained_domain")
	record("go.input_validation", result.ValidationMS, "retained_recipe", "recipe_compatibility")
	record("go.action_plan", result.PlanningMS, "retained_recipe", "changed_packages_and_consumers")
	record("go.compile", result.CompileMS, "retained_recipe", "changed_packages_and_consumers")
	record("go.link", result.LinkMS, "retained_recipe", "application_executable")
	RecordStep(ctx, Step{Name: "go.command", StartedAt: started, Duration: time.Since(started), Cache: "retained_recipe", Reason: "build", OK: ok, Actions: result.ToolInvocations, PackagesRebuilt: result.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: result.ExecutableBytes})
}

func retainedNativeToolExecCommand(executable, root string) string {
	return strconv.Quote(executable) + " internal native-build-toolexec --root " + strconv.Quote(root)
}

func retainedNativeRoot(workspace string) (string, error) {
	cacheRoot, err := CacheRoot()
	if err != nil {
		return "", err
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(absWorkspace); canonicalErr == nil {
		absWorkspace = canonical
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absWorkspace)))
	return filepath.Join(cacheRoot, "build", "retained-go-compiler", retainedNativeCacheVersion, hex.EncodeToString(digest[:16])), nil
}

func sameRetainedNativePath(left, right string) bool {
	canonical := func(path string) string {
		if evaluated, err := filepath.EvalSymlinks(path); err == nil {
			path = evaluated
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		return filepath.Clean(path)
	}
	return canonical(left) == canonical(right)
}

func publishRetainedNativeBinary(candidate, destination string) error {
	data, err := os.ReadFile(candidate)
	if err != nil {
		return err
	}
	return writeExecutableAtomically(destination, data)
}

func retainedNativeEnvironment(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	sort.Strings(result)
	return result
}

func retainedNativeLinkSlot(ctx context.Context, result *Result) (func(), error) {
	root, err := sharedBinaryRoot()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(result.Dir))
	return acquireSharedBinarySlotObserved(ctx, root, hex.EncodeToString(digest[:]))
}

func pruneRetainedNativeDirectories(root, current string, keep int) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	values := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		values = append(values, candidate{path: filepath.Join(root, entry.Name()), modTime: info.ModTime()})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].modTime.Before(values[j].modTime) })
	removeCount := len(values) - keep
	for _, value := range values {
		if removeCount <= 0 {
			break
		}
		if filepath.Clean(value.path) == filepath.Clean(current) {
			continue
		}
		if err := os.RemoveAll(value.path); err != nil {
			return err
		}
		removeCount--
	}
	return nil
}
