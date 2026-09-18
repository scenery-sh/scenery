package parse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

// A long-lived caller, such as a development supervisor, analyzes the same
// prepared workspace after every edit, and loading every package of the target
// costs the same whatever changed. An analysis of a workspace without an
// overlay is therefore retained in memory, and the next analysis of the same
// target loads only the packages whose own sources changed and the packages
// that import them, directly or not; every other package keeps the model its
// last successful load produced.
//
// Reuse is decided from bytes, never from times. A retained analysis applies
// only while the target, its environment, the module files and every local
// replacement are the same, and the set of source directories is the same.
// A package is reused only while the names and contents of its own source
// files, and the names of every other file beneath it, are the same. Anything
// the plan cannot prove from that, a new or removed directory, a changed
// directory that is not a loaded package, a vendor tree, a load that reports an
// error or returns other packages than expected, is answered by loading the
// whole target again, which is what happened before retention existed.

// maxRetainedAnalyses bounds the analyses kept in memory.
const maxRetainedAnalyses = 8

// analysisSourceExtensions are the files of a package directory whose contents
// the Go command may compile into the package.
var analysisSourceExtensions = map[string]bool{
	".go": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hh": true, ".hpp": true, ".hxx": true,
	".m": true, ".s": true, ".S": true, ".sx": true, ".swig": true, ".swigcxx": true, ".f": true, ".F": true, ".for": true,
	".f90": true, ".syso": true,
}

// analysisStamp identifies everything an analysis of one module consumed.
type analysisStamp struct {
	// target identifies the target, its environment, flags and patterns.
	target string
	// identity covers the target, the module files and local replacements.
	identity string
	// dirs maps each source directory, relative to the module root, to the
	// digest of what the package there may consume.
	dirs map[string]string
}

type retainedPackage struct {
	model   *model.Package
	dir     string
	imports []string
	// api digests everything another package can observe of this one. A package
	// whose sources changed without changing it, such as a function body edit,
	// leaves every importer's analysis valid.
	api string
}

type retainedAnalysis struct {
	stamp      analysisStamp
	modulePath string
	// packages maps an import path to its retained package.
	packages map[string]*retainedPackage
}

var retainedAnalyses struct {
	sync.Mutex
	byKey map[string]*retainedAnalysis
	order []string
}

func retainedAnalysisFor(key string) *retainedAnalysis {
	retainedAnalyses.Lock()
	defer retainedAnalyses.Unlock()
	return retainedAnalyses.byKey[key]
}

func retainAnalysis(key string, analysis *retainedAnalysis) {
	retainedAnalyses.Lock()
	defer retainedAnalyses.Unlock()
	if retainedAnalyses.byKey == nil {
		retainedAnalyses.byKey = map[string]*retainedAnalysis{}
	}
	if _, exists := retainedAnalyses.byKey[key]; !exists {
		retainedAnalyses.order = append(retainedAnalyses.order, key)
		for len(retainedAnalyses.order) > maxRetainedAnalyses {
			delete(retainedAnalyses.byKey, retainedAnalyses.order[0])
			retainedAnalyses.order = retainedAnalyses.order[1:]
		}
	}
	retainedAnalyses.byKey[key] = analysis
}

func forgetAnalysis(key string) {
	retainedAnalyses.Lock()
	defer retainedAnalyses.Unlock()
	delete(retainedAnalyses.byKey, key)
}

// errAnalysisNotRetainable reports a workspace whose analysis is never
// retained; the caller loads the whole target.
var errAnalysisNotRetainable = errors.New("analysis is not retainable")

