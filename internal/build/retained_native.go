package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	gobuild "go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/nativebuilddriver"
)

const (
	retainedNativeStateProtocol = "scenery.retained-go-compiler"
	retainedNativeStateRevision = 2
	retainedNativeCacheVersion  = "v2"
)

type retainedNativeCurrent struct {
	Protocol           string `json:"protocol"`
	Revision           int    `json:"revision"`
	Workspace          string `json:"workspace"`
	RecipePath         string `json:"recipe_path"`
	RecorderExecutable string `json:"recorder_executable"`
	RecorderDigest     string `json:"recorder_digest"`
}

type retainedNativeLoaded struct {
	recipe     *nativebuilddriver.Recipe
	recipePath string
}

type retainedNativeCacheEntry struct {
	mu     sync.Mutex
	loaded *retainedNativeLoaded
}

func (entry *retainedNativeCacheEntry) load(load func() (*retainedNativeLoaded, error)) (*retainedNativeLoaded, error) {
	if entry.loaded != nil {
		return entry.loaded, nil
	}
	loaded, err := load()
	if err == nil {
		entry.loaded = loaded
	}
	return loaded, err
}

var retainedNativeRecipeCache sync.Map

var retainedNativeExecutable = func() (string, bool) {
	path, err := os.Executable()
	if err != nil || strings.HasSuffix(filepath.Base(path), ".test") {
		return "", false
	}
	path, err = filepath.EvalSymlinks(path)
	return path, err == nil
}

var runRetainedNativeCompiler = runRetainedNativeCompilerContext

var errRetainedNativeGraphRefreshNeedsBootstrap = errors.New("retained graph refresh cannot preserve configured compiler flags")

func compileApplicationBinaryContext(ctx context.Context, result *Result) error {
	if shouldUseRetainedNativeCompiler(result) {
		if benchmarkStockGoBuild {
			return runSharedGoBuildContext(ctx, result)
		}
		executable, ok := retainedNativeExecutable()
		if !ok {
			// Package tests inject the Go runner and execute from a .test binary,
			// which cannot also serve as Scenery's toolexec recorder. This branch
			// is unreachable from a shipped Scenery executable.
			if current, err := os.Executable(); err == nil && strings.HasSuffix(filepath.Base(current), ".test") {
				return runSharedGoBuildContext(ctx, result)
			}
			return fmt.Errorf("retained development compiler executable is unavailable")
		}
		return runRetainedNativeCompiler(ctx, result, executable)
	}
	return runSharedGoBuildContext(ctx, result)
}

