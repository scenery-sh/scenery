package nativebuilddriver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var ErrIncompleteRecordedRecipe = errors.New("recorded recipe does not cover the current package closure")

type Recipe struct {
	Protocol       string                    `json:"protocol"`
	Root           string                    `json:"root"`
	Workspace      string                    `json:"workspace"`
	ToolDigests    map[string]string         `json:"tool_digests"`
	Retained       map[string]RetainedFile   `json:"retained_artifacts"`
	Support        map[string]RetainedFile   `json:"captured_support_artifacts"`
	Bootstrap      Capture                   `json:"bootstrap_capture"`
	Current        Capture                   `json:"current_capture"`
	Compiles       map[string]*CompileAction `json:"compiles"`
	Link           *LinkAction               `json:"link"`
	ArchiveByOld   map[string]string         `json:"archive_by_old"`
	RetainedBytes  int64                     `json:"retained_bytes"`
	RetentionLimit int64                     `json:"retention_limit"`
}

type RetainedFile struct {
	Digest string    `json:"digest"`
	Bytes  int64     `json:"bytes"`
	Stamp  FileStamp `json:"stamp"`
}

type CompileAction struct {
	Package     string            `json:"package"`
	Tool        string            `json:"tool"`
	Argv        []string          `json:"argv"`
	Files       map[int]FileCopy  `json:"files"`
	Output      FileCopy          `json:"output"`
	Imports     map[string]string `json:"imports"`
	ImportCfgAt int               `json:"importcfg_at"`
	OutputAt    int               `json:"output_at"`
}

type LinkAction struct {
	Tool        string            `json:"tool"`
	Argv        []string          `json:"argv"`
	Files       map[int]FileCopy  `json:"files"`
	Output      FileCopy          `json:"output"`
	Imports     map[string]string `json:"imports"`
	ImportCfgAt int               `json:"importcfg_at"`
	OutputAt    int               `json:"output_at"`
	MainAt      int               `json:"main_at"`
}

// LoadRecordedRecipe turns one recorded stock-Go build into a recipe. Every
// archive, support input and source snapshot it keeps is moved into the
// content-addressed state root, so the recording directory is disposable and
// recipes of one workspace share the state their closures have in common.
func LoadRecordedRecipe(recordRoot, workspace string, bootstrap Capture, stateRoot string) (*Recipe, error) {
	recipe := &Recipe{Protocol: ProtocolVersion, Root: recordRoot, Workspace: workspace,
		Bootstrap: bootstrap, Current: cloneCaptureValue(bootstrap), Compiles: map[string]*CompileAction{}, ArchiveByOld: map[string]string{}, ToolDigests: map[string]string{}, Retained: map[string]RetainedFile{}, Support: map[string]RetainedFile{}}
	linkSeen, err := mergeRecordedActions(recipe, recordRoot, bootstrap)
	if err != nil {
		return nil, err
	}
	var missing []string
	for importPath := range bootstrap.Packages {
		if importPath != "unsafe" && recipe.Compiles[importPath] == nil {
			missing = append(missing, importPath)
		}
	}
	if len(missing) != 0 || !linkSeen || recipe.Link == nil || recipe.Link.MainAt < 0 {
		return nil, fmt.Errorf("incomplete recipe: compiles=%d packages=%d link=%v", len(recipe.Compiles), len(bootstrap.Packages), recipe.Link != nil)
	}
	for old := range recipe.Link.Imports {
		if _, ok := recipe.ArchiveByOld[old]; !ok {
			return nil, fmt.Errorf("link input archive was not retained: %s", old)
		}
	}
	if err := recipe.retainBootstrapState(stateRoot); err != nil {
		return nil, err
	}
	if err := recipe.Validate(); err != nil {
		return nil, err
	}
	return recipe, nil
}

