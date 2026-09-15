package nativebuilddriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BuildRequest struct {
	Workspace, Output, GenerationRoot string
	BuildArgv                         []string
	Environment                       []string
	BuildFlags                        []string
	CaptureMode                       string
}

type BuildResult struct {
	StartedAt       time.Time              `json:"transaction_started_at"`
	Backend         string                 `json:"backend,omitempty"`
	Owner           string                 `json:"owner,omitempty"`
	RequestSequence uint64                 `json:"request_sequence,omitempty"`
	Status          string                 `json:"status"`
	CaptureMS       float64                `json:"capture_ms"`
	ArchiveMS       float64                `json:"archive_validation_ms,omitempty"`
	SupportMS       float64                `json:"support_validation_ms,omitempty"`
	DirectoryMS     float64                `json:"directory_validation_ms,omitempty"`
	InputHashMS     float64                `json:"input_hash_ms,omitempty"`
	SnapshotMS      float64                `json:"snapshot_ms,omitempty"`
	ValidationMS    float64                `json:"validation_ms"`
	PlanningMS      float64                `json:"planning_ms"`
	CompileMS       float64                `json:"compile_ms"`
	LinkMS          float64                `json:"link_ms"`
	FinalizationMS  float64                `json:"finalization_ms"`
	ArtifactBuildMS float64                `json:"artifact_build_ms"`
	TransactionMS   float64                `json:"transaction_ms"`
	Phases          map[string]PhaseTiming `json:"phases,omitempty"`
	CaptureDigest   string                 `json:"capture_digest"`
	ArtifactDigest  string                 `json:"artifact_digest"`
	ExecutableBytes int64                  `json:"executable_bytes"`
	ChangedPackages []string               `json:"changed_packages"`
	RebuiltPackages []string               `json:"rebuilt_packages"`
	ToolInvocations int                    `json:"tool_invocations"`
	ActionArtifacts map[string]string      `json:"action_artifacts"`
	Reason          string                 `json:"reason,omitempty"`
	ExpectedConfig  *BuildConfig           `json:"expected_config,omitempty"`
	ObservedConfig  *BuildConfig           `json:"observed_config,omitempty"`
	capture         Capture
	archiveOutputs  map[string]string
}

type PhaseTiming struct {
	StartedAt  time.Time `json:"started_at"`
	DurationMS float64   `json:"duration_ms"`
}

type BuildConfig struct {
	GoVersion    string            `json:"go_version"`
	GoToolDigest string            `json:"go_tool_digest"`
	BuildFlags   []string          `json:"build_flags"`
	Environment  map[string]string `json:"environment"`
}