func stockGoDriverPath() string {
	return filepath.Join(gobuild.Default.GOROOT, "bin", "go")
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
	cacheKey := root + "\x00" + recorderExecutable
	entryValue, _ := retainedNativeRecipeCache.LoadOrStore(cacheKey, &retainedNativeCacheEntry{})
	entry := entryValue.(*retainedNativeCacheEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	loaded, loadErr := entry.load(func() (*retainedNativeLoaded, error) {
		return loadRetainedNativeRecipe(root, result.Dir, recorderExecutable)
	})
	bootstrapReason := "missing_recipe"
	if loadErr == nil {
		buildResult, next, err := runRetainedNativeRecipe(ctx, root, result, loaded)
		if err != nil {
			return err
		}
		if buildResult.Status == "supported_and_rebuilt" {
			loaded.recipe = next
			return nil
		}
		if buildResult.Status != "needs_rebootstrap" {
			return fmt.Errorf("retained compiler returned %s: %s", buildResult.Status, buildResult.Reason)
		}
		RecordStep(ctx, Step{Name: "build.backend", StartedAt: time.Now(), Cache: "miss", Reason: "retained_recipe_" + buildResult.Reason, OK: true})
		if shouldRefreshRetainedNativeGraph(buildResult.Reason) {
			refreshed, err := refreshRetainedNativeRecipe(ctx, root, result, recorderExecutable, loaded, buildResult.Reason)
			if errors.Is(err, nativebuilddriver.ErrIncompleteRecordedRecipe) || errors.Is(err, errRetainedNativeGraphRefreshNeedsBootstrap) {
				refreshed, err = bootstrapRetainedNativeRecipe(ctx, root, result, recorderExecutable, "graph_refresh_incomplete")
			}
			if err != nil {
				return err
			}
			entry.loaded = refreshed
			return nil
		}
		bootstrapReason = "incompatible_recipe"
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		bootstrapReason = "invalid_recipe"
	}
	loaded, err = bootstrapRetainedNativeRecipe(ctx, root, result, recorderExecutable, bootstrapReason)
	if err == nil {
		entry.loaded = loaded
	}
	return err
}

func shouldRefreshRetainedNativeGraph(reason string) bool {
	switch reason {
	case "package_membership_changed", "package_selection_changed", "input_added", "input_missing",
		"unsupported_input_changed", "unsupported_external_input_changed", "unsupported_native_or_unowned_change",
		"compile_recipe_missing", "unsupported_native_action_frontier":
		return true
	default:
		return false
	}
}

func refreshRetainedNativeRecipe(ctx context.Context, root string, result *Result, recorderExecutable string, loaded *retainedNativeLoaded, reason string) (*retainedNativeLoaded, error) {
	started := time.Now()
	generationsRoot := filepath.Join(root, "generations")
	if err := os.MkdirAll(generationsRoot, 0o700); err != nil {
		return nil, err
	}
	generation, err := os.MkdirTemp(generationsRoot, "refresh-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	recordRoot := filepath.Join(generation, "record")
	for _, directory := range []string{filepath.Join(recordRoot, "actions"), filepath.Join(recordRoot, "tmp")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}
	captureStarted := time.Now()
	goTool := stockGoDriverPath()
	capture, err := nativebuilddriver.FullCapture(ctx, goTool, result.Dir, filepath.Join(recordRoot, "snapshot"), result.GoEnvironment, result.GoBuildFlags)
	RecordStep(ctx, Step{Name: "go.recipe_capture", StartedAt: captureStarted, Duration: time.Since(captureStarted), Cache: "miss", Reason: "graph_refresh_package_loading", OK: err == nil, Actions: len(capture.Packages)})
	if err != nil {
		return nil, err
	}
	candidate := filepath.Join(generation, "candidate")
	refreshPackages := loaded.recipe.GraphRefreshPackages(capture)
	refreshFlags, ok := retainedNativeGraphRefreshFlags(refreshPackages, generation, result.GoEnvironment, effectiveGoBuildFlags(result))
	if !ok {
		return nil, errRetainedNativeGraphRefreshNeedsBootstrap
	}
	args := goBuildArgs(candidate, effectiveGoBuildFlags(result))
	prefix := append([]string{"build", "-work", "-toolexec=" + retainedNativeToolExecCommand(recorderExecutable, recordRoot)}, refreshFlags...)
	args = append(prefix, args[1:]...)
	environment := retainedNativeEnvironment(result.GoEnvironment, map[string]string{"TMPDIR": filepath.Join(recordRoot, "tmp")})
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return nil, err
	}
	commandStarted := time.Now()
	command := exec.CommandContext(ctx, goTool, args...)
	command.Dir, command.Env = result.Dir, environment
	output, runErr := command.CombinedOutput()
	releaseSlot()
	RecordStep(ctx, Step{Name: "go.command", StartedAt: commandStarted, Duration: time.Since(commandStarted), Cache: "shared", Reason: "retained_graph_refresh", OK: runErr == nil, Actions: 1})
	if runErr != nil {
		return nil, fmt.Errorf("refresh retained compiler graph: %w\n%s", runErr, output)
	}
	inputCheckStarted := time.Now()
	inputCheckErr := capture.ValidateCurrentStamps()
	RecordStep(ctx, Step{Name: "go.refresh_input_check", StartedAt: inputCheckStarted, Duration: time.Since(inputCheckStarted), Cache: "captured_stamps", Reason: "reject_refresh_input_race", OK: inputCheckErr == nil})
	if inputCheckErr != nil {
		return nil, inputCheckErr
	}
	stateRoot := filepath.Join(filepath.Dir(loaded.recipePath), "retained")
	mergeStarted := time.Now()
	next, err := nativebuilddriver.RefreshRecordedRecipe(loaded.recipe, recordRoot, capture, stateRoot)
	RecordStep(ctx, Step{Name: "go.action_recipe", StartedAt: mergeStarted, Duration: time.Since(mergeStarted), Cache: "merge", Reason: "warm_graph_refresh", OK: err == nil, Actions: len(nextCompiles(next)) + 1})
	if err != nil {
		return nil, err
	}
	if err := writeRetainedNativeRecipe(loaded.recipePath, next); err != nil {
		return nil, err
	}
	artifactDigest, executableBytes, err := nativebuilddriver.FileDigest(candidate)
	if err != nil {
		return nil, err
	}
	if err := publishRetainedNativeBinary(candidate, result.Binary); err != nil {
		return nil, fmt.Errorf("publish graph-refresh application binary: %w", err)
	}
	result.ArtifactDigest, result.ExecutableBytes = artifactDigest, executableBytes
	if err := recordBuildArtifact(ctx, result.Binary, "retained_graph_refresh"); err != nil {
		return nil, err
	}
	RecordStep(ctx, Step{Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "refresh", Reason: "retained_graph_refresh_" + reason, OK: true, Actions: 1, PackagesRebuiltAvailable: false})
	return &retainedNativeLoaded{recipe: next, recipePath: loaded.recipePath}, nil
}

// retainedNativeGraphRefreshFlags makes cmd/go invoke only the changed package
// frontier even when those actions already exist in the shared Go cache. The
// neutral identity mapping leaves source paths unchanged. Configured compiler
// flags fall back to complete bootstrap because composing exact patterns could
// replace user flag semantics.
func retainedNativeGraphRefreshFlags(packages []string, token string, environment, buildFlags []string) ([]string, bool) {
	if len(packages) == 0 || hasRetainedNativeCompilerFlags(buildFlags) {
		return nil, false
	}
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, "GOFLAGS="); found && hasRetainedNativeCompilerFlags(strings.Fields(value)) {
			return nil, false
		}
	}
	selected := append([]string(nil), packages...)
	for _, importPath := range selected {
		if importPath == "" {
			return nil, false
		}
	}
	sort.Strings(selected)
	mapping := filepath.Clean(token) + "=>" + filepath.Clean(token)
	flags := make([]string, 0, len(selected))
	for _, importPath := range selected {
		flags = append(flags, "-gcflags="+importPath+"=-trimpath="+mapping)
	}
	return flags, true
}

