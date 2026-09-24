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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/codegen"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/machine"
)

const (
	buildInputKind             = "scenery.go-build-input-manifest"
	buildInputSchemaDescriptor = machine.ExactSchemaRevision("sha256:0b3dbb89ce6779d9102139831f455f792adee4a3c0e332099816a2761c4d9ec2")
)

// buildInputDigestCacheLimit bounds the retained content digests. One warm
// build of a large application consults its workspace sources, their
// authored copies, generated artifacts, the framework and module dependencies,
// which together exceed 16,384 files; a smaller first-in-first-out bound
// evicts every entry before its next use. An entry is about 200 bytes.
const buildInputDigestCacheLimit = 65_536

type buildInputFileStamp struct {
	Size            int64
	ModTimeUnixNano int64
	Perm            uint32
	ChangeTimeNano  int64
	Device          uint64
	Inode           uint64
}

type buildInputDigestCacheEntry struct {
	stamp  buildInputFileStamp
	digest string
}

var buildInputDigestCache = struct {
	sync.Mutex
	entries map[string]buildInputDigestCacheEntry
	order   []string
}{entries: map[string]buildInputDigestCacheEntry{}}

type buildInputDigestStats struct {
	hits   int
	misses int
}

type BuildInput struct {
	Identity string `json:"identity"`
	Digest   string `json:"digest"`
}

type BuildInputManifest struct {
	machine.ArtifactIdentity
	Target  string       `json:"target"`
	Entries []BuildInput `json:"entries"`
	Digest  string       `json:"digest"`
	// Nonempty only for a freshly discovered, workspace-owned input domain.
	// It is not serialized: an old manifest cannot grant cache admission.
	sharedWorkspace string
	// Best-effort live mutation detection only, never shared-cache admission.
	observed map[string]buildInputFileStamp
	// processes projects development process manifests from the same discovery.
	processes *buildInputProcessGraph
}

// buildInputProcessGraph retains the per-package entries and import edges of
// one discovery so each development process main can be identified by exactly
// the inputs of its Go import closure.
type buildInputProcessGraph struct {
	global   map[string]string
	packages map[string]map[string]string
	imports  map[string][]string
	mains    map[string]string
	// closures memoizes the digest of each package's import closure.
	closures map[string]string
}

type goListPackage struct {
	Dir               string
	ImportPath        string
	Standard          bool
	GoFiles           []string
	CgoFiles          []string
	CFiles            []string
	CXXFiles          []string
	MFiles            []string
	HFiles            []string
	FFiles            []string
	SFiles            []string
	SwigFiles         []string
	SwigCXXFiles      []string
	SysoFiles         []string
	EmbedFiles        []string
	IgnoredGoFiles    []string
	IgnoredOtherFiles []string
	Imports           []string
	ImportMap         map[string]string
	Module            *goListModule
}

type goListModule struct {
	Path     string
	Dir      string
	Version  string
	Sum      string
	GoMod    string
	GoModSum string
	Replace  *goListModule
}

// Ask Go for the complete consumed-file/module projection, without computing
// unrelated package presentation fields such as transitive import summaries.
const goBuildInputFields = "Dir,ImportPath,Standard,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles,SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,IgnoredGoFiles,IgnoredOtherFiles,Imports,ImportMap,Module"

type retainedBuildInputSelection struct {
	Stamp       buildInputFileStamp
	Digest      string
	FullContent bool
}

type retainedBuildInputGraph struct {
	Key         string
	Output      []byte
	Directories map[string]buildInputFileStamp
	Selection   map[string]retainedBuildInputSelection
}

type retainedBuildInputGraphEntry struct {
	sync.Mutex
	state *retainedBuildInputGraph
}

var retainedBuildInputGraphs sync.Map

var runGoInputList = func(ctx context.Context, directory string, environment []string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir, command.Env = directory, environment
	return command.CombinedOutput()
}

// Tests can model filesystems without a change timestamp without skipping
// publication regressions on platforms that normally expose one.
var buildInputLstat = os.Lstat