// captureAnalysisStamp reads what identifies an analysis of the module at
// moduleRoot for target.
func captureAnalysisStamp(moduleRoot string, target *gotarget.Context, environment, buildFlags, patterns []string) (analysisStamp, error) {
	identity := sha256.New()
	encoded, err := json.Marshal(struct {
		Target      *gotarget.Context
		Environment []string
		BuildFlags  []string
		Patterns    []string
	}{target, environment, buildFlags, patterns})
	if err != nil {
		return analysisStamp{}, err
	}
	_, _ = identity.Write(encoded)
	targetSum := sha256.Sum256(encoded)
	moduleFile, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return analysisStamp{}, err
	}
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		data, err := os.ReadFile(filepath.Join(moduleRoot, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return analysisStamp{}, err
		}
		_, _ = identity.Write([]byte("\x00" + name + "\x00"))
		_, _ = identity.Write(data)
	}
	if _, err := os.Lstat(filepath.Join(moduleRoot, "vendor")); err == nil {
		return analysisStamp{}, errAnalysisNotRetainable
	}
	parsed, err := modfile.Parse("go.mod", moduleFile, nil)
	if err != nil {
		return analysisStamp{}, err
	}
	for _, replacement := range parsed.Replace {
		if replacement.New.Version != "" {
			continue
		}
		// A local replacement is source the module consumes without a checksum,
		// so its bytes belong to the identity.
		local := replacement.New.Path
		if !filepath.IsAbs(local) {
			local = filepath.Join(moduleRoot, local)
		}
		if contentAddressedDir(local) {
			// The module file, already part of the identity, names the digest of
			// this directory's content, which its owner verifies before it is
			// used; reading a whole framework snapshot here would cost more than
			// the load this stamp avoids.
			continue
		}
		dirs, err := analysisSourceDirs(local)
		if err != nil {
			return analysisStamp{}, err
		}
		_, _ = identity.Write([]byte("\x00replace\x00" + replacement.Old.Path + "\x00" + digestAnalysisDirs(dirs)))
	}
	dirs, err := analysisSourceDirs(moduleRoot)
	if err != nil {
		return analysisStamp{}, err
	}
	return analysisStamp{target: hex.EncodeToString(targetSum[:]), identity: hex.EncodeToString(identity.Sum(nil)), dirs: dirs}, nil
}

// contentAddressedDir reports a directory named by a SHA-256 digest, as a
// content-bound framework source snapshot is.
func contentAddressedDir(path string) bool {
	name := filepath.Base(filepath.Clean(path))
	if len(name) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil && strings.ToLower(name) == name
}

func digestAnalysisDirs(dirs map[string]string) string {
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = hash.Write([]byte(name + "\x00" + dirs[name] + "\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// analysisSourceDirs digests every source directory beneath root that the Go
// command matches with `./...`: it skips directories whose name starts with a
// dot or an underscore, `testdata`, and nested modules. A directory without a
// source file of its own belongs to the nearest source directory above it, and
// contributes the names of its files there, because an embed pattern may reach
// them.
func analysisSourceDirs(root string) (map[string]string, error) {
	type pending struct {
		sources []string
		others  []string
	}
	collected := map[string]*pending{}
	var owners []string
	var walk func(dir, rel, owner string) error
	walk = func(dir, rel, owner string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var sources, others, children []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
					continue
				}
				children = append(children, name)
				continue
			}
			if !entry.Type().IsRegular() {
				others = append(others, name+"\x00irregular")
				continue
			}
			if rel != "" && name == "go.mod" {
				// A nested module is not part of this one.
				return nil
			}
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			if analysisSourceExtensions[filepath.Ext(name)] {
				sources = append(sources, name)
			} else {
				others = append(others, name)
			}
		}
		if len(sources) > 0 {
			owner = rel
			collected[owner] = &pending{}
			owners = append(owners, owner)
		}
		if owner != "\x00none" {
			entry := collected[owner]
			for _, name := range sources {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					return err
				}
				sum := sha256.Sum256(data)
				entry.sources = append(entry.sources, name+"\x00"+hex.EncodeToString(sum[:]))
			}
			beneath := strings.TrimPrefix(strings.TrimPrefix(rel, owner), "/")
			for _, name := range others {
				entry.others = append(entry.others, filepath.ToSlash(filepath.Join(beneath, name)))
			}
		}
		for _, child := range children {
			childRel := child
			if rel != "" {
				childRel = rel + "/" + child
			}
			if err := walk(filepath.Join(dir, child), childRel, owner); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, "", "\x00none"); err != nil {
		return nil, err
	}
	dirs := make(map[string]string, len(owners))
	for _, owner := range owners {
		entry := collected[owner]
		sort.Strings(entry.sources)
		sort.Strings(entry.others)
		hash := sha256.New()
		for _, source := range entry.sources {
			_, _ = hash.Write([]byte(source + "\x00"))
		}
		_, _ = hash.Write([]byte("\x00others\x00"))
		for _, other := range entry.others {
			_, _ = hash.Write([]byte(other + "\x00"))
		}
		dirs[owner] = hex.EncodeToString(hash.Sum(nil))
	}
	return dirs, nil
}

// analysisPlan is what the next analysis must load.
type analysisPlan struct {
	// full loads the whole target; reason says why retention did not apply.
	full   bool
	reason string
	// dirs are the module-relative directories whose own sources changed,
	// sorted; none means the retained analysis is current.
	dirs []string
}