func mergeRecordedActions(recipe *Recipe, recordRoot string, capture Capture) (bool, error) {
	entries, err := filepath.Glob(filepath.Join(recordRoot, "actions", "action-*", "record.json"))
	if err != nil {
		return false, err
	}
	linkSeen := false
	// The recorded compile of the entrypoint names the package "main"; every
	// other action is keyed by import path, so the entrypoint is too.
	mainPackage := capture.Entrypoint
	selection := newCapturedSelection(capture)
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		var record ToolRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return false, err
		}
		if record.Protocol != ProtocolVersion || record.ExitCode != 0 {
			return false, fmt.Errorf("invalid recorded action %s", path)
		}
		if err := selection.validate(record, mainPackage); err != nil {
			return false, err
		}
		base := filepath.Base(record.Tool)
		switch base {
		case "compile":
			if record.Output == nil || flagValue(record.Argv, "-p") == "" || flagValue(record.Argv, "-V") != "" {
				continue
			}
			if err := retainToolDigest(recipe, record.Tool); err != nil {
				return false, err
			}
			finalOutput, err := CopyRegular(record.Output.Original, filepath.Join(filepath.Dir(record.Output.Copy), "final-output"))
			if err != nil {
				return false, fmt.Errorf("capture finalized archive for %s: %w", record.ID, err)
			}
			imports, cfgAt, err := actionImportCfg(record)
			if err != nil {
				return false, err
			}
			if err := retainActionSupport(recipe, record.Files, capture.Files); err != nil {
				return false, err
			}
			pkg := flagValue(record.Argv, "-p")
			if pkg == "main" && mainPackage != "" {
				pkg = mainPackage
			}
			action := &CompileAction{Package: pkg, Tool: record.Tool, Argv: record.Argv,
				Files: record.Files, Output: finalOutput, Imports: imports,
				ImportCfgAt: cfgAt, OutputAt: flagIndex(record.Argv, "-o")}
			recipe.Compiles[pkg] = action
			recipe.ArchiveByOld[action.Output.Original] = action.Output.Copy
			info, err := os.Lstat(action.Output.Copy)
			if err != nil {
				return false, err
			}
			recipe.Retained[action.Output.Copy] = RetainedFile{Digest: action.Output.Digest, Bytes: action.Output.Bytes, Stamp: fileStamp(info)}
			recipe.RetainedBytes += action.Output.Bytes
		case "link":
			if record.Output == nil || flagValue(record.Argv, "-V") != "" {
				continue
			}
			if err := retainToolDigest(recipe, record.Tool); err != nil {
				return false, err
			}
			imports, cfgAt, err := actionImportCfg(record)
			if err != nil {
				return false, err
			}
			if err := retainActionSupport(recipe, record.Files, capture.Files); err != nil {
				return false, err
			}
			mainAt := -1
			for index, file := range record.Files {
				if strings.HasSuffix(file.Original, "/_pkg_.a") && index != cfgAt {
					mainAt = index
				}
			}
			recipe.Link = &LinkAction{Tool: record.Tool, Argv: record.Argv, Files: record.Files,
				Output: *record.Output, Imports: imports, ImportCfgAt: cfgAt,
				OutputAt: flagIndex(record.Argv, "-o"), MainAt: mainAt}
			linkSeen = true
		}
	}
	return linkSeen, nil
}

// capturedSelection is the source selection a capture names: the content of
// every captured input and the directories of its selected packages.
type capturedSelection struct {
	files       map[string]string
	packages    map[string]Package
	directories map[string]bool
}

func newCapturedSelection(capture Capture) capturedSelection {
	selection := capturedSelection{files: capture.Files, packages: capture.Packages, directories: map[string]bool{}}
	for _, pkg := range capture.Packages {
		if pkg.Dir != "" {
			selection.directories[filepath.Clean(pkg.Dir)] = true
		}
	}
	return selection
}

// validate binds a recorded action to the capture its recipe describes. The
// recorder compiled the live workspace, so checking the inputs the capture
// knows is not enough: a source file added to a selected package and removed
// again before the capture is revalidated leaves every captured stamp and
// directory listing unchanged. Every input the tool read from a selected
// package directory must therefore be a captured input with its captured
// content, and every compiled package must be a captured package. Inputs
// outside package directories are the build's own generated files, such as
// import configurations and archives in its work directory.
func (selection capturedSelection) validate(record ToolRecord, mainPackage string) error {
	tool := filepath.Base(record.Tool)
	if tool == "compile" && flagValue(record.Argv, "-V") == "" {
		pkg := flagValue(record.Argv, "-p")
		if pkg == "main" && mainPackage != "" {
			pkg = mainPackage
		}
		if _, captured := selection.packages[pkg]; pkg != "" && pkg != "main" && !captured {
			return fmt.Errorf("recorded compile of %s is outside its captured package selection", pkg)
		}
		if config := flagValue(record.Argv, "-embedcfg"); config != "" {
			if err := selection.validateEmbeds(record, config); err != nil {
				return err
			}
		}
	}
	for _, file := range record.Files {
		digest, captured := selection.files[file.Original]
		if captured {
			if digest != file.Digest {
				return fmt.Errorf("recorded %s input differs from its captured content: %s", tool, file.Original)
			}
			continue
		}
		if selection.directories[filepath.Dir(filepath.Clean(file.Original))] {
			return fmt.Errorf("recorded %s input is outside its captured source selection: %s", tool, file.Original)
		}
	}
	return nil
}