func buildInputManifest(ctx context.Context, result *Result) (*BuildInputManifest, error) {
	if result == nil || result.Target == nil {
		return nil, fmt.Errorf("build target is unavailable")
	}
	target := result.Target
	args := []string{"list", "-deps", "-json=" + goBuildInputFields}
	args = append(args, target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		args = append(args, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	patterns := append([]string(nil), target.Context.Patterns...)
	patterns = append(patterns, "./scenery_internal_main")
	if info, err := os.Stat(filepath.Join(result.Dir, codegen.ProcessMainRoot)); err == nil && info.IsDir() {
		patterns = append(patterns, "./"+codegen.ProcessMainRoot+"/...")
	}
	slices.Sort(patterns)
	patterns = slices.Compact(patterns)
	args = append(args, patterns...)
	keyData, _ := json.Marshal(struct {
		Arguments   []string
		Environment []string
	}{args, gotarget.Environment(target.Context)})
	keySum := sha256.Sum256(keyData)
	graphKey := hex.EncodeToString(keySum[:])
	cacheValue, _ := retainedBuildInputGraphs.LoadOrStore(filepath.Clean(result.Dir), &retainedBuildInputGraphEntry{})
	entry := cacheValue.(*retainedBuildInputGraphEntry)
	entry.Lock()
	defer entry.Unlock()
	var output []byte
	cache, reason, actions := "miss", "go_list_package_projection", 1
	if entry.state != nil && entry.state.Key == graphKey {
		if current, currentErr := retainedBuildInputGraphCurrent(entry.state); currentErr == nil && current {
			output = append([]byte(nil), entry.state.Output...)
			cache, reason, actions = "hit", "retained_directory_and_import_identity", 0
		}
	}
	started := time.Now()
	var err error
	if len(output) == 0 {
		output, err = runGoInputList(ctx, result.Dir, gotarget.Environment(target.Context), args...)
	}
	RecordStep(ctx, Step{Name: "go.input_discovery", StartedAt: started, Duration: time.Since(started), Cache: cache, Reason: reason, OK: err == nil, Actions: actions})
	if err != nil {
		return nil, fmt.Errorf("go %s failed while producing build inputs: %w\n%s", strings.Join(args, " "), err, output)
	}
	var manifest *BuildInputManifest
	stats := buildInputDigestStats{}
	fingerprintStarted := time.Now()
	manifest, err = buildInputManifestFromGoListObserved(ctx, result, output, &stats)
	RecordStep(ctx, Step{
		Name: "go.input_fingerprint", StartedAt: fingerprintStarted, Duration: time.Since(fingerprintStarted), Cache: "content_stamp",
		Reason: "exact_consumed_bytes", OK: err == nil, Actions: stats.hits + stats.misses, CacheHits: stats.hits, CacheMisses: stats.misses,
	})
	// A retained graph that was just proven current already refreshed its file
	// stamps in place; only a new listing needs a new retained graph.
	if err == nil && cache == "miss" {
		retainStarted := time.Now()
		state, stateErr := newRetainedBuildInputGraph(graphKey, output)
		RecordStep(ctx, Step{Name: "go.input_graph_retain", StartedAt: retainStarted, Duration: time.Since(retainStarted), Cache: cache, Reason: "directory_and_import_identity", OK: stateErr == nil})
		if stateErr != nil {
			return nil, stateErr
		}
		entry.state = state
	}
	return manifest, err
}

func retainedBuildInputGraphCurrent(state *retainedBuildInputGraph) (bool, error) {
	for path, before := range state.Directories {
		info, err := buildInputLstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || buildInputStamp(info) != before {
			return false, nil
		}
	}
	for path, before := range state.Selection {
		info, err := buildInputLstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		stamp := buildInputStamp(info)
		if stamp == before.Stamp {
			continue
		}
		digest, err := retainedBuildInputSelectionIdentity(path, before.FullContent)
		if err != nil || digest != before.Digest {
			return false, nil
		}
		state.Selection[path] = retainedBuildInputSelection{Stamp: stamp, Digest: digest, FullContent: before.FullContent}
	}
	return true, nil
}

func newRetainedBuildInputGraph(key string, output []byte) (*retainedBuildInputGraph, error) {
	state := &retainedBuildInputGraph{Key: key, Output: append([]byte(nil), output...), Directories: map[string]buildInputFileStamp{}, Selection: map[string]retainedBuildInputSelection{}}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode retained Go build input graph: %w", err)
		}
		if pkg.Standard {
			continue
		}
		if err := retainBuildInputDirectory(state.Directories, pkg.Dir); err != nil {
			return nil, err
		}
		for _, name := range append(append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...), pkg.IgnoredGoFiles...) {
			path := filepath.Join(pkg.Dir, filepath.FromSlash(name))
			info, err := buildInputLstat(path)
			if err != nil {
				return nil, err
			}
			digest, err := retainedBuildInputSelectionIdentity(path, false)
			if err != nil {
				return nil, err
			}
			state.Selection[path] = retainedBuildInputSelection{Stamp: buildInputStamp(info), Digest: digest}
		}
		module := pkg.Module
		if module != nil && module.Replace != nil {
			module = module.Replace
		}
		if module != nil && module.GoMod != "" {
			if err := retainBuildInputSelection(state.Selection, module.GoMod, true); err != nil {
				return nil, err
			}
		}
		for _, name := range pkg.EmbedFiles {
			for directory := filepath.Dir(filepath.Join(pkg.Dir, filepath.FromSlash(name))); ; directory = filepath.Dir(directory) {
				if err := retainBuildInputDirectory(state.Directories, directory); err != nil {
					return nil, err
				}
				if directory == pkg.Dir {
					break
				}
			}
		}
	}
	return state, nil
}

