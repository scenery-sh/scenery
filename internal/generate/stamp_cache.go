package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	"scenery.sh/internal/atomicfile"
)

const generateStampCacheVersion = 1

type generateStampState struct {
	Version int                           `json:"version"`
	Scopes  map[string]generateScopeStamp `json:"scopes"`
}

type generateScopeStamp struct {
	InputDigest string            `json:"input_digest"`
	Files       map[string]string `json:"files"`
	Descriptors []string          `json:"descriptors"`
	GoModFiles  map[string]string `json:"go_mod_files,omitempty"`
}

var generateExecutableStamp = sync.OnceValue(func() string {
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
	sum := sha256.Sum256([]byte(exe + "\x00" + info.ModTime().UTC().String() + "\x00" + string(rune(info.Size()))))
	return hex.EncodeToString(sum[:])
})

func generateStampCachePath(root string) string {
	return filepath.Join(root, ".scenery", "gen", ".scenery-generate-state.json")
}

func (op *generationOperation) stampScopeKey() string {
	switch {
	case op.cachedOnly:
		return "cached_typescript"
	case op.typescriptOnly && op.selector == "":
		return "typescript_all"
	case op.typescriptOnly:
		return op.selector
	case op.selector == "":
		return "all"
	default:
		return op.selector
	}
}

func (op *generationOperation) stampTargets() []Resource {
	if op.selector == "contracts" {
		return nil
	}
	targets := typescriptTargets(op.compilation.Manifest.Resources, op.selector)
	if op.cachedOnly {
		targets = cacheTypeScriptTargets(targets)
	}
	return targets
}

func (op *generationOperation) stampCacheEligible() (string, string, bool) {
	compiled := op.compilation
	if !canUseFastProjectionInput(compiled) {
		return "", "", false
	}
	targets := op.stampTargets()
	for _, target := range targets {
		if target.Spec["react"] != nil {
			// React targets run external type-checking over arbitrary transitive
			// imports outside workspace.implementation_root; always run full flow.
			return "", "", false
		}
	}
	exeStamp := generateExecutableStamp()
	if exeStamp == "" {
		return "", "", false
	}
	input := sharedProjectionInput(compiled)
	if input.err != nil {
		return "", "", false
	}
	scopeKey := op.stampScopeKey()
	raw, err := json.Marshal([]any{generateStampCacheVersion, scopeKey, hex.EncodeToString(input.digest[:]), exeStamp})
	if err != nil {
		return "", "", false
	}
	sum := sha256.Sum256(raw)
	return scopeKey, "sha256:" + hex.EncodeToString(sum[:]), true
}

func (op *generationOperation) observeScopeDescriptors() ([]string, bool) {
	compiled := op.compilation
	root := compiled.Root
	var observed []string
	seen := map[string]bool{}
	add := func(path string) {
		rel, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(rel) {
			return
		}
		slash := filepath.ToSlash(rel)
		if !seen[slash] {
			seen[slash] = true
			observed = append(observed, slash)
		}
	}

	includesContracts := !op.cachedOnly && !op.typescriptOnly && (op.selector == "" || op.selector == "contracts")
	targets := op.stampTargets()
	hasSourceTS := false
	var cacheTSRoots []string
	for _, target := range targets {
		if typeScriptMaterialization(target) == "cache" {
			if outRoot, err := typeScriptOutputRoot(compiled, target); err == nil {
				cacheTSRoots = append(cacheTSRoots, outRoot)
			}
		} else {
			hasSourceTS = true
		}
	}
	includesWorkspaceTS := !op.cachedOnly && (op.selector == "" || hasSourceTS)

	if includesContracts || includesWorkspaceTS {
		wantNames := map[string]bool{}
		protected := map[string]bool{}
		if includesContracts {
			for k := range goGeneratedDescriptorNames() {
				wantNames[k] = true
			}
			for k := range protectedGoGeneratedDescriptors(compiled) {
				protected[k] = true
			}
		}
		if includesWorkspaceTS {
			wantNames[typescriptGeneratedDescriptorName] = true
			if op.selector != "" {
				selected := map[string]bool{}
				for _, target := range targets {
					selected[target.Address] = true
				}
				for _, target := range typescriptTargets(compiled.Manifest.Resources, "") {
					if selected[target.Address] {
						continue
					}
					if outRoot, err := typeScriptOutputRoot(compiled, target); err == nil {
						protected[filepath.Clean(filepath.Join(outRoot, typescriptGeneratedDescriptorName))] = true
					}
				}
			}
		}
		descs, err := generatedDescriptorsBeneath(compiled, root)
		if err != nil {
			return nil, false
		}
		for _, d := range descs {
			if !wantNames[filepath.Base(d.path)] || protected[filepath.Clean(d.path)] {
				continue
			}
			if d.symlink {
				return nil, false
			}
			add(d.path)
		}
	}
	for _, outRoot := range cacheTSRoots {
		if !pathExists(outRoot) {
			continue
		}
		descs, err := generatedDescriptorsBeneath(compiled, outRoot)
		if err != nil {
			return nil, false
		}
		for _, d := range descs {
			if filepath.Base(d.path) != typescriptGeneratedDescriptorName {
				continue
			}
			if d.symlink {
				return nil, false
			}
			add(d.path)
		}
	}
	sort.Strings(observed)
	if observed == nil {
		observed = []string{}
	}
	return observed, true
}

