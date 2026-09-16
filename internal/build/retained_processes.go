package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/devprocess"
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
	// retainedProcessCollectInterval is the number of recipe publications of a
	// workspace after which its shared store is collected again. Every retained
	// edit publishes an advanced recipe and leaves the archives and snapshots it
	// superseded behind, so a long session must keep collecting, but a
	// collection reads every recipe and must not run on every edit.
	retainedProcessCollectInterval = 16
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
	retainedProcessStores     sync.Map
	retainedProcessStockLinks sync.Map
	// retainedProcessRejected names, per target root, the recipe a retained
	// build found incompatible, so the committed pointer to it does not count
	// as a usable recipe and a replacement is captured.
	retainedProcessRejected sync.Map
)

// retainedRecordingStopTimeout bounds how long a recording waits for the tools
// of its process group to end.
const retainedRecordingStopTimeout = 5 * time.Second

// retainedRecordingPoll is how often a waiting recording checks whether the
// machine can admit it.
const retainedRecordingPoll = 250 * time.Millisecond

// acquireRetainedRecordingSlot admits one recipe recording on the whole
// machine. A recording rebuilds a complete closure, so the admission is shared
// by every supervisor and worktree through the same host-wide state as the
// foreground link queue, and a recording starts only while no foreground link
// is queued or running. A foreground link that arrives later makes a running
// recording yield (see yieldToForegroundLinks); foreground links never wait for
// a recording.
func acquireRetainedRecordingSlot(ctx context.Context) (func(), error) {
	return acquireRetainedRecordingSlotEvery(ctx, retainedRecordingPoll)
}

func acquireRetainedRecordingSlotEvery(ctx context.Context, poll time.Duration) (func(), error) {
	root, err := sharedBinaryRoot()
	if err != nil {
		return nil, err
	}
	for {
		tickets, err := sharedBinaryActiveTickets(filepath.Join(root, "queue"))
		if err != nil {
			return nil, err
		}
		if len(tickets) == 0 {
			release, acquired, err := trySharedBinaryLock(filepath.Join(root, "slots", "recording.lock"))
			if err != nil {
				return nil, err
			}
			if acquired {
				return release, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// errRetainedRecordingYielded ends a recording that gave way to a foreground
// link; it is retried once the machine admits it again.
var errRetainedRecordingYielded = errors.New("recording yielded to a foreground link")

// yieldToForegroundLinks cancels a running recording with
// errRetainedRecordingYielded as soon as a foreground link of any worktree is
// queued or running, and returns when ctx ends.
func yieldToForegroundLinks(ctx context.Context, cancel context.CancelCauseFunc, poll time.Duration) {
	root, err := sharedBinaryRoot()
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
		if tickets, err := sharedBinaryActiveTickets(filepath.Join(root, "queue")); err == nil && len(tickets) > 0 {
			cancel(errRetainedRecordingYielded)
			return
		}
	}
}

// retainedRecordingParallelism bounds the tool actions of one recording to a
// quarter of the cores, so its compiler processes and their memory stay a
// fraction of what a foreground build of the same closure may use.
func retainedRecordingParallelism() int {
	return max(2, runtime.NumCPU()/4)
}

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

// BackgroundWork owns build work that outlives the build which scheduled it,
// such as recipe capture. Its owner closes it, which cancels the work and waits
// until it has stopped; a build whose context carries no owner schedules none.
type BackgroundWork struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

type backgroundWorkKey struct{}

// NewBackgroundWork returns background work that ends with parent or Close.
func NewBackgroundWork(parent context.Context) *BackgroundWork {
	ctx, cancel := context.WithCancel(parent)
	return &BackgroundWork{ctx: ctx, cancel: cancel}
}

// WithBackgroundWork makes work the owner of background work that builds
// running with the returned context schedule.
func WithBackgroundWork(ctx context.Context, work *BackgroundWork) context.Context {
	return context.WithValue(ctx, backgroundWorkKey{}, work)
}

// Close cancels the owned work and waits for it to stop.
func (work *BackgroundWork) Close() {
	if work == nil {
		return
	}
	work.mu.Lock()
	work.closed = true
	work.mu.Unlock()
	work.cancel()
	work.wg.Wait()
}

// start runs one unit of work owned by the owner in ctx. The work keeps the
// values of ctx, such as its build trace, but not its cancellation: it ends
// when its owner closes. It reports false when there is no open owner.
func startBackgroundWork(ctx context.Context, run func(context.Context)) bool {
	work, _ := ctx.Value(backgroundWorkKey{}).(*BackgroundWork)
	if work == nil {
		return false
	}
	work.mu.Lock()
	defer work.mu.Unlock()
	if work.closed || work.ctx.Err() != nil {
		return false
	}
	work.wg.Add(1)
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(work.ctx, cancel)
	go func() {
		defer work.wg.Done()
		defer cancel()
		defer stop()
		run(runCtx)
	}()
	return true
}

// retainedProcessStore accounts for the shared retained store of one
// workspace: the captures that may be adopting objects into it before their
// recipe is published, and the publications since it was last collected.
type retainedProcessStore struct {
	mu           sync.Mutex
	captures     int
	publications int
	collected    bool
}

func retainedProcessStoreFor(root string) *retainedProcessStore {
	value, _ := retainedProcessStores.LoadOrStore(root, &retainedProcessStore{})
	return value.(*retainedProcessStore)
}

func (store *retainedProcessStore) lease() func() {
	store.mu.Lock()
	store.captures++
	store.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			store.mu.Lock()
			store.captures--
			store.mu.Unlock()
		})
	}
}