func (recipe *Recipe) Build(ctx context.Context, request BuildRequest) (result BuildResult, resultErr error) {
	transactionStarted := time.Now()
	defer func() { result.TransactionMS = elapsedMS(transactionStarted) }()
	result.StartedAt = transactionStarted.UTC()
	result.Status = "needs_rebootstrap"
	result.Backend = "retained_compiler"
	result.Phases = map[string]PhaseTiming{}
	recordPhase := func(name string, started time.Time) {
		result.Phases[name] = PhaseTiming{StartedAt: started.UTC(), DurationMS: elapsedMS(started)}
	}
	if err := recipe.Validate(); err != nil {
		result.Reason = "retained_recipe_invalid"
		return result, nil
	}
	archiveAt := time.Now()
	if err := recipe.validateRetainedArtifacts(); err != nil {
		result.ArchiveMS = float64(time.Since(archiveAt).Nanoseconds()) / 1e6
		recordPhase("archive_validation", archiveAt)
		result.Reason = "retained_archive_invalid"
		return result, nil
	}
	result.ArchiveMS = float64(time.Since(archiveAt).Nanoseconds()) / 1e6
	recordPhase("archive_validation", archiveAt)
	supportAt := time.Now()
	if err := recipe.validateSupportArtifacts(); err != nil {
		result.SupportMS = float64(time.Since(supportAt).Nanoseconds()) / 1e6
		recordPhase("support_validation", supportAt)
		result.Reason = "retained_support_invalid"
		return result, nil
	}
	result.SupportMS = float64(time.Since(supportAt).Nanoseconds()) / 1e6
	recordPhase("support_validation", supportAt)
	captureAt := time.Now()
	var capture Capture
	var err error
	if request.CaptureMode == "retained" {
		capture, err = recipe.RetainedCapture(ctx, request.BuildArgv[0], filepath.Join(request.GenerationRoot, "snapshot"), request.Environment, request.BuildFlags)
	} else {
		capture, err = FullCapture(ctx, request.BuildArgv[0], request.Workspace, filepath.Join(request.GenerationRoot, "snapshot"), request.Environment, request.BuildFlags)
	}
	result.CaptureMS, result.CaptureDigest = capture.DurationMS, capture.Digest
	recordPhase("input_capture", captureAt)
	result.DirectoryMS, result.InputHashMS, result.SnapshotMS = capture.DirectoryValidationMS, capture.InputHashMS, capture.SnapshotMS
	if err != nil {
		return result, err
	}
	validatedAt := time.Now()
	changed, reason := recipe.eligible(capture)
	result.ValidationMS = float64(time.Since(validatedAt).Nanoseconds()) / 1e6
	recordPhase("eligibility", validatedAt)
	if reason != "" {
		result.Reason = reason
		result.ExpectedConfig = captureConfig(recipe.currentCapture())
		result.ObservedConfig = captureConfig(capture)
		return result, nil
	}
	result.ChangedPackages = changed
	planningAt := time.Now()
	rebuilt, err := recipe.rebuildOrder(changed)
	if err != nil {
		return result, err
	}
	result.RebuiltPackages = rebuilt
	result.PlanningMS = float64(time.Since(planningAt).Nanoseconds()) / 1e6
	recordPhase("planning", planningAt)
	if reason := recipe.unsupportedFrontier(rebuilt); reason != "" {
		result.Reason = reason
		return result, nil
	}
	buildAt := time.Now()
	archives := make(map[string]string, len(recipe.ArchiveByOld))
	for old, retained := range recipe.ArchiveByOld {
		archives[old] = retained
	}
	result.ActionArtifacts = map[string]string{}
	result.archiveOutputs = map[string]string{}
	compileAt := time.Now()
	for _, pkg := range rebuilt {
		action := recipe.Compiles[pkg]
		output := filepath.Join(request.GenerationRoot, "archives", digestName(pkg)+".a")
		if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
			return result, err
		}
		args, err := recipe.compileArgs(action, capture, archives, output, request.GenerationRoot)
		if err != nil {
			return result, err
		}
		if err := runTool(ctx, action.Tool, request.Workspace, request.Environment, args); err != nil {
			return result, err
		}
		digest, _, err := FileDigest(output)
		if err != nil {
			return result, err
		}
		result.ActionArtifacts[pkg] = digest
		result.archiveOutputs[pkg] = output
		for _, alias := range recipe.archiveAliases(pkg) {
			archives[alias] = output
		}
		result.ToolInvocations++
	}
	result.CompileMS = float64(time.Since(compileAt).Nanoseconds()) / 1e6
	recordPhase("compile", compileAt)
	linkAt := time.Now()
	args, err := recipe.linkArgs(request, archives)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(request.Output), 0o700); err != nil {
		return result, err
	}
	if err := runTool(ctx, recipe.Link.Tool, request.Workspace, request.Environment, args); err != nil {
		return result, err
	}
	result.LinkMS = float64(time.Since(linkAt).Nanoseconds()) / 1e6
	recordPhase("link", linkAt)
	finalAt := time.Now()
	digest, size, err := FileDigest(request.Output)
	if err != nil {
		return result, err
	}
	if err := os.Chmod(request.Output, 0o755); err != nil {
		return result, err
	}
	result.ArtifactDigest, result.ExecutableBytes = digest, size
	result.FinalizationMS = float64(time.Since(finalAt).Nanoseconds()) / 1e6
	recordPhase("finalization", finalAt)
	result.ArtifactBuildMS = float64(time.Since(buildAt).Nanoseconds()) / 1e6
	result.ToolInvocations++
	result.Status = "supported_and_rebuilt"
	result.capture = capture
	return result, nil
}