func hasRetainedNativeCompilerFlags(flags []string) bool {
	for _, flag := range flags {
		if flag == "-gcflags" || strings.HasPrefix(flag, "-gcflags=") {
			return true
		}
	}
	return false
}

func nextCompiles(recipe *nativebuilddriver.Recipe) map[string]*nativebuilddriver.CompileAction {
	if recipe == nil {
		return nil
	}
	return recipe.Compiles
}

func runRetainedNativeRecipe(ctx context.Context, root string, result *Result, loaded *retainedNativeLoaded) (nativebuilddriver.BuildResult, *nativebuilddriver.Recipe, error) {
	started := time.Now()
	recipe := loaded.recipe
	generationRoot := filepath.Join(root, "generations")
	if err := os.MkdirAll(generationRoot, 0o700); err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	if err := pruneRetainedNativeDirectories(generationRoot, "", 0); err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	generation, err := os.MkdirTemp(generationRoot, "generation-")
	if err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	candidate := filepath.Join(generation, "candidate")
	defer func() { _ = os.RemoveAll(generation) }()
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	buildResult, buildErr := recipe.Build(ctx, nativebuilddriver.BuildRequest{
		Workspace:      result.Dir,
		Output:         candidate,
		GenerationRoot: generation,
		BuildArgv:      append([]string{stockGoDriverPath()}, goBuildArgs(result.Binary, effectiveGoBuildFlags(result))...),
		Environment:    result.GoEnvironment,
		BuildFlags:     append([]string(nil), result.GoBuildFlags...),
		CaptureMode:    "retained",
	})
	releaseSlot()
	if buildErr != nil || buildResult.Status != "supported_and_rebuilt" {
		recordRetainedNativeSteps(ctx, started, buildResult, buildErr)
		return buildResult, nil, buildErr
	}
	commitStarted := time.Now()
	next, commitStats, err := recipe.Advance(buildResult, filepath.Join(filepath.Dir(loaded.recipePath), "retained"))
	if err != nil {
		return buildResult, nil, fmt.Errorf("advance retained compiler state: %w", err)
	}
	if err := writeRetainedNativeRecipe(loaded.recipePath, next); err != nil {
		return buildResult, nil, fmt.Errorf("publish retained compiler state: %w", err)
	}
	buildResult.Phases["state_commit"] = nativebuilddriver.PhaseTiming{
		StartedAt: commitStarted.UTC(), DurationMS: retainedNativeElapsedMS(commitStarted),
		FilesHashed: commitStats.FilesHashed, BytesHashed: commitStats.BytesHashed,
		FilesReused: commitStats.FilesReused, BytesReused: commitStats.BytesReused,
	}
	publishStarted := time.Now()
	if err := publishRetainedNativeBinary(candidate, result.Binary); err != nil {
		return buildResult, nil, fmt.Errorf("publish retained application binary: %w", err)
	}
	result.ArtifactDigest, result.ExecutableBytes = buildResult.ArtifactDigest, buildResult.ExecutableBytes
	buildResult.Phases["artifact_publication"] = nativebuilddriver.PhaseTiming{StartedAt: publishStarted.UTC(), DurationMS: retainedNativeElapsedMS(publishStarted)}
	if err := recordBuildArtifact(ctx, result.Binary, "retained_recipe"); err != nil {
		return buildResult, nil, err
	}
	cleanupStarted := time.Now()
	cleanupErr := next.PruneUnreferenced(filepath.Join(filepath.Dir(loaded.recipePath), "retained"))
	buildResult.Phases["state_cleanup"] = nativebuilddriver.PhaseTiming{StartedAt: cleanupStarted.UTC(), DurationMS: retainedNativeElapsedMS(cleanupStarted)}
	buildResult.TransactionMS = retainedNativeElapsedMS(started)
	recordRetainedNativeSteps(ctx, started, buildResult, nil)
	RecordStep(ctx, Step{Name: "go.state_cleanup", StartedAt: cleanupStarted, Duration: time.Since(cleanupStarted), Cache: "retained_state", Reason: "unreferenced_content_addressed_state", OK: cleanupErr == nil})
	RecordStep(ctx, Step{Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "hit", Reason: "retained_compiler", OK: true, Actions: buildResult.ToolInvocations, PackagesRebuilt: buildResult.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: buildResult.ExecutableBytes})
	return buildResult, next, nil
}