func retainBuildInputSelection(target map[string]retainedBuildInputSelection, path string, fullContent bool) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("go graph selection input is not a regular file: %s", path)
	}
	digest, err := retainedBuildInputSelectionIdentity(path, fullContent)
	if err != nil {
		return err
	}
	target[filepath.Clean(path)] = retainedBuildInputSelection{Stamp: buildInputStamp(info), Digest: digest, FullContent: fullContent}
	return nil
}

func retainedBuildInputSelectionIdentity(path string, fullContent bool) (string, error) {
	if !fullContent {
		return sourceSelectionIdentity(path)
	}
	digest, _, err := fileDigest(path)
	return digest, err
}

func retainBuildInputDirectory(target map[string]buildInputFileStamp, path string) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("go build input package directory is not regular: %s", path)
	}
	target[filepath.Clean(path)] = buildInputStamp(info)
	return nil
}

func resetRetainedBuildInputGraphsForTesting() {
	retainedBuildInputGraphs.Range(func(key, _ any) bool {
		retainedBuildInputGraphs.Delete(key)
		return true
	})
}

func buildInputManifestFromGoList(result *Result, output []byte) (*BuildInputManifest, error) {
	return buildInputManifestFromGoListObserved(context.Background(), result, output, nil)
}

func buildInputManifestFromGoListObserved(ctx context.Context, result *Result, output []byte, stats *buildInputDigestStats) (*BuildInputManifest, error) {
	if result == nil || result.Target == nil {
		return nil, fmt.Errorf("build target is unavailable")
	}
	target := result.Target
	entries := map[string]string{}
	workspaceOnly, entrypoint := filepath.IsAbs(result.Dir), false
	observed := map[string]buildInputFileStamp{}
	// The Go command reports package directories through the working directory
	// it resolves itself, which on some systems is the canonical form of a
	// workspace reached through a symbolic link (Darwin's /var is
	// /private/var); entrypoints are recognized under either form.
	workspaces := []string{filepath.Clean(result.Dir)}
	if canonical, err := filepath.EvalSymlinks(result.Dir); err == nil && filepath.Clean(canonical) != workspaces[0] {
		workspaces = append(workspaces, filepath.Clean(canonical))
	}
	processEntrypoint := func(dir string) bool {
		for _, workspace := range workspaces {
			if relative, err := filepath.Rel(filepath.Join(workspace, codegen.ProcessMainRoot), dir); err == nil && filepath.IsLocal(relative) {
				return true
			}
		}
		return false
	}
	applicationEntrypoint := func(dir string) bool {
		for _, workspace := range workspaces {
			if filepath.Clean(dir) == filepath.Join(workspace, "scenery_internal_main") {
				return true
			}
		}
		return false
	}
	processes := &buildInputProcessGraph{packages: map[string]map[string]string{}, imports: map[string][]string{}, mains: map[string]string{}}
	addFileTo := func(destination map[string]string, identity, path string) error {
		workspaceOnly = workspaceOnly && sharedBinaryWorkspacePath(result.Dir, path)
		return observeBuildInputFile(observed, destination, identity, path, stats)
	}
	addFile := func(identity, path string) error { return addFileTo(entries, identity, path) }
	frameworkRoot := ""
	// Stamping a package's consumed files is stat-bound, so packages are read
	// concurrently into their own entry and observation sets and merged in
	// listing order, which keeps discovery deterministic.
	var listed []goListPackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode Go build input graph: %w", err)
		}
		if pkg.Standard {
			continue
		}
		listed = append(listed, pkg)
	}
	read, err := readBuildInputPackages(result.Dir, listed)
	if err != nil {
		return nil, err
	}
	for index, pkg := range listed {
		if stats != nil {
			stats.hits += read[index].stats.hits
			stats.misses += read[index].stats.misses
		}
		workspaceOnly = workspaceOnly && read[index].workspaceOnly
		for path, stamp := range read[index].observed {
			if before, ok := observed[path]; ok && before != stamp {
				return nil, fmt.Errorf("go build input changed during discovery: %s", path)
			}
			observed[path] = stamp
		}
		if err := observeBuildInputPath(observed, pkg.Dir); err != nil {
			return nil, err
		}
		entrypoint = entrypoint || (applicationEntrypoint(pkg.Dir) && len(pkg.GoFiles) > 0)
		// Process entrypoints are separate executables: they identify their
		// own process manifests and never the application executable.
		packageEntries := read[index].entries
		processes.packages[pkg.ImportPath] = packageEntries
		for _, imported := range pkg.Imports {
			if mapped := pkg.ImportMap[imported]; mapped != "" {
				imported = mapped
			}
			processes.imports[pkg.ImportPath] = append(processes.imports[pkg.ImportPath], imported)
		}
		process := processEntrypoint(pkg.Dir)
		if process {
			processes.mains[filepath.Base(pkg.Dir)] = pkg.ImportPath
		}
		if !process {
			maps.Copy(entries, packageEntries)
		}
		if pkg.Module != nil {
			// Only the private main module is in the supported reuse domain.
			// Module-cache paths and even in-workspace replacements are excluded;
			// their resolver inputs need a separate ownership proof.
			workspaceOnly = workspaceOnly && pkg.Module.Replace == nil && pkg.Module.Version == "" &&
				pkg.Module.Dir == result.Dir && pkg.Module.GoMod == filepath.Join(result.Dir, "go.mod")
			module := pkg.Module
			if module.Replace != nil {
				module = module.Replace
			}
			if pkg.Module.Path == "scenery.sh" {
				// Published GoMod files live in the download metadata cache;
				// only Dir identifies the module's actual package source tree.
				if !filepath.IsAbs(module.Dir) || module.GoMod == "" {
					return nil, fmt.Errorf("go build graph omits the selected Scenery module directory or go.mod")
				}
				root := filepath.Clean(module.Dir)
				if frameworkRoot != "" && frameworkRoot != root {
					return nil, fmt.Errorf("go build graph contains multiple Scenery framework roots")
				}
				frameworkRoot = root
			}
			if module.GoMod != "" {
				if err := addFile("module/"+pkg.Module.Path+"/go.mod", module.GoMod); err != nil {
					return nil, err
				}
			}
			identity := pkg.Module.Path + "@" + pkg.Module.Version + "\x00" + pkg.Module.Sum + "\x00" + pkg.Module.GoModSum
			sum := sha256.Sum256([]byte(identity))
			entries["module/"+pkg.Module.Path] = "sha256:" + hex.EncodeToString(sum[:])
		} else {
			workspaceOnly = false
		}
	}
	if frameworkRoot != "" {
		// A build whose request verified this framework source binds that
		// observation; any other build reads the source here.
		source, verified := verifiedFrameworkSource(ctx, frameworkRoot)
		if !verified {
			source, err = FrameworkSourceManifest(frameworkRoot)
			if err != nil {
				return nil, err
			}
		}
		if linkedFrameworkDigest != "" && source.Digest != linkedFrameworkDigest {
			return nil, fmt.Errorf("actual Go build framework does not match the selected Scenery producer; prepare a coherent framework selection")
		}
		producer, err := executableDigest()
		if err != nil {
			return nil, err
		}
		entries["framework/scenery.sh/source"] = source.Digest
		entries["producer/scenery-cli/executable"] = producer
		result.FrameworkSourceRoot, result.FrameworkSourceDigest = source.Root, source.Digest
	}
	for _, relative := range append(stringValuesForBuild(target.Effective["native_inputs"]), stringValuesForBuild(target.Effective["native_input"])...) {
		workspaceOnly = false
		path := filepath.Join(result.AppRoot, filepath.FromSlash(relative))
		if err := filepath.WalkDir(path, func(filePath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return observeBuildInputPath(observed, filePath)
			}
			rel, err := filepath.Rel(path, filePath)
			if err != nil {
				return err
			}
			return addFile("native/"+filepath.ToSlash(relative)+"/"+filepath.ToSlash(rel), filePath)
		}); err != nil {
			return nil, err
		}
	}
	processes.global = map[string]string{}
	for identity, digest := range entries {
		if !strings.HasPrefix(identity, "package/") {
			processes.global[identity] = digest
		}
	}
	manifest := newBuildInputManifest(target.Name, entries)
	manifest.observed = observed
	manifest.processes = processes
	if workspaceOnly && entrypoint && sharedBinaryStandaloneModule(result.Dir) {
		manifest.sharedWorkspace = result.Dir
	}
	return manifest, nil
}