// planAnalysis decides what to load for stamp given the retained analysis. It
// reads nothing: every decision follows from the two stamps and the retained
// import edges.
func planAnalysis(retained *retainedAnalysis, stamp analysisStamp) analysisPlan {
	if retained == nil {
		return analysisPlan{full: true, reason: "no retained analysis"}
	}
	if retained.stamp.identity != stamp.identity {
		return analysisPlan{full: true, reason: "target, module files or a local replacement changed"}
	}
	if len(retained.stamp.dirs) != len(stamp.dirs) {
		return analysisPlan{full: true, reason: "source directories were added or removed"}
	}
	byDir := make(map[string]*retainedPackage, len(retained.packages))
	for _, pkg := range retained.packages {
		byDir[pkg.dir] = pkg
	}
	dirs := []string{}
	for dir, digest := range stamp.dirs {
		previous, known := retained.stamp.dirs[dir]
		if !known {
			return analysisPlan{full: true, reason: "source directories were added or removed"}
		}
		if previous == digest {
			continue
		}
		if byDir[dir] == nil {
			// The directory was not a loaded package, so which packages consume it
			// is unknown.
			return analysisPlan{full: true, reason: "a changed directory is not a loaded package"}
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return analysisPlan{dirs: dirs}
}

// importerDirs returns the directories of every retained package that imports,
// directly or not, a package of changed, excluding the loaded directories. The
// edges into a changed package come from packages whose own sources are
// unchanged, so the retained edges are exact.
func (retained *retainedAnalysis) importerDirs(changed []string, loaded map[string]bool) []string {
	importers := map[string][]string{}
	for path, pkg := range retained.packages {
		for _, imported := range pkg.imports {
			importers[imported] = append(importers[imported], path)
		}
	}
	affected := map[string]bool{}
	queue := slices.Clone(changed)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		for _, importer := range importers[path] {
			if !affected[importer] {
				affected[importer] = true
				queue = append(queue, importer)
			}
		}
	}
	dirs := []string{}
	for path := range affected {
		if dir := retained.packages[path].dir; !loaded[dir] {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// mergeAnalysis replaces the packages loaded for dirs in the retained analysis.
// It reports false when the load did not return exactly the packages of those
// directories, which the caller answers with a whole load.
func mergeAnalysis(retained *retainedAnalysis, stamp analysisStamp, dirs []string, loaded []*retainedPackage) (*retainedAnalysis, bool) {
	expected := map[string]bool{}
	for _, dir := range dirs {
		expected[dir] = true
	}
	seen := map[string]bool{}
	for _, pkg := range loaded {
		previous := retained.packages[pkg.model.ImportPath]
		if previous == nil || previous.dir != pkg.dir || !expected[pkg.dir] || seen[pkg.dir] {
			return nil, false
		}
		seen[pkg.dir] = true
	}
	if len(seen) != len(expected) {
		return nil, false
	}
	merged := &retainedAnalysis{stamp: stamp, modulePath: retained.modulePath, packages: make(map[string]*retainedPackage, len(retained.packages))}
	for path, pkg := range retained.packages {
		merged.packages[path] = pkg
	}
	for _, pkg := range loaded {
		merged.packages[pkg.model.ImportPath] = pkg
	}
	return merged, true
}

// app returns the application model of a retained analysis, sorted as a whole
// load sorts it.
func (retained *retainedAnalysis) app(name, root string) *model.App {
	app := &model.App{Name: name, Root: root, ModulePath: retained.modulePath}
	for _, pkg := range retained.packages {
		app.Packages = append(app.Packages, pkg.model)
	}
	slices.SortFunc(app.Packages, func(left, right *model.Package) int {
		return strings.Compare(left.RelDir, right.RelDir)
	})
	return app
}

// packageAPIDigest digests everything another package can observe of pkg: every
// package-level object with its complete type, the value of each constant, and
// the methods of each named type. Unexported objects are included because an
// exported declaration can expose them.
func packageAPIDigest(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	qualifier := func(other *types.Package) string { return other.Path() }
	hash := sha256.New()
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		_, _ = hash.Write([]byte(types.ObjectString(object, qualifier) + "\x00"))
		switch object := object.(type) {
		case *types.Const:
			_, _ = hash.Write([]byte(object.Val().ExactString() + "\x00"))
		case *types.TypeName:
			_, _ = hash.Write([]byte(types.TypeString(object.Type().Underlying(), qualifier) + "\x00"))
			if named, ok := types.Unalias(object.Type()).(*types.Named); ok {
				methods := make([]string, 0, named.NumMethods())
				for index := range named.NumMethods() {
					methods = append(methods, types.ObjectString(named.Method(index), qualifier))
				}
				sort.Strings(methods)
				_, _ = hash.Write([]byte(strings.Join(methods, "\x00") + "\x00"))
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}