func (store *retainedProcessStore) published() {
	store.mu.Lock()
	store.publications++
	store.mu.Unlock()
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
	if rejected, _ := retainedProcessRejected.Load(targetRoot); rejected == loaded.recipePath {
		// Its replacement is not published yet.
		return false, nil
	}
	buildResult, next, err := runRetainedProcessRecipe(ctx, result, target, shared, loaded)
	if err != nil {
		return false, err
	}
	if buildResult.Status != "supported_and_rebuilt" {
		// The recipe no longer describes this entrypoint's inputs. Reject it, so
		// the capture scheduled beside the stock build the caller now runs
		// replaces it rather than finding its pointer and keeping it.
		retainedProcessRejected.Store(targetRoot, loaded.recipePath)
		RecordStep(ctx, Step{Name: "build.backend", StartedAt: time.Now(), Cache: "miss", Reason: "retained_process_" + buildResult.Reason, OK: true})
		return false, nil
	}
	loaded.recipe = next
	retainedProcessStoreFor(root).published()
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
	recordRetainedNativeSteps(ctx, started, buildResult, buildErr)
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
	if err := publishRetainedProcessBinary(candidate, target.output); err != nil {
		return buildResult, nil, fmt.Errorf("publish retained development process %s: %w", target.name, err)
	}
	RecordStep(ctx, Step{
		Name: "build.backend", StartedAt: started, Duration: time.Since(started), Cache: "hit",
		Reason: "retained_process_" + target.name, OK: true, Actions: buildResult.ToolInvocations,
		PackagesRebuilt: buildResult.RebuiltPackages, PackagesRebuiltAvailable: true, ExecutableBytes: buildResult.ExecutableBytes,
	})
	return buildResult, next, nil
}

// publishRetainedProcessBinary moves a linked entrypoint to its content-keyed
// path, as the stock build of an entrypoint does; a candidate on another
// filesystem is copied instead.
func publishRetainedProcessBinary(candidate, output string) error {
	if err := os.Chmod(candidate, 0o755); err != nil {
		return err
	}
	if err := os.Rename(candidate, output); err == nil {
		return nil
	}
	return publishRetainedNativeBinary(candidate, output)
}