// validateEmbeds requires every file a recorded compile embedded to be a
// captured input. The compiler reads embedded files through its embed
// configuration rather than its arguments, so they are not recorded inputs of
// their own: a file added under an embed pattern and removed again before the
// capture is revalidated would otherwise stay in the archive.
func (selection capturedSelection) validateEmbeds(record ToolRecord, config string) error {
	if !filepath.IsAbs(config) {
		config = filepath.Join(record.CWD, config)
	}
	var recorded *FileCopy
	for _, file := range record.Files {
		if filepath.Clean(file.Original) == filepath.Clean(config) {
			recorded = &file
			break
		}
	}
	if recorded == nil {
		return fmt.Errorf("recorded compile did not retain its embed configuration: %s", config)
	}
	data, err := os.ReadFile(recorded.Copy)
	if err != nil {
		return err
	}
	var embeds struct {
		Files map[string]string
	}
	if err := json.Unmarshal(data, &embeds); err != nil {
		return fmt.Errorf("decode recorded embed configuration %s: %w", config, err)
	}
	for _, original := range embeds.Files {
		if _, captured := selection.files[original]; !captured {
			if _, captured := selection.files[filepath.Clean(original)]; !captured {
				return fmt.Errorf("recorded compile embedded a file outside its captured source selection: %s", original)
			}
		}
	}
	return nil
}

// RefreshRecordedRecipe merges the cache-miss actions from an ordinary stock
// build into the last committed recipe. Compatible actions and archives remain
// reusable; every archive named by a newly recorded action is copied into the
// durable content-addressed store before the refreshed recipe is returned.
func RefreshRecordedRecipe(previous *Recipe, recordRoot string, current Capture, stateRoot string) (*Recipe, error) {
	if err := previous.Validate(); err != nil {
		return nil, err
	}
	next := *previous
	next.Current = cloneCaptureValue(current)
	previousCurrent := previous.currentCapture()
	for original, digest := range next.Current.Files {
		if previousCurrent.Files[original] == digest && previousCurrent.SnapshotFiles[original] != "" {
			next.Current.SnapshotFiles[original] = previousCurrent.SnapshotFiles[original]
		}
	}
	next.Compiles = make(map[string]*CompileAction, len(previous.Compiles))
	for importPath, action := range previous.Compiles {
		next.Compiles[importPath] = action
	}
	next.ToolDigests = cloneStrings(previous.ToolDigests)
	next.ArchiveByOld = cloneStrings(previous.ArchiveByOld)
	next.Retained = cloneRetainedFiles(previous.Retained)
	next.Support = cloneRetainedFiles(previous.Support)
	linkSeen, err := mergeRecordedActions(&next, recordRoot, current)
	if err != nil {
		return nil, err
	}
	if !linkSeen || next.Link == nil {
		return nil, fmt.Errorf("graph refresh did not record a current link action")
	}
	for importPath := range next.Compiles {
		if _, currentPackage := current.Packages[importPath]; !currentPackage {
			delete(next.Compiles, importPath)
		}
	}
	for importPath := range current.Packages {
		if importPath != "unsafe" && next.Compiles[importPath] == nil {
			return nil, fmt.Errorf("%w: compile recipe is absent for %s", ErrIncompleteRecordedRecipe, importPath)
		}
	}
	needed := map[string]bool{}
	for _, action := range next.Compiles {
		needed[action.Output.Original] = true
		for path := range action.Imports {
			needed[path] = true
		}
	}
	for path := range next.Link.Imports {
		needed[path] = true
	}
	needed[next.Link.Files[next.Link.MainAt].Original] = true
	for original := range needed {
		if retained := next.ArchiveByOld[original]; retained != "" {
			if err := validateRetainedFile(retained, next.Retained[retained]); err == nil && !pathWithin(recordRoot, retained) {
				continue
			}
		}
		if err := next.retainArchiveMapping(original, stateRoot); err != nil {
			return nil, err
		}
	}
	for original := range next.ArchiveByOld {
		if !needed[original] {
			delete(next.ArchiveByOld, original)
		}
	}
	if err := next.rebuildRetainedAccounting(); err != nil {
		return nil, err
	}
	if err := next.materializeSupport(stateRoot); err != nil {
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

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && relative != "." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cloneFileCopies(source map[int]FileCopy) map[int]FileCopy {
	result := make(map[int]FileCopy, len(source))
	for index, file := range source {
		result[index] = file
	}
	return result
}

func samePath(left, right string) bool {
	return canonicalRetainedPath(left) == canonicalRetainedPath(right)
}

func canonicalRetainedPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	var suffix []string
	for {
		if evaluated, err := filepath.EvalSymlinks(path); err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				evaluated = filepath.Join(evaluated, suffix[index])
			}
			return filepath.Clean(evaluated)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		suffix = append(suffix, filepath.Base(path))
		path = parent
	}
}