// buildInputPackageRead is one package's consumed files, the paths discovery
// observed while reading them, and whether they all stayed inside the
// workspace's shared reuse domain.
type buildInputPackageRead struct {
	entries       map[string]string
	observed      map[string]buildInputFileStamp
	stats         buildInputDigestStats
	workspaceOnly bool
}

// readBuildInputPackages stamps every listed package's consumed files. Reads
// run concurrently because they are dominated by file metadata lookups; each
// package owns its own maps, so the caller merges them in listing order.
func readBuildInputPackages(workspace string, listed []goListPackage) ([]buildInputPackageRead, error) {
	reads := make([]buildInputPackageRead, len(listed))
	errs := make([]error, len(listed))
	workers := min(max(runtime.GOMAXPROCS(0), 1), 8)
	slots := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for index, pkg := range listed {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			reads[index], errs[index] = readBuildInputPackage(workspace, pkg)
		})
	}
	wg.Wait()
	return reads, errors.Join(errs...)
}

func readBuildInputPackage(workspace string, pkg goListPackage) (buildInputPackageRead, error) {
	read := buildInputPackageRead{entries: map[string]string{}, observed: map[string]buildInputFileStamp{}}
	// Native/assembly tools can read includes and link inputs outside Go's
	// file projection. No shared executable until those reads are owned.
	read.workspaceOnly = sharedBinaryWorkspacePath(workspace, pkg.Dir) &&
		len(pkg.CgoFiles)+len(pkg.CFiles)+len(pkg.CXXFiles)+len(pkg.MFiles)+len(pkg.HFiles)+len(pkg.FFiles)+len(pkg.SFiles)+len(pkg.SwigFiles)+len(pkg.SwigCXXFiles)+len(pkg.SysoFiles) == 0
	files := append([]string{}, pkg.GoFiles...)
	for _, group := range [][]string{pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles} {
		files = append(files, group...)
	}
	for _, name := range files {
		path := filepath.Join(pkg.Dir, filepath.FromSlash(name))
		identity := "package/" + pkg.ImportPath + "/" + filepath.ToSlash(name)
		if err := observeBuildInputDirectories(read.observed, filepath.Dir(path), pkg.Dir); err != nil {
			return read, err
		}
		read.workspaceOnly = read.workspaceOnly && sharedBinaryWorkspacePath(workspace, path)
		if err := observeBuildInputFile(read.observed, read.entries, identity, path, &read.stats); err != nil {
			return read, err
		}
	}
	return read, nil
}