// captureRetainedProcessRecipes records a recipe for an entrypoint the session
// has now linked more than once, so later edits of a service the developer
// actually works on link without the Go command's own package loading. The
// first build of a session links every entrypoint and records nothing: an
// application of fifty services must not answer its first edit by rebuilding
// fifty complete closures. Capture runs in the background work that ctx names,
// at a lowered priority, and never blocks or fails the build that requested it.
func captureRetainedProcessRecipes(ctx context.Context, result *Result, targets []retainedProcessTarget) {
	recorder, ok := retainedNativeExecutable()
	if !ok || benchmarkStockGoBuild || len(targets) == 0 {
		return
	}
	root, shared, err := retainedProcessRoots(result.Dir)
	if err != nil {
		return
	}
	store := retainedProcessStoreFor(root)
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
		// The lease is taken before this build collects the shared store, which
		// the capture adopts objects into before its recipe references them.
		release := store.lease()
		started := startBackgroundWork(ctx, func(ctx context.Context) {
			defer retainedProcessBootstraps.Delete(targetRoot)
			defer release()
			ctx, cancel := context.WithTimeout(ctx, retainedProcessBootstrap)
			defer cancel()
			for {
				releaseSlot, err := acquireRetainedRecordingSlot(ctx)
				if err != nil {
					return
				}
				started := time.Now()
				err = captureRetainedProcessRecipe(ctx, workspace, environment, configuration, targetRoot, shared, recorder, target)
				releaseSlot()
				RecordStep(ctx, Step{
					Name: "build.recipe_capture", StartedAt: started, Duration: time.Since(started), Cache: "miss",
					Reason: captureReason(target.name, err), OK: err == nil,
				})
				if !errors.Is(err, errRetainedRecordingYielded) {
					return
				}
			}
		})
		if !started {
			release()
			retainedProcessBootstraps.Delete(targetRoot)
		}
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
	if !retainedProcessNeedsRecipe(targetRoot, func() (*retainedNativeLoaded, error) {
		return loadRetainedNativeRecipe(targetRoot, workspace, recorder)
	}) {
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
	recordCtx, cancelRecording := context.WithCancelCause(ctx)
	defer cancelRecording(nil)
	go yieldToForegroundLinks(recordCtx, cancelRecording, retainedRecordingPoll)
	yielded := func(err error) error {
		if errors.Is(context.Cause(recordCtx), errRetainedRecordingYielded) {
			return errRetainedRecordingYielded
		}
		return err
	}
	goTool := stockGoDriverPath()
	// The capture does not own the workspace, which later edits keep changing.
	// It names the input revision it records before any tool runs, and that
	// revision must still be current once the tools have finished; the recorded
	// actions must also have read exactly its content. An edit during the
	// recording discards it instead of pairing archives with other sources.
	capture, err := nativebuilddriver.FullCapture(recordCtx, goTool, workspace, filepath.Join(recordRoot, "bootstrap-snapshot"), environment, configuration, target.pattern)
	if err != nil {
		return yielded(err)
	}
	candidate := filepath.Join(recipeRoot, "candidate")
	args := developmentProcessGoBuildArgs(retainedProcessTarget{pattern: target.pattern, output: candidate, flags: target.flags})
	args = append([]string{"build", "-a", "-work", "-p=" + strconv.Itoa(retainedRecordingParallelism()), "-toolexec=" + retainedNativeToolExecCommand(recorder, recordRoot)}, args[1:]...)
	// The Go command and every tool it starts through the recorder form one
	// process group. Cancelling the recording ends the whole group, and the
	// recording returns, releasing its slot, lease and directory, only once no
	// process of the group remains.
	command := exec.CommandContext(recordCtx, goTool, args...)
	devprocess.ConfigureChild(command)
	command.Cancel = func() error { return devprocess.KillTree(command) }
	command.Dir = workspace
	command.Env = retainedNativeEnvironment(environment, map[string]string{"GOCACHE": filepath.Join(recordRoot, "cache"), "TMPDIR": filepath.Join(recordRoot, "tmp")})
	if err := command.Start(); err != nil {
		return err
	}
	// Capturing a recipe rebuilds a complete closure; it must not compete with
	// the edit loop that requested it.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, command.Process.Pid, retainedProcessPriority)
	waitErr := command.Wait()
	if err := devprocess.KillTreeConfirmed(command, retainedRecordingStopTimeout); err != nil {
		return errors.Join(waitErr, err)
	}
	if waitErr != nil {
		return yielded(waitErr)
	}
	cancelRecording(nil)
	if err := capture.ValidateCurrentStamps(); err != nil {
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
	// Every retained input now lives in the shared content-addressed store.
	_ = os.RemoveAll(recordRoot)
	_ = os.Remove(candidate)
	if err := publishRetainedProcessRecipe(targetRoot, recipeRoot, current); err != nil {
		return err
	}
	keep = true
	return nil
}

// retainedProcessNeedsRecipe reports whether a target has no committed recipe
// a retained build can use: none is committed, the committed one cannot be
// loaded for this workspace and recorder, or a retained build rejected it. The
// mere existence of its pointer never suppresses a replacement.
func retainedProcessNeedsRecipe(targetRoot string, load func() (*retainedNativeLoaded, error)) bool {
	loaded, err := load()
	if err != nil {
		return true
	}
	rejected, _ := retainedProcessRejected.Load(targetRoot)
	return rejected == loaded.recipePath
}

// publishRetainedProcessRecipe atomically makes a captured recipe the target's
// committed recipe. It holds the target's recipe cache entry, so no retained
// build is advancing the recipe it supersedes while that recipe is removed,
// and the next build loads the published one.
func publishRetainedProcessRecipe(targetRoot, recipeRoot string, current retainedNativeCurrent) error {
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	entryValue, _ := retainedProcessRecipes.LoadOrStore(targetRoot+"\x00"+current.RecorderExecutable, &retainedNativeCacheEntry{})
	entry := entryValue.(*retainedNativeCacheEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := atomicfile.Write(filepath.Join(targetRoot, "current.json"), append(encoded, '\n'), 0o600, atomicfile.Options{}); err != nil {
		return err
	}
	entry.loaded = nil
	retainedProcessRejected.Delete(targetRoot)
	// An interrupted capture leaves its directory without a current.json.
	_ = pruneRetainedNativeDirectories(targetRoot, recipeRoot, 1)
	retainedProcessStoreFor(filepath.Dir(filepath.Dir(targetRoot))).published()
	return nil
}

// pruneRetainedProcessState removes shared retained state no entrypoint recipe
// references. A collection reads every committed recipe, so it runs on the
// first build of a workspace in this process and then once every
// retainedProcessCollectInterval publications. It never runs while a capture
// holds a lease, because a capture adopts objects before its recipe names them;
// the next build collects instead.
func pruneRetainedProcessState(workspace string) {
	root, shared, err := retainedProcessRoots(workspace)
	if err != nil {
		return
	}
	store := retainedProcessStoreFor(root)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.captures > 0 || store.collected && store.publications < retainedProcessCollectInterval {
		return
	}
	store.collected, store.publications = true, 0
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
		if errors.Is(err, os.ErrNotExist) {
			// A target whose first capture never completed references nothing.
			continue
		}
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