// Validate rejects incomplete or internally inconsistent recorded recipes
// before an owner starts or a build reads retained state.
func (recipe *Recipe) Validate() error {
	if recipe == nil || recipe.Protocol != ProtocolVersion || recipe.Workspace == "" || recipe.Root == "" {
		return fmt.Errorf("recipe identity is incomplete")
	}
	if recipe.Bootstrap.Protocol != ProtocolVersion || recipe.Bootstrap.Digest == "" || len(recipe.Bootstrap.Packages) == 0 {
		return fmt.Errorf("bootstrap capture is incomplete")
	}
	current := recipe.currentCapture()
	if current.Protocol != ProtocolVersion || current.Digest == "" || len(current.Packages) == 0 {
		return fmt.Errorf("current capture is incomplete")
	}
	if recipe.Link == nil || recipe.Link.MainAt < 0 || recipe.Link.OutputAt < 0 || recipe.Link.ImportCfgAt < 0 || recipe.Link.OutputAt+1 >= len(recipe.Link.Argv) {
		return fmt.Errorf("link recipe is incomplete")
	}
	for importPath := range current.Packages {
		if importPath != "unsafe" && recipe.Compiles[importPath] == nil {
			return fmt.Errorf("compile recipe is absent for %s", importPath)
		}
	}
	for importPath, action := range recipe.Compiles {
		if action == nil || action.Package != importPath || action.Tool == "" || action.OutputAt < 0 || action.OutputAt+1 >= len(action.Argv) || action.ImportCfgAt < 0 {
			return fmt.Errorf("compile recipe is invalid for %s", importPath)
		}
		if recipe.ToolDigests[action.Tool] == "" {
			return fmt.Errorf("compiler identity is absent for %s", importPath)
		}
		if file, ok := action.Files[action.ImportCfgAt]; !ok || recipe.Support[file.Copy].Digest == "" {
			return fmt.Errorf("compiler support identity is absent for %s", importPath)
		}
	}
	if recipe.ToolDigests[recipe.Link.Tool] == "" {
		return fmt.Errorf("linker identity is absent")
	}
	if file, ok := recipe.Link.Files[recipe.Link.ImportCfgAt]; !ok || recipe.Support[file.Copy].Digest == "" {
		return fmt.Errorf("linker support identity is absent")
	}
	var retainedBytes int64
	counted := map[string]bool{}
	for old, path := range recipe.ArchiveByOld {
		artifact, ok := recipe.Retained[path]
		if old == "" || path == "" || !ok || artifact.Digest == "" || artifact.Bytes <= 0 {
			return fmt.Errorf("retained archive identity is incomplete for %s", old)
		}
		if !counted[path] {
			retainedBytes += artifact.Bytes
			counted[path] = true
		}
	}
	if retainedBytes != recipe.RetainedBytes || recipe.RetentionLimit < recipe.RetainedBytes {
		return fmt.Errorf("retained archive accounting is inconsistent")
	}
	for old := range recipe.Link.Imports {
		if recipe.ArchiveByOld[old] == "" {
			return fmt.Errorf("link input archive was not retained: %s", old)
		}
	}
	return nil
}