func newBuildInputManifest(target string, entries map[string]string) *BuildInputManifest {
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	manifest := &BuildInputManifest{ArtifactIdentity: machine.NewArtifactIdentity(buildInputKind, buildInputSchemaDescriptor), Target: target}
	for _, identity := range identities {
		manifest.Entries = append(manifest.Entries, BuildInput{Identity: identity, Digest: entries[identity]})
	}
	projection, _ := json.Marshal(struct {
		machine.ArtifactIdentity
		Target  string       `json:"target"`
		Entries []BuildInput `json:"entries"`
	}{manifest.ArtifactIdentity, manifest.Target, manifest.Entries})
	digest := sha256.Sum256(append([]byte("scenery.go-build-input-manifest\x00"), projection...))
	manifest.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return manifest
}

func addBuildInput(entries map[string]string, identity, path string) error {
	return addBuildInputObserved(entries, identity, path, nil)
}

// observeBuildInputFile adds under identity the digest of the regular file at
// path and records in observed the stamp that names that content. One metadata
// read serves the observation and a retained digest; a digest that must be
// read is read between two equal stamps (cachedBuildInputFileDigest).
func observeBuildInputFile(observed map[string]buildInputFileStamp, entries map[string]string, identity, path string, stats *buildInputDigestStats) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("go build input is not a regular non-symlink file: %s", path)
	}
	stamp := buildInputStamp(info)
	clean := filepath.Clean(path)
	if before, ok := observed[clean]; ok && before != stamp {
		return fmt.Errorf("go build input changed during discovery: %s", path)
	}
	digest, hit, err := cachedBuildInputFileDigest(path, info, os.ReadFile)
	if err != nil {
		return err
	}
	if stats != nil {
		if hit {
			stats.hits++
		} else {
			stats.misses++
		}
	}
	if previous := entries[identity]; previous != "" && previous != digest {
		return fmt.Errorf("go build input identity collision: %s", identity)
	}
	entries[identity] = digest
	observed[clean] = stamp
	return nil
}