// Advance returns a self-contained next recipe whose source snapshots and
// rebuilt archives survive deletion of the build generation. The receiver is
// unchanged unless the caller atomically publishes the returned manifest.
func (recipe *Recipe) Advance(result BuildResult, stateRoot string) (*Recipe, error) {
	if result.Status != "supported_and_rebuilt" || result.capture.Digest == "" {
		return nil, fmt.Errorf("only a successful retained build can advance state")
	}
	next := *recipe
	next.Current = cloneCaptureValue(result.capture)
	next.ArchiveByOld = cloneStrings(recipe.ArchiveByOld)
	next.Retained = map[string]RetainedFile{}
	next.Support = cloneRetainedFiles(recipe.Support)

	for original, snapshot := range next.Current.SnapshotFiles {
		if snapshot == "" || next.Current.Files[original] == "" {
			continue
		}
		if retained, ok := recipe.Support[snapshot]; ok && retained.Digest == next.Current.Files[original] {
			continue
		}
		target := filepath.Join(stateRoot, "snapshots", digestName(original+"\x00"+next.Current.Files[original])+filepath.Ext(original))
		if !samePath(snapshot, target) {
			copy, err := CopyRegular(snapshot, target)
			if err != nil {
				return nil, fmt.Errorf("retain current source snapshot %s: %w", original, err)
			}
			if copy.Digest != next.Current.Files[original] {
				return nil, fmt.Errorf("current source snapshot identity changed: %s", original)
			}
		}
		next.Current.SnapshotFiles[original] = target
	}
	for pkg, output := range result.archiveOutputs {
		action := next.Compiles[pkg]
		if action == nil {
			return nil, fmt.Errorf("rebuilt package recipe is absent: %s", pkg)
		}
		digest := result.ActionArtifacts[pkg]
		if digest == "" {
			return nil, fmt.Errorf("rebuilt package digest is absent: %s", pkg)
		}
		target := filepath.Join(stateRoot, "artifacts", strings.TrimPrefix(digest, "sha256:")+".a")
		copy, err := CopyRegular(output, target)
		if err != nil {
			return nil, fmt.Errorf("retain rebuilt archive %s: %w", pkg, err)
		}
		if copy.Digest != digest {
			return nil, fmt.Errorf("rebuilt archive identity changed: %s", pkg)
		}
		for _, alias := range next.archiveAliases(pkg) {
			next.ArchiveByOld[alias] = target
		}
	}
	if err := next.rebuildRetainedAccounting(); err != nil {
		return nil, err
	}
	if err := next.rebuildSupportAccounting(); err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	return &next, nil
}

