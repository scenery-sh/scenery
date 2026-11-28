package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/build"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

const checkCacheVersion = 1

type checkCacheState struct {
	Version              int                           `json:"version"`
	ProducerDigest       string                        `json:"producer_digest"`
	ContractStatus       string                        `json:"contract_status"`
	ImplementationStatus string                        `json:"implementation_status"`
	WorkspaceRevision    string                        `json:"workspace_revision"`
	ContractRevision     string                        `json:"contract_revision"`
	HTTPSurfaceRevisions map[string]string             `json:"http_surface_revision"`
	OpenAPIRevisions     map[string]string             `json:"openapi_revision"`
	ManifestSummary      *checkCacheManifestSummary    `json:"manifest_summary,omitempty"`
	Diagnostics          []graph.Diagnostic            `json:"diagnostics,omitempty"`
	SourceDirs           map[string][]string           `json:"source_dirs"`
	WatchedDirs          map[string][]string           `json:"watched_dirs,omitempty"`
	Files                map[string]string             `json:"files"`
	GeneratedPaths       []string                      `json:"generated_paths"`
	GoTargets            map[string]checkCacheGoTarget `json:"go_targets,omitempty"`
}

type checkCacheManifestSummary struct {
	Application     graph.ApplicationIdentity `json:"application"`
	Resources       int                       `json:"resources"`
	ResourcesByKind map[string]int            `json:"resources_by_kind"`
}

type checkCacheGoTarget struct {
	Context  gotarget.Context  `json:"context"`
	Identity string            `json:"identity"`
	Dirs     map[string]string `json:"dirs"`
}

var checkExecutableStamp = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(exe + "\x00" + info.ModTime().UTC().String() + "\x00" + strconv.FormatInt(info.Size(), 10)))
	return hex.EncodeToString(sum[:])
})

func checkCachePath(root string) string {
	return filepath.Join(root, ".scenery", "cache", "check-state.json")
}

func checkCacheProducerDigest(root, pinned string) (string, bool) {
	exeStamp := checkExecutableStamp()
	if exeStamp == "" {
		return "", false
	}
	cfgDigest, ok := regularFileDigestOrAbsent(filepath.Join(root, ".scenery.json"))
	if !ok {
		return "", false
	}
	goTool, _ := gotarget.ToolIdentity(gotarget.Hermetic(nil))
	raw, err := json.Marshal([]any{
		checkCacheVersion,
		pinned,
		runningFrameworkVersion(),
		build.LinkedFrameworkDigest(),
		exeStamp,
		goTool,
		cfgDigest,
	})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), true
}

func checkCacheEligible(result *compiler.Result) bool {
	if result == nil || !result.Valid() || len(result.Diagnostics) > 0 || result.Manifest == nil || len(result.Sources) == 0 {
		return false
	}
	if result.WorkspaceRevision == "" || result.Manifest.ContractRevision == "" {
		return false
	}
	for _, src := range result.Sources {
		if src == nil || src.External {
			return false
		}
	}
	for _, svc := range compiler.RuntimeServices(result.Manifest.Resources) {
		if compiler.IsExternalWorkerRuntime(cp.StringValue(svc.Spec["runtime"])) {
			return false
		}
	}
	for _, r := range result.Manifest.Resources {
		if r.Kind != "scenery.typescript-client" {
			continue
		}
		mat := cp.StringValue(r.Spec["materialization"])
		if mat == "" {
			mat = "source"
		}
		if mat == "source" && r.Spec["react"] != nil {
			// React source targets run external type-checking over arbitrary transitive imports.
			return false
		}
	}
	return true
}