func actionImportCfg(record ToolRecord) (map[string]string, int, error) {
	index := flagIndex(record.Argv, "-importcfg")
	if index < 0 || index+1 >= len(record.Argv) {
		return nil, -1, fmt.Errorf("action has no importcfg: %s", record.ID)
	}
	file, ok := record.Files[index+1]
	if !ok {
		return nil, -1, fmt.Errorf("importcfg was not captured: %s", record.ID)
	}
	result := map[string]string{}
	f, err := os.Open(file.Copy)
	if err != nil {
		return nil, -1, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "packagefile "); ok {
			_, path, found := strings.Cut(value, "=")
			if !found {
				return nil, -1, fmt.Errorf("invalid packagefile line")
			}
			result[path] = value[:len(value)-len(path)-1]
		}
	}
	return result, index + 1, scanner.Err()
}

func (recipe *Recipe) eligible(current Capture) ([]string, string) {
	baseline := recipe.currentCapture()
	if current.Reason != "" {
		return nil, current.Reason
	}
	if current.GoVersion != baseline.GoVersion || current.GoToolDigest != baseline.GoToolDigest {
		return nil, "toolchain_changed"
	}
	if !equalStrings(current.BuildFlags, baseline.BuildFlags) || !equalStringMaps(current.Environment, baseline.Environment) || !equalStringMaps(current.RequestEnv, baseline.RequestEnv) {
		return nil, "build_configuration_changed"
	}
	for tool, expected := range recipe.ToolDigests {
		actual, _, err := FileDigest(tool)
		if err != nil || actual != expected {
			return nil, "tool_identity_changed"
		}
	}
	if len(current.Packages) != len(baseline.Packages) {
		return nil, "package_membership_changed"
	}
	changedSet := map[string]bool{}
	for name, baselinePackage := range baseline.Packages {
		actual, ok := current.Packages[name]
		if !ok || !samePackageSelection(baselinePackage, actual) {
			return nil, "package_selection_changed"
		}
	}
	for path, baselineDigest := range baseline.Files {
		actual, ok := current.Files[path]
		if !ok {
			return nil, "input_missing"
		}
		if actual == baselineDigest {
			continue
		}
		if !strings.HasSuffix(path, ".go") || current.Syntax[path] != baseline.Syntax[path] {
			return nil, "unsupported_input_changed"
		}
		if !withinWorkspace(recipe.Workspace, path) {
			return nil, "unsupported_external_input_changed"
		}
		owner := packageForFile(current.Packages, path)
		if owner == "" || len(current.Packages[owner].CgoFiles) != 0 {
			return nil, "unsupported_native_or_unowned_change"
		}
		changedSet[owner] = true
	}
	for path := range current.Files {
		if _, ok := baseline.Files[path]; !ok {
			return nil, "input_added"
		}
	}
	if !equalStringMaps(current.Directories, baseline.Directories) {
		return nil, "package_selection_changed"
	}
	changed := make([]string, 0, len(changedSet))
	for pkg := range changedSet {
		changed = append(changed, pkg)
	}
	sort.Strings(changed)
	return changed, ""
}

// CheckEligibility exposes the benchmark's frozen-domain decision for explicit
// correctness diagnostics built from an actual captured recipe.
func (recipe *Recipe) CheckEligibility(current Capture) ([]string, string) {
	return recipe.eligible(current)
}

func samePackageSelection(a, b Package) bool {
	return a.Name == b.Name && a.Dir == b.Dir && equalStrings(a.Imports, b.Imports) && equalStringMaps(a.ImportMap, b.ImportMap) && moduleIdentity(a) == moduleIdentity(b) && equalStrings(a.GoFiles, b.GoFiles) &&
		equalStrings(a.CgoFiles, b.CgoFiles) && equalStrings(a.CFiles, b.CFiles) && equalStrings(a.CXXFiles, b.CXXFiles) &&
		equalStrings(a.MFiles, b.MFiles) && equalStrings(a.HFiles, b.HFiles) && equalStrings(a.FFiles, b.FFiles) &&
		equalStrings(a.SFiles, b.SFiles) && equalStrings(a.SwigFiles, b.SwigFiles) && equalStrings(a.SwigCXXFiles, b.SwigCXXFiles) &&
		equalStrings(a.SysoFiles, b.SysoFiles) && equalStrings(a.EmbedFiles, b.EmbedFiles) &&
		equalStrings(a.IgnoredGoFiles, b.IgnoredGoFiles) && equalStrings(a.IgnoredOtherFiles, b.IgnoredOtherFiles)
}