func (op *generationOperation) observeGoModStamps() (map[string]string, bool) {
	if op.cachedOnly || op.typescriptOnly || (op.selector != "" && op.selector != "contracts") {
		return nil, true
	}
	goModPath := filepath.Join(op.compilation.Root, "go.mod")
	info, err := os.Lstat(goModPath)
	if os.IsNotExist(err) {
		return map[string]string{"go.mod": ""}, true
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256(data)
	return map[string]string{"go.mod": "sha256:" + hex.EncodeToString(sum[:])}, true
}

func readGenerateStampState(root string) (generateStampState, bool) {
	path := generateStampCachePath(root)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return generateStampState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return generateStampState{}, false
	}
	var state generateStampState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != generateStampCacheVersion || state.Scopes == nil {
		return generateStampState{}, false
	}
	return state, true
}

func (op *generationOperation) tryStampCacheHit() (GenerateResult, bool) {
	scopeKey, expectedDigest, ok := op.stampCacheEligible()
	if !ok {
		return GenerateResult{}, false
	}
	state, ok := readGenerateStampState(op.compilation.Root)
	if !ok {
		return GenerateResult{}, false
	}
	stamp, exists := state.Scopes[scopeKey]
	if !exists || stamp.InputDigest != expectedDigest || len(stamp.Files) == 0 {
		return GenerateResult{}, false
	}
	observedDescriptors, ok := op.observeScopeDescriptors()
	if !ok || !slices.Equal(observedDescriptors, stamp.Descriptors) {
		return GenerateResult{}, false
	}
	if len(stamp.GoModFiles) > 0 {
		observedGoMod, ok := op.observeGoModStamps()
		if !ok || len(observedGoMod) != len(stamp.GoModFiles) {
			return GenerateResult{}, false
		}
		for k, want := range stamp.GoModFiles {
			if observedGoMod[k] != want {
				return GenerateResult{}, false
			}
		}
	}

	root := op.compilation.Root
	checked := make([]string, 0, len(stamp.Files))
	parentDirs := map[string]struct{}{}
	for rel := range stamp.Files {
		checked = append(checked, rel)
		parentDirs[filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))] = struct{}{}
	}
	for dir := range parentDirs {
		if err := rejectGeneratedPathSymlinks(root, dir); err != nil {
			return GenerateResult{}, false
		}
	}
	sort.Strings(checked)

	var failed atomic.Bool
	workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(checked))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1)) - 1
				if i >= len(checked) {
					return
				}
				rel := checked[i]
				abs := filepath.Join(root, filepath.FromSlash(rel))
				info, err := os.Lstat(abs)
				if err != nil || !info.Mode().IsRegular() {
					failed.Store(true)
					return
				}
				data, err := os.ReadFile(abs)
				if err != nil {
					failed.Store(true)
					return
				}
				sum := sha256.Sum256(data)
				if "sha256:"+hex.EncodeToString(sum[:]) != stamp.Files[rel] {
					failed.Store(true)
					return
				}
			}
		})
	}
	wg.Wait()
	if failed.Load() {
		return GenerateResult{}, false
	}
	return GenerateResult{
		Changed:     []string{},
		Checked:     checked,
		Compilation: op.compilation,
	}, true
}

func (op *generationOperation) recordStampCache() {
	scopeKey, expectedDigest, ok := op.stampCacheEligible()
	if !ok || len(op.result.Checked) == 0 {
		return
	}
	descriptors, ok := op.observeScopeDescriptors()
	if !ok {
		return
	}
	goModFiles, ok := op.observeGoModStamps()
	if !ok {
		return
	}
	root := op.compilation.Root
	checked := op.result.Checked
	digests := make([]string, len(checked))
	var failed atomic.Bool
	workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(checked))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1)) - 1
				if i >= len(checked) {
					return
				}
				abs := filepath.Join(root, filepath.FromSlash(checked[i]))
				info, err := os.Lstat(abs)
				if err != nil || !info.Mode().IsRegular() {
					failed.Store(true)
					return
				}
				data, err := os.ReadFile(abs)
				if err != nil {
					failed.Store(true)
					return
				}
				sum := sha256.Sum256(data)
				digests[i] = "sha256:" + hex.EncodeToString(sum[:])
			}
		})
	}
	wg.Wait()
	if failed.Load() {
		return
	}
	files := make(map[string]string, len(checked))
	for i, rel := range checked {
		files[rel] = digests[i]
	}
	state, ok := readGenerateStampState(root)
	if !ok {
		state = generateStampState{
			Version: generateStampCacheVersion,
			Scopes:  map[string]generateScopeStamp{},
		}
	}
	state.Scopes[scopeKey] = generateScopeStamp{
		InputDigest: expectedDigest,
		Files:       files,
		Descriptors: descriptors,
		GoModFiles:  goModFiles,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = atomicfile.Write(generateStampCachePath(root), encoded, 0o644, atomicfile.Options{})
}