func tryCheckCacheHit(root, pinned string) (checkCacheState, bool) {
	if err := workspacetx.RecoverOrReject(root, workspacetx.NormalRead); err != nil {
		return checkCacheState{}, false
	}
	producerDigest, ok := checkCacheProducerDigest(root, pinned)
	if !ok {
		return checkCacheState{}, false
	}
	path := checkCachePath(root)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return checkCacheState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return checkCacheState{}, false
	}
	var state checkCacheState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != checkCacheVersion || state.ProducerDigest != producerDigest {
		return checkCacheState{}, false
	}
	if state.ContractStatus != "valid" || state.ContractRevision == "" || len(state.Files) == 0 {
		return checkCacheState{}, false
	}

	var failed atomic.Bool
	var wg sync.WaitGroup

	// 1. Verify .scn source directory listings.
	wg.Go(func() {
		for relDir, want := range state.SourceDirs {
			if failed.Load() {
				return
			}
			absDir := root
			if relDir != "." && relDir != "" {
				absDir = filepath.Join(root, filepath.FromSlash(relDir))
			}
			if err := scn.RejectPathSymlinks(root, absDir); err != nil {
				failed.Store(true)
				return
			}
			got, err := scn.SourceFiles(absDir, absDir == root)
			if err != nil || len(got) != len(want) {
				failed.Store(true)
				return
			}
			for i, p := range got {
				if filepath.Base(p) != want[i] {
					failed.Store(true)
					return
				}
			}
		}
	})

	// 2. Verify watched implementation directory listings.
	wg.Go(func() {
		for relDir, want := range state.WatchedDirs {
			if failed.Load() {
				return
			}
			absDir := root
			if relDir != "." && relDir != "" {
				absDir = filepath.Join(root, filepath.FromSlash(relDir))
			}
			entries, err := os.ReadDir(absDir)
			if err != nil || len(entries) != len(want) {
				failed.Store(true)
				return
			}
			for i, e := range entries {
				if e.Name() != want[i] {
					failed.Store(true)
					return
				}
			}
		}
	})

	// 3. Verify generated path set matches.
	wg.Go(func() {
		genSet, err := compiler.GeneratedPaths(root)
		if err != nil || len(genSet) != len(state.GeneratedPaths) {
			failed.Store(true)
			return
		}
		for _, p := range state.GeneratedPaths {
			if !genSet[p] {
				failed.Store(true)
				return
			}
		}
	})

	// 4. Verify Go verification target stamps (covers go.mod/go.sum/replaces/all Go pkg dirs).
	wg.Go(func() {
		for _, target := range state.GoTargets {
			if failed.Load() {
				return
			}
			identity, dirs, err := parse.CaptureTargetAnalysisStamp(target.Context)
			if err != nil || identity != target.Identity || len(dirs) != len(target.Dirs) {
				failed.Store(true)
				return
			}
			for k, want := range target.Dirs {
				if dirs[k] != want {
					failed.Store(true)
					return
				}
			}
		}
	})

	// 5. Verify exact file digests in parallel.
	filePaths := make([]string, 0, len(state.Files))
	for rel := range state.Files {
		filePaths = append(filePaths, rel)
	}
	workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(filePaths))
	var next atomic.Int64
	for range workers {
		wg.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1)) - 1
				if i >= len(filePaths) {
					return
				}
				rel := filePaths[i]
				want := state.Files[rel]
				abs := filepath.Join(root, filepath.FromSlash(rel))
				got, ok := regularFileDigestOrAbsent(abs)
				if !ok || got != want {
					failed.Store(true)
					return
				}
			}
		})
	}
	wg.Wait()
	if failed.Load() {
		return checkCacheState{}, false
	}
	return state, true
}

