package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/nativebuilddriver"
)

// The process model links one executable per service. Each entrypoint keeps its
// own captured stock-Go recipe, and every recipe of one workspace shares a
// single content-addressed store of retained archives, because the entrypoints
// of one application compile nearly the same packages and separate stores would
// retain each closure again. A missing or incompatible recipe never blocks a
// build: the entrypoint is linked by stock Go and its recipe is captured in the
// background, at a lowered scheduling priority, for the next edit.

const (
	retainedProcessRoot      = "processes"
	retainedProcessBootstrap = 10 * time.Minute
	retainedProcessPriority  = 10
)

// retainedProcessTarget is one entrypoint the retained compiler can link.
type retainedProcessTarget struct {
	name    string
	pattern string
	output  string
	flags   []string
}

var (
	retainedProcessRecipes    sync.Map
	retainedProcessBootstraps sync.Map
	retainedProcessPruned     sync.Map
	retainedProcessStockLinks sync.Map
	// One capture rebuilds a complete closure with every core it can use, so a
	// workspace records one recipe at a time however many entrypoints wait.
	retainedProcessCaptureSlot = make(chan struct{}, 1)
)

func retainedProcessRoots(workspace string) (root, shared string, err error) {
	base, err := retainedNativeRoot(workspace)
	if err != nil {
		return "", "", err
	}
	root = filepath.Join(base, retainedProcessRoot)
	return root, filepath.Join(root, "shared"), nil
}

func retainedProcessTargetRoot(root, name string) string {
	return filepath.Join(root, "targets", name)
}