func bootstrapRetainedNativeRecipe(ctx context.Context, root string, result *Result, recorderExecutable, reason string) (*retainedNativeLoaded, error) {
	started := time.Now()
	recipesRoot := filepath.Join(root, "recipes")
	generationsRoot := filepath.Join(root, "generations")
	for _, directory := range []string{recipesRoot, generationsRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}
	if err := pruneRetainedNativeDirectories(generationsRoot, "", 0); err != nil {
		return nil, err
	}
	recipeRoot, err := os.MkdirTemp(recipesRoot, "recipe-")
	if err != nil {
		return nil, err
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
			return nil, err
		}
	}
	generation, err := os.MkdirTemp(generationsRoot, "bootstrap-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	candidate := filepath.Join(generation, "candidate")
	args := goBuildArgs(candidate, effectiveGoBuildFlags(result))
	args = append([]string{"build", "-a", "-work", "-toolexec=" + retainedNativeToolExecCommand(recorderExecutable, recordRoot)}, args[1:]...)
	environment := retainedNativeEnvironment(result.GoEnvironment, map[string]string{"GOCACHE": filepath.Join(recordRoot, "cache"), "TMPDIR": filepath.Join(recordRoot, "tmp")})
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return nil, err
	}
	commandStarted := time.Now()
	goTool := stockGoDriverPath()
	command := exec.CommandContext(ctx, goTool, args...)
	command.Dir, command.Env = result.Dir, environment
	output, runErr := command.CombinedOutput()
	releaseSlot()
	RecordStep(ctx, Step{Name: "go.command", StartedAt: commandStarted, Duration: time.Since(commandStarted), Cache: "miss", Reason: "retained_bootstrap", OK: runErr == nil, Actions: 1})
	if runErr != nil {
		return nil, fmt.Errorf("bootstrap retained compiler recipe: %w\n%s", runErr, output)
	}
	captureStarted := time.Now()
	capture, err := nativebuilddriver.FullCapture(ctx, goTool, result.Dir, filepath.Join(recordRoot, "bootstrap-snapshot"), result.GoEnvironment, result.GoBuildFlags)
	RecordStep(ctx, Step{Name: "go.recipe_capture", StartedAt: captureStarted, Duration: time.Since(captureStarted), Cache: "miss", Reason: "stock_package_loading_and_input_snapshot", OK: err == nil, Actions: len(capture.Packages)})
	if err != nil {
		return nil, err
	}
	recipeStarted := time.Now()
	recipe, err := nativebuilddriver.LoadRecordedRecipe(recordRoot, result.Dir, capture)
	actions := 0
	if recipe != nil {
		actions = len(recipe.Compiles) + 1
	}
	RecordStep(ctx, Step{Name: "go.action_recipe", StartedAt: recipeStarted, Duration: time.Since(recipeStarted), Cache: "miss", Reason: "captured_stock_tool_actions", OK: err == nil, Actions: actions})
	if err != nil {
		return nil, err
	}
	recipePath := filepath.Join(recipeRoot, "recipe.json")
	if err := writeRetainedNativeRecipe(recipePath, recipe); err != nil {
		return nil, err
	}
	artifactDigest, executableBytes, err := nativebuilddriver.FileDigest(candidate)
	if err != nil {
		return nil, err
	}
	if err := publishRetainedNativeBinary(candidate, result.Binary); err != nil {
		return nil, fmt.Errorf("publish bootstrap application binary: %w", err)
	}
	result.ArtifactDigest, result.ExecutableBytes = artifactDigest, executableBytes
	recorderDigest, _, err := nativebuilddriver.FileDigest(recorderExecutable)
	if err != nil {
		return nil, err
	}
	current := retainedNativeCurrent{Protocol: retainedNativeStateProtocol, Revision: retainedNativeStateRevision, Workspace: result.Dir, RecipePath: recipePath, RecorderExecutable: recorderExecutable, RecorderDigest: recorderDigest}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicfile.Write(filepath.Join(root, "current.json"), append(encoded, '\n'), 0o600, atomicfile.Options{}); err != nil {
		return nil, err
	}
	keepRecipe = true
	_ = os.RemoveAll(filepath.Join(recordRoot, "cache"))
	_ = os.RemoveAll(filepath.Join(recordRoot, "tmp"))
	_ = pruneRetainedNativeDirectories(recipesRoot, recipeRoot, 1)
	if err := recordBuildArtifact(ctx, result.Binary, "retained_bootstrap"); err != nil {
		return nil, err
	}
	RecordStep(ctx, Step{Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "miss", Reason: reason, OK: true, Actions: len(recipe.Compiles) + 1, PackagesRebuiltAvailable: true})
	return &retainedNativeLoaded{recipe: recipe, recipePath: recipePath}, nil
}