func addBuildInputObserved(entries map[string]string, identity, path string, stats *buildInputDigestStats) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("go build input is not a regular non-symlink file: %s", path)
	}
	digest, hit, err := cachedBuildInputFileDigest(path, info, os.ReadFile)
	if err != nil {
		return err
	}
	if stats != nil {
		if hit {
			stats.hits++
		} else {
			stats.misses++
		}
	}
	if previous := entries[identity]; previous != "" && previous != digest {
		return fmt.Errorf("go build input identity collision: %s", identity)
	}
	entries[identity] = digest
	return nil
}

// cachedBuildInputFileDigest returns the digest of the content that the stamp
// of before names. A digest retained for that exact stamp, which includes the
// status-change time any write changes, is returned without another metadata
// read: nothing is read, so there is no window for the content to change
// under it. Otherwise the content is read between two equal stamps.
func cachedBuildInputFileDigest(path string, before os.FileInfo, read func(string) ([]byte, error)) (string, bool, error) {
	stamp := buildInputStamp(before)
	canonical := filepath.Clean(path)
	if stamp.ChangeTimeNano != 0 {
		buildInputDigestCache.Lock()
		entry, ok := buildInputDigestCache.entries[canonical]
		buildInputDigestCache.Unlock()
		if ok && entry.stamp == stamp {
			return entry.digest, true, nil
		}
	}
	data, err := read(path)
	if err != nil {
		return "", false, err
	}
	after, err := buildInputLstat(path)
	if err != nil {
		return "", false, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() {
		return "", false, fmt.Errorf("go build input changed type while hashing: %s", path)
	}
	afterStamp := buildInputStamp(after)
	if stamp != afterStamp || afterStamp.ChangeTimeNano == 0 {
		if stamp != afterStamp {
			return "", false, fmt.Errorf("go build input changed while hashing: %s", path)
		}
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:]), false, nil
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	buildInputDigestCache.Lock()
	if _, exists := buildInputDigestCache.entries[canonical]; !exists {
		buildInputDigestCache.order = append(buildInputDigestCache.order, canonical)
	}
	buildInputDigestCache.entries[canonical] = buildInputDigestCacheEntry{stamp: afterStamp, digest: digest}
	for len(buildInputDigestCache.entries) > buildInputDigestCacheLimit && len(buildInputDigestCache.order) > 0 {
		oldest := buildInputDigestCache.order[0]
		buildInputDigestCache.order = buildInputDigestCache.order[1:]
		delete(buildInputDigestCache.entries, oldest)
	}
	buildInputDigestCache.Unlock()
	return digest, false, nil
}

func buildInputStamp(info os.FileInfo) buildInputFileStamp {
	device, inode := buildInputFileIdentity(info)
	return buildInputFileStamp{
		Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), Perm: uint32(info.Mode().Perm()), ChangeTimeNano: buildInputFileChangeTime(info), Device: device, Inode: inode,
	}
}

