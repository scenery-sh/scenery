package generate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devcache"
	generateapi "scenery.sh/internal/generate/api"
)

type editorWorkOwner = generateapi.EditorWorkOwner

type editorContractModule struct {
	ImportPath string
	Files      map[string][]byte
}

// SyncEditorWorkspace refreshes external contract modules and the managed
// root go.work used by gopls and raw Go commands. It never rewrites an
// unowned workfile.
func SyncEditorWorkspace(result *compiler.Result) error {
	return syncEditorWorkspace(result, false)
}

// SyncEditorWorkspaceMerge explicitly adopts only a tagged Scenery block in
// an existing user workfile. All bytes outside that block remain user-owned.
func SyncEditorWorkspaceMerge(result *compiler.Result) error {
	return syncEditorWorkspace(result, true)
}

func syncEditorWorkspace(result *compiler.Result, requestMerge bool) error {
	return syncEditorWorkspaceWithLockRetry(result, requestMerge, 25*time.Millisecond)
}

func syncEditorWorkspaceWithLockRetry(result *compiler.Result, requestMerge bool, lockRetry time.Duration) error {
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return nil
	}
	root, err := filepath.Abs(result.Root)
	if err != nil {
		return err
	}
	frameworkRoot, frameworkErr := filepath.Abs(app.RepoRoot())
	if frameworkErr == nil && root != frameworkRoot && pathWithin(frameworkRoot, root) {
		return nil
	}
	modules, digest, err := renderEditorContractModules(result)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return nil
	}
	cacheRoot, err := editorCacheRoot(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return err
	}
	unlock, err := lockEditorWorkspace(cacheRoot, lockRetry)
	if err != nil {
		return err
	}
	defer unlock()

	frameworkVersion, err := requiredSceneryVersion(root)
	if err != nil {
		return err
	}
	generation := filepath.Join(cacheRoot, "generations", editorGenerationKey(digest, frameworkVersion))
	moduleDirs, err := materializeEditorGeneration(generation, modules, editorGoVersion(result), frameworkVersion)
	if err != nil {
		return err
	}
	workFile := filepath.Join(root, "go.work")
	ownerFile := filepath.Join(root, ".scenery", "editor", "go-work-owner.json")
	workBytes := renderEditorWorkFile(editorGoVersion(result), moduleDirs, app.RepoRoot())
	workDigest := contentDigest(workBytes)
	current, currentExists, err := readOptionalFile(workFile)
	if err != nil {
		return err
	}
	owner, ownerExists, err := generateapi.ReadEditorWorkOwner(ownerFile)
	if err != nil {
		return err
	}
	merge := requestMerge || ownerExists && owner.Mode == "merge"
	if requestMerge && ownerExists && owner.Mode != "merge" {
		return fmt.Errorf("editor workspace conflict: %s is exclusively Scenery-owned; merge mode is unavailable", workFile)
	}
	if merge {
		return syncMergedEditorWorkFile(root, workFile, ownerFile, current, currentExists, owner, ownerExists, result, moduleDirs, generation)
	}
	if !ownerExists {
		if currentExists || pathExists(filepath.Join(root, "go.work.sum")) || gitTracks(root, "go.work") || gitTracks(root, "go.work.sum") {
			return fmt.Errorf("editor workspace conflict: %s is user-owned; Scenery did not modify it", workFile)
		}
	} else if owner.Path != "go.work" || owner.Generator != generateapi.EditorWorkspaceGenerator {
		return fmt.Errorf("editor workspace conflict: invalid ownership record %s", ownerFile)
	} else if currentExists {
		currentDigest := contentDigest(current)
		if currentDigest != owner.Digest && currentDigest != owner.PreviousDigest {
			return fmt.Errorf("editor workspace conflict: %s changed after Scenery created it; Scenery did not modify it", workFile)
		}
	}
	if currentExists && bytes.Equal(current, workBytes) {
		return finalizeEditorWorkspace(root, ownerFile, editorWorkOwner{
			Path: "go.work", Mode: "exclusive", Digest: workDigest, Application: root, Generator: generateapi.EditorWorkspaceGenerator,
			SpecRevision: result.Manifest.SpecRevision, ContractRevision: result.Manifest.ContractRevision,
		}, generation)
	}
	previousDigest := ""
	if currentExists {
		previousDigest = contentDigest(current)
	}
	pending := editorWorkOwner{
		Path: "go.work", Mode: "exclusive", Digest: workDigest, PreviousDigest: previousDigest, Application: root, Generator: generateapi.EditorWorkspaceGenerator,
		SpecRevision: result.Manifest.SpecRevision, ContractRevision: result.Manifest.ContractRevision,
	}
	if err := writeEditorWorkOwner(ownerFile, pending); err != nil {
		return err
	}
	if err := atomicWrite(workFile, workBytes); err != nil {
		return err
	}
	pending.PreviousDigest = ""
	return finalizeEditorWorkspace(root, ownerFile, pending, generation)
}