func loadRetainedNativeRecipe(root, workspace, recorderExecutable string) (*retainedNativeLoaded, error) {
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
	return &retainedNativeLoaded{recipe: &recipe, recipePath: current.RecipePath}, nil
}

func writeRetainedNativeRecipe(path string, recipe *nativebuilddriver.Recipe) error {
	encoded, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(encoded, '\n'), 0o600, atomicfile.Options{})
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
	ok := err == nil && result.Status == "supported_and_rebuilt"
	record := func(phase, name, cache, reason string) {
		timing, exists := result.Phases[phase]
		if !exists {
			return
		}
		RecordStep(ctx, Step{Name: name, StartedAt: timing.StartedAt, Duration: time.Duration(timing.DurationMS * float64(time.Millisecond)), Cache: cache, Reason: reason, OK: ok})
	}
	record("archive_validation", "go.retained_archive", "content_stamp", "retained_archive_identity")
	record("support_validation", "go.retained_support", "content_stamp", "retained_action_support_identity")
	record("input_capture", "go.input_discovery", "retained", "complete_retained_domain")
	if timing, exists := result.Phases["input_capture"]; exists {
		captureStart := timing.StartedAt
		for _, phase := range []struct {
			name, reason string
			duration     float64
		}{
			{"go.package_loading", "tool_and_retained_package_projection", result.PackageLoadingMS},
			{"go.directory_validation", "package_membership", result.DirectoryMS},
			{"go.input_hash", "current_input_bytes", result.InputHashMS},
			{"go.snapshot", "generation_owned_changed_inputs", result.SnapshotMS},
		} {
			if phase.duration > 0 {
				RecordStep(ctx, Step{Name: phase.name, StartedAt: captureStart, Duration: time.Duration(phase.duration * float64(time.Millisecond)), Cache: "retained", Reason: phase.reason, OK: ok})
			}
		}
	}
	record("eligibility", "go.input_validation", "retained_recipe", "recipe_compatibility")
	record("planning", "go.action_plan", "retained_recipe", "changed_packages_and_consumers")
	record("compile", "go.compile", "retained_recipe", "changed_packages_and_consumers")
	record("link", "go.link", "retained_recipe", "application_executable")
	record("finalization", "go.finalization", "content_digest", "application_executable")
	if timing, exists := result.Phases["state_commit"]; exists {
		RecordStep(ctx, Step{
			Name: "go.state_commit", StartedAt: timing.StartedAt, Duration: time.Duration(timing.DurationMS * float64(time.Millisecond)),
			Cache: "retained_state", Reason: "current_recipe_manifest", OK: ok,
			FilesHashed: timing.FilesHashed, BytesHashed: timing.BytesHashed,
			FilesReused: timing.FilesReused, BytesReused: timing.BytesReused,
		})
	}
	record("artifact_publication", "go.artifact_publication", "retained_state", "application_executable")
	RecordStep(ctx, Step{Name: "go.command", StartedAt: started, Duration: time.Since(started), Cache: "retained_recipe", Reason: "build", OK: ok, Actions: result.ToolInvocations, PackagesRebuilt: result.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: result.ExecutableBytes})
}

func retainedNativeElapsedMS(started time.Time) float64 {
	return float64(time.Since(started).Nanoseconds()) / 1e6
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