func buildInputFileIdentity(info os.FileInfo) (uint64, uint64) {
	if info == nil || info.Sys() == nil {
		return 0, 0
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, 0
	}
	read := func(name string) uint64 {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanUint() {
			return field.Uint()
		}
		return 0
	}
	return read("Dev"), read("Ino")
}

func buildInputFileChangeTime(info os.FileInfo) int64 {
	if info == nil || info.Sys() == nil {
		return 0
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	for _, name := range []string{"Ctimespec", "Ctim", "Ctimen"} {
		stamp := value.FieldByName(name)
		if !stamp.IsValid() || stamp.Kind() != reflect.Struct {
			continue
		}
		seconds, nanos := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
		if seconds.IsValid() && nanos.IsValid() && seconds.CanInt() && nanos.CanInt() {
			return seconds.Int()*int64(time.Second) + nanos.Int()
		}
	}
	return 0
}

func stringValuesForBuild(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}

// developmentProcessDigest identifies one process entrypoint by the inputs of
// the packages in its Go import closure plus every module, framework, producer
// and native input of the discovery. Package closure digests are memoized, so
// identifying every entrypoint of an application costs one pass over the import
// graph instead of one input union per entrypoint.
func (manifest *BuildInputManifest) developmentProcessDigest(name string) (string, string, error) {
	graph := manifest.processes
	if graph == nil {
		return "", "", fmt.Errorf("build inputs do not include development process entrypoints")
	}
	main := graph.mains[name]
	if main == "" {
		return "", "", fmt.Errorf("build inputs do not include development process %s", name)
	}
	closure, err := graph.closureDigest(main, map[string]bool{})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte("scenery.go-build-input-process\x00" + manifest.Target + "\x00" + graph.entriesDigest(graph.global) + "\x00" + closure))
	return "sha256:" + hex.EncodeToString(sum[:]), main, nil
}

// closureDigest hashes a package's own inputs with the digests of the packages
// it imports, which identifies every input the package's closure consumes.
func (graph *buildInputProcessGraph) closureDigest(pkg string, visiting map[string]bool) (string, error) {
	if digest, ok := graph.closures[pkg]; ok {
		return digest, nil
	}
	if visiting[pkg] {
		return "", fmt.Errorf("go build graph imports %s cyclically", pkg)
	}
	visiting[pkg] = true
	defer delete(visiting, pkg)
	imported := make([]string, 0, len(graph.imports[pkg]))
	for _, dependency := range graph.imports[pkg] {
		digest, err := graph.closureDigest(dependency, visiting)
		if err != nil {
			return "", err
		}
		imported = append(imported, digest)
	}
	sort.Strings(imported)
	imported = slices.Compact(imported)
	hash := sha256.New()
	_, _ = hash.Write([]byte(pkg + "\x00" + graph.entriesDigest(graph.packages[pkg])))
	for _, digest := range imported {
		_, _ = hash.Write([]byte("\x00" + digest))
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if graph.closures == nil {
		graph.closures = map[string]string{}
	}
	graph.closures[pkg] = digest
	return digest, nil
}

// entriesDigest hashes one input set by identity and content digest.
func (graph *buildInputProcessGraph) entriesDigest(entries map[string]string) string {
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	hash := sha256.New()
	for _, identity := range identities {
		_, _ = hash.Write([]byte(identity + "\x00" + entries[identity] + "\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// developmentProcessInputs projects the complete input set of one process
// entrypoint. Identification uses developmentProcessDigest; this projection
// serves diagnostics and tests that assert membership.
func (manifest *BuildInputManifest) developmentProcessInputs(name string) (*BuildInputManifest, string, error) {
	graph := manifest.processes
	if graph == nil {
		return nil, "", fmt.Errorf("build inputs do not include development process entrypoints")
	}
	main := graph.mains[name]
	if main == "" {
		return nil, "", fmt.Errorf("build inputs do not include development process %s", name)
	}
	entries := maps.Clone(graph.global)
	seen := map[string]bool{}
	pending := []string{main}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		maps.Copy(entries, graph.packages[current])
		pending = append(pending, graph.imports[current]...)
	}
	return newBuildInputManifest(manifest.Target, entries), main, nil
}