const (
	editorMergeBegin = "// scenery:begin managed editor contracts\n"
	editorMergeEnd   = "// scenery:end managed editor contracts\n"
)

func syncMergedEditorWorkFile(root, workFile, ownerFile string, current []byte, currentExists bool, owner editorWorkOwner, ownerExists bool, result *compiler.Result, moduleDirs []string, generation string) error {
	if !currentExists {
		return fmt.Errorf("editor workspace merge requires an existing %s", workFile)
	}
	block := renderEditorMergeBlock(moduleDirs)
	if ownerExists {
		existing, ok := editorMergeBlock(current)
		if !ok || contentDigest(existing) != owner.Digest {
			return fmt.Errorf("editor workspace conflict: Scenery's managed block in %s changed; Scenery did not modify it", workFile)
		}
	}
	merged, err := replaceEditorMergeBlock(current, block, !ownerExists)
	if err != nil {
		return err
	}
	pending := editorWorkOwner{
		Path: "go.work", Mode: "merge", Digest: contentDigest(block), Application: root, Generator: generateapi.EditorWorkspaceGenerator,
		SpecRevision: result.Manifest.SpecRevision, ContractRevision: result.Manifest.ContractRevision,
	}
	if !bytes.Equal(current, merged) {
		if err := writeEditorWorkOwner(ownerFile, pending); err != nil {
			return err
		}
		if err := atomicWrite(workFile, merged); err != nil {
			return err
		}
	}
	if err := writeEditorWorkOwner(ownerFile, pending); err != nil {
		return err
	}
	return pruneEditorGenerations(filepath.Dir(generation), generation)
}

func renderEditorMergeBlock(moduleDirs []string) []byte {
	var builder strings.Builder
	builder.WriteString(editorMergeBegin)
	builder.WriteString("use (\n")
	for _, directory := range moduleDirs {
		builder.WriteString("\t" + strconv.Quote(filepath.ToSlash(directory)) + "\n")
	}
	builder.WriteString(")\n")
	builder.WriteString(editorMergeEnd)
	return []byte(builder.String())
}

func editorMergeBlock(contents []byte) ([]byte, bool) {
	start := bytes.Index(contents, []byte(editorMergeBegin))
	if start < 0 {
		return nil, false
	}
	endRelative := bytes.Index(contents[start:], []byte(editorMergeEnd))
	if endRelative < 0 {
		return nil, false
	}
	end := start + endRelative + len(editorMergeEnd)
	if bytes.Index(contents[end:], []byte(editorMergeBegin)) >= 0 {
		return nil, false
	}
	return append([]byte(nil), contents[start:end]...), true
}