// archiveAliases returns every stock-Go archive path that an action or the
// linker recorded for one package. A rebuilt archive must replace all aliases;
// updating only the compiler output path can leave the linker on an older cache
// archive for the same import path.
func (recipe *Recipe) archiveAliases(importPath string) []string {
	aliases := map[string]bool{}
	if action := recipe.Compiles[importPath]; action != nil && action.Output.Original != "" {
		aliases[action.Output.Original] = true
	}
	for _, action := range recipe.Compiles {
		for path, pkg := range action.Imports {
			if pkg == importPath {
				aliases[path] = true
			}
		}
	}
	if recipe.Link != nil {
		for path, pkg := range recipe.Link.Imports {
			if pkg == importPath {
				aliases[path] = true
			}
		}
	}
	result := make([]string, 0, len(aliases))
	for path := range aliases {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// PruneUnreferenced removes superseded content-addressed state after the next
// recipe manifest and executable have both been published. It never follows
// symlinks and only visits the three driver-owned retained-state directories.
func (recipe *Recipe) PruneUnreferenced(stateRoot string) error {
	root, err := filepath.Abs(stateRoot)
	if err != nil {
		return err
	}
	referenced := make(map[string]struct{}, len(recipe.Retained)+len(recipe.Support))
	for path := range recipe.Retained {
		referenced[filepath.Clean(path)] = struct{}{}
	}
	for path := range recipe.Support {
		referenced[filepath.Clean(path)] = struct{}{}
	}
	for _, name := range []string{"artifacts", "snapshots", "support"} {
		directory := filepath.Join(root, name)
		if _, statErr := os.Lstat(directory); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		var empty []string
		if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == directory {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("retained state contains symlink: %s", path)
			}
			if entry.IsDir() {
				empty = append(empty, path)
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("retained state contains non-regular file: %s", path)
			}
			if _, keep := referenced[filepath.Clean(path)]; !keep {
				return os.Remove(path)
			}
			return nil
		}); err != nil {
			return err
		}
		for index := len(empty) - 1; index >= 0; index-- {
			if err := os.Remove(empty[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
				remaining, readErr := os.ReadDir(empty[index])
				if readErr != nil || len(remaining) == 0 {
					return err
				}
			}
		}
	}
	return nil
}

func (recipe *Recipe) rebuildRetainedAccounting() error {
	recipe.Retained = map[string]RetainedFile{}
	recipe.RetainedBytes = 0
	for _, path := range recipe.ArchiveByOld {
		if _, exists := recipe.Retained[path]; exists {
			continue
		}
		digest, size, err := FileDigest(path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		recipe.Retained[path] = RetainedFile{Digest: digest, Bytes: size, Stamp: fileStamp(info)}
		recipe.RetainedBytes += size
	}
	recipe.RetentionLimit = recipe.RetainedBytes*2 + 512<<20
	return nil
}

func (recipe *Recipe) rebuildSupportAccounting() error {
	paths := map[string]string{}
	for _, action := range recipe.Compiles {
		for _, file := range action.Files {
			if _, source := recipe.Current.Files[file.Original]; !source {
				paths[file.Copy] = file.Digest
			}
		}
	}
	for _, file := range recipe.Link.Files {
		if _, source := recipe.Current.Files[file.Original]; !source {
			paths[file.Copy] = file.Digest
		}
	}
	for original, path := range recipe.Current.SnapshotFiles {
		paths[path] = recipe.Current.Files[original]
	}
	recipe.Support = map[string]RetainedFile{}
	for path, digest := range paths {
		if err := retainSupportPath(recipe, path, digest); err != nil {
			return err
		}
	}
	return nil
}

func cloneRetainedFiles(source map[string]RetainedFile) map[string]RetainedFile {
	result := make(map[string]RetainedFile, len(source))
	for path, file := range source {
		result[path] = file
	}
	return result
}

func cloneCaptureValue(source Capture) Capture {
	result := source
	result.Packages = clonePackages(source.Packages)
	result.Files = cloneStrings(source.Files)
	result.FileStamps = make(map[string]FileStamp, len(source.FileStamps))
	for path, stamp := range source.FileStamps {
		result.FileStamps[path] = stamp
	}
	result.Syntax = cloneStrings(source.Syntax)
	result.Directories = cloneStrings(source.Directories)
	result.SnapshotFiles = cloneStrings(source.SnapshotFiles)
	result.BuildFlags = append([]string(nil), source.BuildFlags...)
	result.Environment = cloneStrings(source.Environment)
	result.RequestEnv = cloneStrings(source.RequestEnv)
	return result
}

func captureConfig(value Capture) *BuildConfig {
	return &BuildConfig{GoVersion: value.GoVersion, GoToolDigest: value.GoToolDigest, BuildFlags: value.BuildFlags, Environment: value.Environment}
}