func moduleIdentity(value Package) string {
	if value.Module == nil {
		return ""
	}
	result := value.Module.GoMod
	if value.Module.Replace != nil {
		result += "\x00" + value.Module.Replace.GoMod
	}
	return result
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	return strings.Join(aa, "\x00") == strings.Join(bb, "\x00")
}

func packageForFile(packages map[string]Package, path string) string {
	for name, pkg := range packages {
		for _, file := range append(append([]string(nil), pkg.GoFiles...), pkg.CgoFiles...) {
			if filepath.Join(pkg.Dir, file) == path {
				return name
			}
		}
	}
	return ""
}

func (recipe *Recipe) rebuildOrder(changed []string) ([]string, error) {
	packages := recipe.currentCapture().Packages
	affected := map[string]bool{}
	queue := append([]string(nil), changed...)
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if affected[pkg] {
			continue
		}
		affected[pkg] = true
		for consumer, value := range packages {
			if contains(value.Imports, pkg) {
				queue = append(queue, consumer)
			}
		}
	}
	var order []string
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(pkg string) error {
		if visited[pkg] || !affected[pkg] {
			return nil
		}
		if visiting[pkg] {
			return fmt.Errorf("package cycle at %s", pkg)
		}
		visiting[pkg] = true
		for _, dependency := range packages[pkg].Imports {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[pkg] = false
		visited[pkg] = true
		order = append(order, pkg)
		return nil
	}
	for pkg := range affected {
		if err := visit(pkg); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (recipe *Recipe) unsupportedFrontier(packages []string) string {
	for _, importPath := range packages {
		action := recipe.Compiles[importPath]
		if action == nil {
			return "compile_recipe_missing"
		}
		pkg := recipe.currentCapture().Packages[importPath]
		if len(pkg.CgoFiles)+len(pkg.CFiles)+len(pkg.CXXFiles)+len(pkg.MFiles)+len(pkg.HFiles)+len(pkg.FFiles)+len(pkg.SFiles)+len(pkg.SwigFiles)+len(pkg.SwigCXXFiles)+len(pkg.SysoFiles) != 0 {
			return "unsupported_native_action_frontier"
		}
	}
	return ""
}

func (recipe *Recipe) currentCapture() Capture {
	if recipe.Current.Protocol == ProtocolVersion && recipe.Current.Digest != "" {
		return recipe.Current
	}
	return recipe.Bootstrap
}

func (recipe *Recipe) compileArgs(action *CompileAction, capture Capture, archives map[string]string, output, generationRoot string) ([]string, error) {
	args := append([]string(nil), action.Argv...)
	args[action.OutputAt+1] = output
	args = replaceFlagValue(args, "-buildid", digestName(capture.Digest+action.Package)+"/"+digestName(capture.Digest+action.Package))
	cfg := filepath.Join(generationRoot, "configs", digestName(action.Package)+".importcfg")
	if err := rewriteImportCfg(action.Files[action.ImportCfgAt].Copy, cfg, archives); err != nil {
		return nil, err
	}
	args[action.ImportCfgAt] = cfg
	for index, file := range action.Files {
		if current := capture.SnapshotFiles[file.Original]; current != "" {
			args[index] = current
		} else if _, source := capture.Files[file.Original]; source {
			args[index] = file.Original
		} else {
			args[index] = file.Copy
		}
	}
	args[action.ImportCfgAt] = cfg
	if embedFlagAt := flagIndex(args, "-embedcfg"); embedFlagAt >= 0 {
		embedValueAt := embedFlagAt
		if args[embedFlagAt] == "-embedcfg" {
			embedValueAt++
		}
		file, ok := action.Files[embedValueAt]
		if !ok || file.Copy == "" {
			return nil, fmt.Errorf("captured embed configuration is absent for %s", action.Package)
		}
		embedCfg := filepath.Join(generationRoot, "configs", digestName(action.Package)+".embedcfg")
		if err := rewriteEmbedCfg(file.Copy, embedCfg, capture); err != nil {
			return nil, fmt.Errorf("rewrite embed configuration for %s: %w", action.Package, err)
		}
		args = replaceFlagValue(args, "-embedcfg", embedCfg)
	}
	args = replaceFlagValue(args, "-trimpath", filepath.Join(generationRoot, "snapshot", "workspace")+"=>"+recipe.Workspace)
	return args, nil
}

func (recipe *Recipe) linkArgs(request BuildRequest, archives map[string]string) ([]string, error) {
	args := append([]string(nil), recipe.Link.Argv...)
	for index, file := range recipe.Link.Files {
		args[index] = file.Copy
	}
	args[recipe.Link.OutputAt+1] = request.Output
	cfg := filepath.Join(request.GenerationRoot, "configs", "link.importcfg")
	if err := rewriteImportCfg(recipe.Link.Files[recipe.Link.ImportCfgAt].Copy, cfg, archives); err != nil {
		return nil, err
	}
	args[recipe.Link.ImportCfgAt] = cfg
	mainOriginal := recipe.Link.Files[recipe.Link.MainAt].Original
	main := archives[mainOriginal]
	if main == "" {
		return nil, fmt.Errorf("current main archive is absent")
	}
	args[recipe.Link.MainAt] = main
	identity := linkIdentityArgs(request.BuildArgv)
	if len(identity) == 0 {
		return nil, fmt.Errorf("current linker identity is absent")
	}
	for i := range args {
		if strings.HasPrefix(args[i], "-X=scenery.sh/runtime.linked") {
			args[i] = ""
		}
	}
	filtered := args[:0]
	for _, arg := range args {
		if arg != "" {
			filtered = append(filtered, arg)
		}
	}
	filtered = append(filtered[:len(filtered)-1], append(identity, filtered[len(filtered)-1])...)
	filtered = replaceFlagValue(filtered, "-buildid", digestName(request.GenerationRoot)+"/"+digestName(request.GenerationRoot)+"/"+digestName(request.GenerationRoot)+"/"+digestName(request.GenerationRoot))
	return filtered, nil
}

func linkIdentityArgs(buildArgs []string) []string {
	for _, arg := range buildArgs {
		if value, ok := strings.CutPrefix(arg, "-ldflags="); ok {
			parts, err := splitQuoted(value)
			if err != nil {
				return nil
			}
			var result []string
			for _, part := range parts {
				if strings.HasPrefix(part, "-X=scenery.sh/runtime.linked") {
					result = append(result, part)
				}
			}
			return result
		}
	}
	return nil
}

// rewriteImportCfg rebinds exact packagefile archive paths. Import
// configurations name archives only in those entries, so one map lookup per
// line replaces a whole-file scan per archive; every other line is preserved.
func rewriteImportCfg(source, target string, archives map[string]string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for index, line := range lines {
		value, ok := strings.CutPrefix(line, "packagefile ")
		if !ok {
			continue
		}
		importPath, path, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		if current, mapped := archives[path]; mapped {
			path = current
			lines[index] = "packagefile " + importPath + "=" + path
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("import archive unavailable %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(strings.Join(lines, "\n")), 0o600)
}

func runTool(ctx context.Context, tool, cwd string, env, args []string) error {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir, cmd.Env = cwd, env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w\n%s", filepath.Base(tool), err, output)
	}
	return nil
}

func flagIndex(args []string, name string) int {
	for i, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return i
		}
	}
	return -1
}

func flagValue(args []string, name string) string {
	index := flagIndex(args, name)
	if index < 0 {
		return ""
	}
	if value, ok := strings.CutPrefix(args[index], name+"="); ok {
		return value
	}
	if index+1 < len(args) {
		return args[index+1]
	}
	return ""
}

func replaceFlagValue(args []string, name, value string) []string {
	index := flagIndex(args, name)
	if index < 0 {
		return args
	}
	if strings.HasPrefix(args[index], name+"=") {
		args[index] = name + "=" + value
	} else if index+1 < len(args) {
		args[index+1] = value
	}
	return args
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func digestName(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}