// linkRetainedDevelopmentProcess links one entrypoint from its captured recipe.
// It reports whether the retained compiler produced the executable; every other
// outcome leaves the entrypoint to the caller's stock Go build.
func linkRetainedDevelopmentProcess(ctx context.Context, result *Result, target retainedProcessTarget) (bool, error) {
	recorder, ok := retainedNativeExecutable()
	if !ok || benchmarkStockGoBuild {
		return false, nil
	}
	root, shared, err := retainedProcessRoots(result.Dir)
	if err != nil {
		return false, err
	}
	targetRoot := retainedProcessTargetRoot(root, target.name)
	key := targetRoot + "\x00" + recorder
	entryValue, _ := retainedProcessRecipes.LoadOrStore(key, &retainedNativeCacheEntry{})
	entry := entryValue.(*retainedNativeCacheEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	loaded, err := entry.load(func() (*retainedNativeLoaded, error) {
		return loadRetainedNativeRecipe(targetRoot, result.Dir, recorder)
	})
	if err != nil {
		return false, nil
	}
	buildResult, next, err := runRetainedProcessRecipe(ctx, result, target, shared, loaded)
	if err != nil {
		return false, err
	}
	if buildResult.Status != "supported_and_rebuilt" {
		// The recipe no longer describes this entrypoint's inputs. Forget it and
		// capture a new one beside the stock build the caller now runs.
		retainedProcessRecipes.Delete(key)
		RecordStep(ctx, Step{Name: "build.backend", StartedAt: time.Now(), Cache: "miss", Reason: "retained_process_" + buildResult.Reason, OK: true})
		return false, nil
	}
	loaded.recipe = next
	return true, nil
}

func runRetainedProcessRecipe(ctx context.Context, result *Result, target retainedProcessTarget, shared string, loaded *retainedNativeLoaded) (nativebuilddriver.BuildResult, *nativebuilddriver.Recipe, error) {
	started := time.Now()
	generation, err := os.MkdirTemp(filepath.Dir(loaded.recipePath), "generation-")
	if err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	candidate := filepath.Join(generation, "candidate")
	releaseSlot, err := retainedNativeLinkSlot(ctx, result)
	if err != nil {
		return nativebuilddriver.BuildResult{}, nil, err
	}
	buildResult, buildErr := loaded.recipe.Build(ctx, nativebuilddriver.BuildRequest{
		Workspace:      result.Dir,
		Output:         candidate,
		GenerationRoot: generation,
		BuildArgv:      append([]string{stockGoDriverPath()}, developmentProcessGoBuildArgs(target)...),
		Environment:    result.GoEnvironment,
		// Only stable configuration decides eligibility; the per-process linker
		// identity travels in the build argv and is spliced into the link.
		BuildFlags:  append([]string(nil), result.GoBuildFlags...),
		CaptureMode: "retained",
	})
	releaseSlot()
	if buildErr != nil || buildResult.Status != "supported_and_rebuilt" {
		return buildResult, nil, buildErr
	}
	next, _, err := loaded.recipe.Advance(buildResult, shared)
	if err != nil {
		return buildResult, nil, fmt.Errorf("advance retained compiler state of %s: %w", target.name, err)
	}
	if err := writeRetainedNativeRecipe(loaded.recipePath, next); err != nil {
		return buildResult, nil, fmt.Errorf("publish retained compiler state of %s: %w", target.name, err)
	}
	if err := publishRetainedNativeBinary(candidate, target.output); err != nil {
		return buildResult, nil, fmt.Errorf("publish retained development process %s: %w", target.name, err)
	}
	RecordStep(ctx, Step{
		Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "hit",
		Reason: "retained_process_" + target.name, OK: true, Actions: buildResult.ToolInvocations,
		PackagesRebuilt: buildResult.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: buildResult.ExecutableBytes,
	})
	return buildResult, next, nil
}

// captureRetainedProcessRecipes records a recipe for an entrypoint the session
// has now linked more than once, so later edits of a service the developer
// actually works on link without the Go command's own package loading. The
// first build of a session links every entrypoint and records nothing: an
// application of fifty services must not answer its first edit by rebuilding
// fifty complete closures. Capture runs in the background at a lowered
// priority and never blocks or fails the build that requested it.
func captureRetainedProcessRecipes(ctx context.Context, result *Result, targets []retainedProcessTarget) {
	recorder, ok := retainedNativeExecutable()
	if !ok || benchmarkStockGoBuild || len(targets) == 0 {
		return
	}
	root, shared, err := retainedProcessRoots(result.Dir)
	if err != nil {
		return
	}
	workspace, environment := result.Dir, append([]string(nil), result.GoEnvironment...)
	configuration := append([]string(nil), result.GoBuildFlags...)
	for _, target := range targets {
		targetRoot := retainedProcessTargetRoot(root, target.name)
		if !repeatedStockProcessLink(targetRoot) {
			continue
		}
		if _, exists := retainedProcessBootstraps.LoadOrStore(targetRoot, true); exists {
			continue
		}
		// The capture outlives the build that scheduled it but keeps its trace,
		// so the recipe it captures is attributed to the edit that needed it.
		captureCtx := context.WithoutCancel(ctx)
		go func(target retainedProcessTarget) {
			defer retainedProcessBootstraps.Delete(targetRoot)
			captureCtx, cancel := context.WithTimeout(captureCtx, retainedProcessBootstrap)
			defer cancel()
			select {
			case retainedProcessCaptureSlot <- struct{}{}:
				defer func() { <-retainedProcessCaptureSlot }()
			case <-captureCtx.Done():
				return
			}
			started := time.Now()
			err := captureRetainedProcessRecipe(captureCtx, workspace, environment, configuration, targetRoot, shared, recorder, target)
			RecordStep(captureCtx, Step{
				Name: "build.recipe_capture", StartedAt: started, Duration: time.Since(started), Cache: "miss",
				Reason: captureReason(target.name, err), OK: err == nil,
			})
		}(target)
	}
}

// repeatedStockProcessLink reports whether this workspace has linked the
// entrypoint by stock Go before, which is what makes a recipe worth its
// complete rebuild.
func repeatedStockProcessLink(targetRoot string) bool {
	previous, _ := retainedProcessStockLinks.LoadOrStore(targetRoot, false)
	retainedProcessStockLinks.Store(targetRoot, true)
	linked, _ := previous.(bool)
	return linked
}

func captureRetainedProcessRecipe(ctx context.Context, workspace string, environment, configuration []string, targetRoot, shared, recorder string, target retainedProcessTarget) error {
	if _, err := os.Lstat(filepath.Join(targetRoot, "current.json")); err == nil {
		return nil
	}
	recipeRoot, err := os.MkdirTemp(targetRoot, "recipe-")
	if os.IsNotExist(err) {
		if err = os.MkdirAll(targetRoot, 0o700); err != nil {
			return err
		}
		recipeRoot, err = os.MkdirTemp(targetRoot, "recipe-")
	}
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(recipeRoot)
		}
	}()
	recordRoot := filepath.Join(recipeRoot, "bootstrap")
	for _, directory := range []string{filepath.Join(recordRoot, "actions"), filepath.Join(recordRoot, "cache"), filepath.Join(recordRoot, "tmp")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	candidate := filepath.Join(recipeRoot, "candidate")
	args := developmentProcessGoBuildArgs(retainedProcessTarget{pattern: target.pattern, output: candidate, flags: target.flags})
	args = append([]string{"build", "-a", "-work", "-toolexec=" + retainedNativeToolExecCommand(recorder, recordRoot)}, args[1:]...)
	goTool := stockGoDriverPath()
	command := exec.CommandContext(ctx, goTool, args...)
	command.Dir = workspace
	command.Env = retainedNativeEnvironment(environment, map[string]string{"GOCACHE": filepath.Join(recordRoot, "cache"), "TMPDIR": filepath.Join(recordRoot, "tmp")})
	if err := command.Start(); err != nil {
		return err
	}
	// Capturing a recipe rebuilds a complete closure; it must not compete with
	// the edit loop that requested it.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, command.Process.Pid, retainedProcessPriority)
	if err := command.Wait(); err != nil {
		return err
	}
	capture, err := nativebuilddriver.FullCapture(ctx, goTool, workspace, filepath.Join(recordRoot, "bootstrap-snapshot"), environment, configuration, target.pattern)
	if err != nil {
		return err
	}
	recipe, err := nativebuilddriver.LoadRecordedRecipe(recordRoot, workspace, capture, shared)
	if err != nil {
		return err
	}
	recipePath := filepath.Join(recipeRoot, "recipe.json")
	if err := writeRetainedNativeRecipe(recipePath, recipe); err != nil {
		return err
	}
	recorderDigest, _, err := nativebuilddriver.FileDigest(recorder)
	if err != nil {
		return err
	}
	current := retainedNativeCurrent{
		Protocol: retainedNativeStateProtocol, Revision: retainedNativeStateRevision, Workspace: workspace,
		RecipePath: recipePath, RecorderExecutable: recorder, RecorderDigest: recorderDigest,
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(targetRoot, "current.json"), append(encoded, '\n'), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	keep = true
	// Every retained input now lives in the shared content-addressed store.
	_ = os.RemoveAll(recordRoot)
	_ = os.Remove(candidate)
	// An interrupted capture leaves its directory without a current.json.
	_ = pruneRetainedNativeDirectories(targetRoot, recipeRoot, 1)
	return nil
}

// pruneRetainedProcessState removes shared retained state no entrypoint recipe
// references. It runs once per workspace in this process, because it must read
// every recipe to know what is still referenced.
func pruneRetainedProcessState(workspace string) {
	root, shared, err := retainedProcessRoots(workspace)
	if err != nil {
		return
	}
	if _, pruned := retainedProcessPruned.LoadOrStore(root, true); pruned {
		return
	}
	entries, err := os.ReadDir(filepath.Join(root, "targets"))
	if err != nil {
		return
	}
	var recipes []*nativebuilddriver.Recipe
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		loaded, err := loadRetainedProcessRecipe(filepath.Join(root, "targets", entry.Name()))
		if err != nil || loaded == nil {
			// An unreadable recipe must not authorize deleting shared state.
			return
		}
		recipes = append(recipes, loaded)
	}
	_ = nativebuilddriver.PruneUnreferencedAcross(shared, recipes)
}

func loadRetainedProcessRecipe(targetRoot string) (*nativebuilddriver.Recipe, error) {
	data, err := os.ReadFile(filepath.Join(targetRoot, "current.json"))
	if err != nil {
		return nil, err
	}
	var current retainedNativeCurrent
	if err := decodeRetainedNativeJSON(data, &current); err != nil {
		return nil, err
	}
	data, err = os.ReadFile(current.RecipePath)
	if err != nil {
		return nil, err
	}
	var recipe nativebuilddriver.Recipe
	if err := decodeRetainedNativeJSON(data, &recipe); err != nil {
		return nil, err
	}
	return &recipe, nil
}

func captureReason(name string, err error) string {
	if err == nil {
		return "captured_" + name
	}
	return "failed_" + name + ": " + err.Error()
}

// developmentProcessGoBuildArgs is the stock Go build of one entrypoint.
func developmentProcessGoBuildArgs(target retainedProcessTarget) []string {
	args := append([]string{"build"}, normalizeGoBuildFlags(target.flags)...)
	return append(args, "-buildvcs=false", "-o", target.output, target.pattern)
}
