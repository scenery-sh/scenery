package nativebuilddriver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Recipe struct {
	Protocol       string                    `json:"protocol"`
	Root           string                    `json:"root"`
	Workspace      string                    `json:"workspace"`
	ToolDigests    map[string]string         `json:"tool_digests"`
	Bootstrap      Capture                   `json:"bootstrap_capture"`
	Compiles       map[string]*CompileAction `json:"compiles"`
	Link           *LinkAction               `json:"link"`
	ArchiveByOld   map[string]string         `json:"archive_by_old"`
	RetainedBytes  int64                     `json:"retained_bytes"`
	RetentionLimit int64                     `json:"retention_limit"`
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

func LoadRecordedRecipe(recordRoot, workspace string, bootstrap Capture) (*Recipe, error) {
	recipe := &Recipe{Protocol: ProtocolVersion, Root: recordRoot, Workspace: workspace,
		Bootstrap: bootstrap, Compiles: map[string]*CompileAction{}, ArchiveByOld: map[string]string{}, ToolDigests: map[string]string{}}
	entries, err := filepath.Glob(filepath.Join(recordRoot, "actions", "action-*", "record.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var record ToolRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		if record.Protocol != ProtocolVersion || record.ExitCode != 0 {
			return nil, fmt.Errorf("invalid recorded action %s", path)
		}
		base := filepath.Base(record.Tool)
		switch base {
		case "compile":
			if record.Output == nil || flagValue(record.Argv, "-p") == "" || flagValue(record.Argv, "-V") != "" {
				continue
			}
			if err := retainToolDigest(recipe, record.Tool); err != nil {
				return nil, err
			}
			finalOutput, err := CopyRegular(record.Output.Original, filepath.Join(filepath.Dir(record.Output.Copy), "final-output"))
			if err != nil {
				return nil, fmt.Errorf("capture finalized archive for %s: %w", record.ID, err)
			}
			imports, cfgAt, err := actionImportCfg(record)
			if err != nil {
				return nil, err
			}
			pkg := flagValue(record.Argv, "-p")
			if _, exists := recipe.Compiles[pkg]; exists {
				return nil, fmt.Errorf("duplicate compile recipe for %s", pkg)
			}
			action := &CompileAction{Package: pkg, Tool: record.Tool, Argv: record.Argv,
				Files: record.Files, Output: finalOutput, Imports: imports,
				ImportCfgAt: cfgAt, OutputAt: flagIndex(record.Argv, "-o")}
			recipe.Compiles[pkg] = action
			recipe.ArchiveByOld[action.Output.Original] = action.Output.Copy
			recipe.RetainedBytes += action.Output.Bytes
		case "link":
			if record.Output == nil || flagValue(record.Argv, "-V") != "" {
				continue
			}
			if err := retainToolDigest(recipe, record.Tool); err != nil {
				return nil, err
			}
			imports, cfgAt, err := actionImportCfg(record)
			if err != nil {
				return nil, err
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
		}
	}
	if main := recipe.Compiles["main"]; main != nil && recipe.Link != nil {
		for importPath, pkg := range bootstrap.Packages {
			if pkg.Name == "main" && filepath.Clean(pkg.Dir) == filepath.Join(filepath.Clean(workspace), "scenery_internal_main") {
				delete(recipe.Compiles, "main")
				main.Package = importPath
				recipe.Compiles[importPath] = main
				break
			}
		}
	}
	var missing []string
	for importPath := range bootstrap.Packages {
		if importPath != "unsafe" && recipe.Compiles[importPath] == nil {
			missing = append(missing, importPath)
		}
	}
	if len(missing) != 0 || recipe.Link == nil || recipe.Link.MainAt < 0 {
		return nil, fmt.Errorf("incomplete recipe: compiles=%d packages=%d link=%v", len(recipe.Compiles), len(bootstrap.Packages), recipe.Link != nil)
	}
	for old := range recipe.Link.Imports {
		if _, ok := recipe.ArchiveByOld[old]; !ok {
			return nil, fmt.Errorf("link input archive was not retained: %s", old)
		}
	}
	recipe.RetentionLimit = recipe.RetainedBytes*2 + 512<<20
	return recipe, nil
}

func retainToolDigest(recipe *Recipe, tool string) error {
	path := tool
	if !filepath.IsAbs(path) {
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return err
		}
	}
	digest, _, err := FileDigest(path)
	if err != nil {
		return err
	}
	recipe.ToolDigests[path] = digest
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
	defer f.Close()
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

type BuildRequest struct {
	Workspace, Output, GenerationRoot string
	BuildArgv                         []string
	Environment                       []string
	BuildFlags                        []string
}

type BuildResult struct {
	Status          string            `json:"status"`
	CaptureMS       float64           `json:"capture_ms"`
	ValidationMS    float64           `json:"validation_ms"`
	PlanningMS      float64           `json:"planning_ms"`
	CompileMS       float64           `json:"compile_ms"`
	LinkMS          float64           `json:"link_ms"`
	FinalizationMS  float64           `json:"finalization_ms"`
	ArtifactBuildMS float64           `json:"artifact_build_ms"`
	CaptureDigest   string            `json:"capture_digest"`
	ArtifactDigest  string            `json:"artifact_digest"`
	ExecutableBytes int64             `json:"executable_bytes"`
	ChangedPackages []string          `json:"changed_packages"`
	RebuiltPackages []string          `json:"rebuilt_packages"`
	ToolInvocations int               `json:"tool_invocations"`
	ActionArtifacts map[string]string `json:"action_artifacts"`
	Reason          string            `json:"reason,omitempty"`
	ExpectedConfig  *BuildConfig      `json:"expected_config,omitempty"`
	ObservedConfig  *BuildConfig      `json:"observed_config,omitempty"`
}

type BuildConfig struct {
	GoVersion    string            `json:"go_version"`
	GoToolDigest string            `json:"go_tool_digest"`
	BuildFlags   []string          `json:"build_flags"`
	Environment  map[string]string `json:"environment"`
}

func (recipe *Recipe) Build(ctx context.Context, request BuildRequest) (BuildResult, error) {
	var result BuildResult
	result.Status = "needs_rebootstrap"
	capture, err := FullCapture(ctx, request.BuildArgv[0], request.Workspace, filepath.Join(request.GenerationRoot, "snapshot"), request.Environment, request.BuildFlags)
	result.CaptureMS, result.CaptureDigest = capture.DurationMS, capture.Digest
	if err != nil {
		return result, err
	}
	validatedAt := time.Now()
	changed, reason := recipe.eligible(capture)
	result.ValidationMS = float64(time.Since(validatedAt).Nanoseconds()) / 1e6
	if reason != "" {
		result.Reason = reason
		result.ExpectedConfig = captureConfig(recipe.Bootstrap)
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
	buildAt := time.Now()
	archives := make(map[string]string, len(recipe.ArchiveByOld))
	for old, retained := range recipe.ArchiveByOld {
		archives[old] = retained
	}
	result.ActionArtifacts = map[string]string{}
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
		archives[action.Output.Original] = output
		result.ToolInvocations++
	}
	result.CompileMS = float64(time.Since(compileAt).Nanoseconds()) / 1e6
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
	result.ArtifactBuildMS = float64(time.Since(buildAt).Nanoseconds()) / 1e6
	result.ToolInvocations++
	result.Status = "supported_and_rebuilt"
	return result, nil
}

func captureConfig(value Capture) *BuildConfig {
	return &BuildConfig{GoVersion: value.GoVersion, GoToolDigest: value.GoToolDigest, BuildFlags: value.BuildFlags, Environment: value.Environment}
}

func (recipe *Recipe) eligible(current Capture) ([]string, string) {
	if current.GoVersion != recipe.Bootstrap.GoVersion || current.GoToolDigest != recipe.Bootstrap.GoToolDigest {
		return nil, "toolchain_changed"
	}
	if !equalStrings(current.BuildFlags, recipe.Bootstrap.BuildFlags) || !equalStringMaps(current.Environment, recipe.Bootstrap.Environment) {
		return nil, "build_configuration_changed"
	}
	for tool, expected := range recipe.ToolDigests {
		actual, _, err := FileDigest(tool)
		if err != nil || actual != expected {
			return nil, "tool_identity_changed"
		}
	}
	if len(current.Packages) != len(recipe.Bootstrap.Packages) {
		return nil, "package_membership_changed"
	}
	changedSet := map[string]bool{}
	for name, baseline := range recipe.Bootstrap.Packages {
		actual, ok := current.Packages[name]
		if !ok || !samePackageSelection(baseline, actual) {
			return nil, "package_selection_changed"
		}
	}
	for path, baseline := range recipe.Bootstrap.Files {
		actual, ok := current.Files[path]
		if !ok {
			return nil, "input_missing"
		}
		if actual == baseline {
			continue
		}
		if !strings.HasSuffix(path, ".go") || current.Syntax[path] != recipe.Bootstrap.Syntax[path] {
			return nil, "unsupported_input_changed"
		}
		owner := packageForFile(current.Packages, path)
		if owner == "" || len(current.Packages[owner].CgoFiles) != 0 {
			return nil, "unsupported_native_or_unowned_change"
		}
		changedSet[owner] = true
	}
	for path := range current.Files {
		if _, ok := recipe.Bootstrap.Files[path]; !ok {
			return nil, "input_added"
		}
	}
	changed := make([]string, 0, len(changedSet))
	for pkg := range changedSet {
		changed = append(changed, pkg)
	}
	sort.Strings(changed)
	return changed, ""
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
	affected := map[string]bool{}
	queue := append([]string(nil), changed...)
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if affected[pkg] {
			continue
		}
		affected[pkg] = true
		for consumer, value := range recipe.Bootstrap.Packages {
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
		for _, dependency := range recipe.Bootstrap.Packages[pkg].Imports {
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
		if current := capture.SnapshotFiles[file.Original]; current != "" && strings.HasSuffix(file.Original, ".go") {
			args[index] = current
		}
	}
	args = replaceFlagValue(args, "-trimpath", filepath.Join(generationRoot, "snapshot", "workspace")+"=>"+recipe.Workspace)
	return args, nil
}

func (recipe *Recipe) linkArgs(request BuildRequest, archives map[string]string) ([]string, error) {
	args := append([]string(nil), recipe.Link.Argv...)
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

func rewriteImportCfg(source, target string, archives map[string]string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	text := string(data)
	for old, current := range archives {
		text = strings.ReplaceAll(text, old, current)
	}
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(line, "packagefile "); ok {
			_, path, found := strings.Cut(value, "=")
			if found {
				if _, err := os.Stat(path); err != nil {
					return fmt.Errorf("import archive unavailable %s: %w", path, err)
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(text), 0o600)
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

func splitQuoted(input string) ([]string, error) {
	var result []string
	var current strings.Builder
	quote, escaped := rune(0), false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quoted flags")
	}
	flush()
	return result, nil
}