func recordCheckCache(root, pinned string, result *compiler.Result) {
	if !checkCacheEligible(result) {
		return
	}
	producerDigest, ok := checkCacheProducerDigest(root, pinned)
	if !ok {
		return
	}
	sourceDirs := map[string][]string{}
	fileSet := map[string]bool{}
	absentFiles := map[string]bool{}
	for _, src := range result.Sources {
		fileSet[src.Relative] = true
		absDir := filepath.Dir(src.Path)
		relDir, err := filepath.Rel(root, absDir)
		if err != nil {
			return
		}
		relDir = filepath.ToSlash(filepath.Clean(relDir))
		if _, exists := sourceDirs[relDir]; !exists {
			paths, err := scn.SourceFiles(absDir, absDir == root)
			if err != nil {
				return
			}
			names := make([]string, len(paths))
			for i, p := range paths {
				names[i] = filepath.Base(p)
			}
			sourceDirs[relDir] = names
		}
	}
	inputs, err := compiler.WorkspaceRevisionInputs(result)
	if err != nil {
		return
	}
	for _, in := range inputs {
		if in.Present {
			fileSet[in.Path] = true
		} else {
			absentFiles[in.Path] = true
		}
	}
	genSet, err := compiler.GeneratedPaths(root)
	if err != nil {
		return
	}
	genPaths := make([]string, 0, len(genSet))
	for p := range genSet {
		genPaths = append(genPaths, p)
		fileSet[p] = true
	}
	sort.Strings(genPaths)

	watchedDirList, err := compiler.WorkspaceRevisionWatchedDirs(result)
	if err != nil {
		return
	}
	watchedDirs := make(map[string][]string, len(watchedDirList))
	for _, relDir := range watchedDirList {
		absDir := root
		if relDir != "." && relDir != "" {
			absDir = filepath.Join(root, filepath.FromSlash(relDir))
		}
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		watchedDirs[relDir] = names
	}

	goTargets := map[string]checkCacheGoTarget{}
	targets, err := compiler.VerificationGoTargets(result)
	if err != nil {
		return
	}
	for _, target := range targets {
		relMod, err := filepath.Rel(root, target.Context.ModuleRoot)
		if err != nil || relMod == ".." || strings.HasPrefix(relMod, ".."+string(filepath.Separator)) {
			return
		}
		identity, dirs, err := parse.CaptureTargetAnalysisStamp(target.Context)
		if err != nil {
			return
		}
		goTargets[target.Address] = checkCacheGoTarget{
			Context:  target.Context,
			Identity: identity,
			Dirs:     dirs,
		}
	}

	presentPaths := make([]string, 0, len(fileSet))
	for p := range fileSet {
		presentPaths = append(presentPaths, p)
	}
	digests := make([]string, len(presentPaths))
	var failed atomic.Bool
	if len(presentPaths) > 0 {
		workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(presentPaths))
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for !failed.Load() {
					i := int(next.Add(1)) - 1
					if i >= len(presentPaths) {
						return
					}
					d, ok := regularFileDigestOrAbsent(filepath.Join(root, filepath.FromSlash(presentPaths[i])))
					if !ok || d == "" {
						failed.Store(true)
						return
					}
					digests[i] = d
				}
			})
		}
		wg.Wait()
	}
	if failed.Load() {
		return
	}
	files := make(map[string]string, len(presentPaths)+len(absentFiles))
	for p := range absentFiles {
		files[p] = ""
	}
	for i, p := range presentPaths {
		files[p] = digests[i]
	}

	byKind := map[string]int{}
	for _, r := range result.Manifest.Resources {
		byKind[r.Kind]++
	}
	state := checkCacheState{
		Version:              checkCacheVersion,
		ProducerDigest:       producerDigest,
		ContractStatus:       result.ContractStatus,
		ImplementationStatus: result.ImplementationStatus,
		WorkspaceRevision:    result.WorkspaceRevision,
		ContractRevision:     result.Manifest.ContractRevision,
		HTTPSurfaceRevisions: result.HTTPSurfaceRevisions,
		OpenAPIRevisions:     result.OpenAPIRevisions,
		ManifestSummary: &checkCacheManifestSummary{
			Application:     result.Manifest.Application,
			Resources:       len(result.Manifest.Resources),
			ResourcesByKind: byKind,
		},
		Diagnostics:    slices.Clone(result.Diagnostics),
		SourceDirs:     sourceDirs,
		WatchedDirs:    watchedDirs,
		Files:          files,
		GeneratedPaths: genPaths,
		GoTargets:      goTargets,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = atomicfile.Write(checkCachePath(root), encoded, 0o644, atomicfile.Options{})
}

func regularFileDigestOrAbsent(path string) (string, bool) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", true
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), true
}

func writeCachedCheckResult(stdout io.Writer, output string, quiet bool, cached checkCacheState) error {
	diagnostics := cached.Diagnostics
	if diagnostics == nil {
		diagnostics = []graph.Diagnostic{}
	}
	data := map[string]any{
		"contract_status":       cached.ContractStatus,
		"implementation_status": cached.ImplementationStatus,
		"manifest_summary":      cached.ManifestSummary,
		"http_surface_revision": cached.HTTPSurfaceRevisions,
		"openapi_revision":      cached.OpenAPIRevisions,
	}
	if output == "json" {
		env := newCLIEnvelope(true, data, diagnostics)
		env.WorkspaceRevision = cached.WorkspaceRevision
		env.ContractRevision = cached.ContractRevision
		return json.NewEncoder(stdout).Encode(env)
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintln(stdout, "scenery: contract ok", cached.ContractRevision)
	return err
}