func replaceEditorMergeBlock(contents, block []byte, allowAppend bool) ([]byte, error) {
	existing, ok := editorMergeBlock(contents)
	if !ok {
		if !allowAppend {
			return nil, fmt.Errorf("editor workspace conflict: managed merge block is missing or malformed")
		}
		result := append([]byte(nil), contents...)
		if len(result) > 0 && result[len(result)-1] != '\n' {
			result = append(result, '\n')
		}
		return append(append(result, '\n'), block...), nil
	}
	start := bytes.Index(contents, existing)
	result := append([]byte(nil), contents[:start]...)
	result = append(result, block...)
	result = append(result, contents[start+len(existing):]...)
	return result, nil
}

func finalizeEditorWorkspace(root, ownerFile string, owner editorWorkOwner, generation string) error {
	if err := writeEditorWorkOwner(ownerFile, owner); err != nil {
		return err
	}
	if err := excludeManagedWorkFiles(root); err != nil {
		return err
	}
	return pruneEditorGenerations(filepath.Dir(generation), generation)
}

func renderEditorContractModules(result *compiler.Result) ([]editorContractModule, string, error) {
	files, err := renderExpectedGoContractFiles(result)
	if err != nil {
		return nil, "", err
	}
	byDir := map[string]*editorContractModule{}
	for _, file := range files {
		name := filepath.Base(file.Path)
		if name != "scenery.package-generated.json" && name != "scenery.library-generated.json" {
			continue
		}
		var descriptor struct {
			ImportPath   string `json:"import_path"`
			FacadeImport string `json:"facade_import"`
		}
		if err := json.Unmarshal(file.Bytes, &descriptor); err != nil {
			return nil, "", fmt.Errorf("decode generated contract descriptor %s", file.Path)
		}
		importPath := descriptor.ImportPath
		if name == "scenery.library-generated.json" {
			importPath = descriptor.FacadeImport
		}
		if importPath == "" {
			return nil, "", fmt.Errorf("decode generated contract descriptor %s: missing import path", file.Path)
		}
		byDir[filepath.Clean(filepath.Dir(file.Path))] = &editorContractModule{ImportPath: importPath, Files: map[string][]byte{}}
	}
	for _, file := range files {
		var moduleRoot string
		for root := range byDir {
			if pathWithin(root, file.Path) && len(root) > len(moduleRoot) {
				moduleRoot = root
			}
		}
		if moduleRoot == "" {
			continue
		}
		relative, err := filepath.Rel(moduleRoot, file.Path)
		if err != nil {
			return nil, "", err
		}
		byDir[moduleRoot].Files[filepath.ToSlash(relative)] = append([]byte(nil), file.Bytes...)
	}
	modules := make([]editorContractModule, 0, len(byDir))
	for _, module := range byDir {
		modules = append(modules, *module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].ImportPath < modules[j].ImportPath })
	h := sha256.New()
	for _, module := range modules {
		_, _ = h.Write([]byte(module.ImportPath + "\x00"))
		names := make([]string, 0, len(module.Files))
		for name := range module.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			_, _ = h.Write([]byte(name + "\x00"))
			_, _ = h.Write(module.Files[name])
			_, _ = h.Write([]byte{0})
		}
	}
	return modules, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func materializeEditorGeneration(generation string, modules []editorContractModule, goVersion, frameworkVersion string) ([]string, error) {
	moduleDirs := make([]string, 0, len(modules))
	for _, module := range modules {
		sum := sha256.Sum256([]byte(module.ImportPath))
		moduleDirs = append(moduleDirs, filepath.Join(generation, "contracts", hex.EncodeToString(sum[:8])))
	}
	if pathExists(generation) {
		return moduleDirs, nil
	}
	parent := filepath.Dir(generation)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(parent, ".generation-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	for index, module := range modules {
		relative, _ := filepath.Rel(generation, moduleDirs[index])
		directory := filepath.Join(temporary, relative)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
		goMod := "module " + module.ImportPath + "\n\ngo " + goVersion + "\n\nrequire scenery.sh " + frameworkVersion + "\n"
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
			return nil, err
		}
		for name, contents := range module.Files {
			path := filepath.Join(directory, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, contents, 0o644); err != nil {
				return nil, err
			}
		}
	}
	if err := os.Rename(temporary, generation); err != nil {
		if pathExists(generation) {
			return moduleDirs, nil
		}
		return nil, err
	}
	return moduleDirs, nil
}

func requiredSceneryVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "v0.0.0", nil
		}
		return "", fmt.Errorf("read application go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return "", fmt.Errorf("parse application go.mod: %w", err)
	}
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == "scenery.sh" && strings.TrimSpace(requirement.Mod.Version) != "" {
			return requirement.Mod.Version, nil
		}
	}
	return "v0.0.0", nil
}

func editorGenerationKey(contractDigest, frameworkVersion string) string {
	sum := sha256.Sum256([]byte(contractDigest + "\x00" + frameworkVersion))
	return hex.EncodeToString(sum[:])
}

func renderEditorWorkFile(goVersion string, moduleDirs []string, frameworkRoot string) []byte {
	var builder strings.Builder
	builder.WriteString("// Code generated by Scenery. DO NOT EDIT.\n\n")
	builder.WriteString("go " + goVersion + "\n\nuse (\n\t.\n")
	for _, directory := range moduleDirs {
		builder.WriteString("\t" + strconv.Quote(filepath.ToSlash(directory)) + "\n")
	}
	builder.WriteString(")\n")
	// A source checkout uses the local framework so generated contracts track
	// framework edits immediately. Release binaries built with -trimpath expose
	// a module-relative caller path instead; in that case the app's required
	// scenery.sh version is already the authoritative dependency and no replace
	// directive is needed.
	if filepath.IsAbs(frameworkRoot) && pathExists(filepath.Join(frameworkRoot, "go.mod")) {
		builder.WriteString("\nreplace scenery.sh => " + strconv.Quote(filepath.ToSlash(frameworkRoot)) + "\n")
	}
	return []byte(builder.String())
}

func editorGoVersion(result *compiler.Result) string {
	if result != nil && result.Manifest != nil {
		for _, resource := range result.Manifest.Resources {
			if resource.Kind == "scenery.go-toolchain" {
				if version, ok := resource.Spec["version"].(string); ok && version != "" {
					return strings.TrimPrefix(version, "go")
				}
			}
		}
	}
	return strings.TrimPrefix(runtime.Version(), "go")
}

func editorCacheRoot(appRoot string) (string, error) {
	cacheRoot, err := devcache.Root()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	return filepath.Join(cacheRoot, "editor", hex.EncodeToString(sum[:8])), nil
}

func writeEditorWorkOwner(path string, owner editorWorkOwner) error {
	data, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func excludeManagedWorkFiles(root string) error {
	command := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/exclude")
	output, err := command.Output()
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(data)
	changed := false
	for _, entry := range []string{"/go.work", "/go.work.sum"} {
		found := false
		for line := range strings.SplitSeq(text, "\n") {
			if strings.TrimSpace(line) == entry {
				found = true
				break
			}
		}
		if !found {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += entry + "\n"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWrite(path, []byte(text))
}

func gitTracks(root, relative string) bool {
	command := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", relative)
	return command.Run() == nil
}

func pruneEditorGenerations(root, current string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type generationInfo struct {
		path string
		mod  time.Time
	}
	var generations []generationInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		generations = append(generations, generationInfo{path: filepath.Join(root, entry.Name()), mod: info.ModTime()})
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i].mod.After(generations[j].mod) })
	keptPrevious := false
	for _, generation := range generations {
		if filepath.Clean(generation.path) == filepath.Clean(current) {
			continue
		}
		if !keptPrevious {
			keptPrevious = true
			continue
		}
		if err := os.RemoveAll(generation.path); err != nil {
			return err
		}
	}
	return nil
}

func lockEditorWorkspace(root string, retry time.Duration) (func(), error) {
	path := filepath.Join(root, ".sync.lock")
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("editor workspace sync is already running")
		}
		time.Sleep(retry)
	}
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
