package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/nativebuilddriver"
)

func nativeBuildBootstrapResult(root string) (map[string]any, []string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "generations", "generation-*", "result.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, nil, err
		}
		status, _ := result["status"].(string)
		if status != "bootstrap_complete" && status != "stock_go_build" {
			continue
		}
		result["result_path"] = path
		var buildArgv []string
		if values, ok := result["build_argv"].([]any); ok {
			for _, value := range values {
				if text, ok := value.(string); ok {
					buildArgv = append(buildArgv, text)
				}
			}
		}
		return result, buildArgv, nil
	}
	return nil, nil, fmt.Errorf("bootstrap result is absent beneath %s", root)
}

func nativeBuildLaneLogPath(appRoot, lane string) (string, error) {
	var newest string
	var newestAt time.Time
	err := filepath.Walk(filepath.Join(appRoot, ".scenery", "task-agent"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && filepath.Ext(path) == ".log" && filepath.Base(filepath.Dir(path)) == "dev" {
			if newest == "" || info.ModTime().After(newestAt) {
				newest, newestAt = path, info.ModTime()
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if newest == "" {
		return "", fmt.Errorf("detached runtime log is absent for %s", lane)
	}
	return newest, nil
}

func nativeBuildGenericSource(original []byte, marker string) ([]byte, error) {
	anchor := []byte("func validatedSearchQuery(")
	if bytes.Count(original, anchor) != 1 || strings.ContainsAny(marker, "\"\n\r%") {
		return nil, fmt.Errorf("generic correctness source anchor is absent")
	}
	changed, err := nativeReloadEditedSource(original, marker)
	if err != nil {
		return nil, err
	}
	helper := []byte("func nativeBuildIdentity[T ~string](value T) string { return string(value) }\n\n")
	changed = bytes.Replace(changed, anchor, append(helper, anchor...), 1)
	literal := []byte(strconv.Quote("query must be at most %d characters [" + marker + "]"))
	if bytes.Count(changed, literal) != 1 {
		return nil, fmt.Errorf("generic correctness literal is absent")
	}
	return bytes.Replace(changed, literal, append([]byte("nativeBuildIdentity("), append(literal, ')')...), 1), nil
}

func (run *nativeBuildDriverRun) runNativeBuildDriverCorrectness() (result map[string]any, resultErr error) {
	result = map[string]any{"status": "incomplete", "scope": "actual pinned full-ONLV recipe and authenticated AHJ runtime before primary cohorts"}
	lanes := make([]*nativeBuildDriverLane, 0, 2)
	defer func() { resultErr = errorsJoin(resultErr, run.closeLanes(lanes)) }()
	for slot, backend := range []string{"stock", "driver"} {
		lane, err := run.prepareLane(0, slot, backend)
		if err != nil {
			return result, err
		}
		lanes = append(lanes, lane)
	}
	if !bytes.Equal(lanes[0].original, lanes[1].original) {
		return result, fmt.Errorf("correctness lanes do not share identical AHJ source bytes")
	}
	driver := lanes[1]
	result["resources_before_negative"] = run.nativeBuildResourceSnapshot("correctness-owner", driver)
	if _, err := run.commandEnv(driver.env, driver.appRoot, driver.name+"-down-before-negative", driver.scenery, "down", "-o", "json"); err != nil {
		return result, err
	}
	negative, err := run.runNativeBuildDriverNegativeMatrix(driver)
	result["negative"] = negative
	if err != nil {
		return result, err
	}
	restartStarted := time.Now()
	if _, err := run.commandEnv(driver.env, driver.appRoot, driver.name+"-up-after-negative", driver.scenery, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
		return result, err
	}
	driver.logPath, err = nativeBuildLaneLogPath(driver.appRoot, driver.name)
	if err != nil {
		return result, err
	}
	driver.token, err = nativeBuildDevToken(run.ctx, driver.origin)
	if err != nil {
		return result, err
	}
	restartedIdentity, _, err := nativeBuildAwaitResponse(run.ctx, driver, "query must be at most 200 characters", driver.lastPID)
	if err != nil {
		return result, err
	}
	driver.lastPID, driver.lastIdentity = restartedIdentity.ProcessID, restartedIdentity
	result["driver_runtime_restart"] = map[string]any{"wall_ms": nativeReloadMS(time.Since(restartStarted)), "served_identity": restartedIdentity, "passed": true}

	positive := map[string]any{}
	bodyA, err := nativeReloadEditedSource(lanes[0].original, "matrix-size-a")
	if err != nil {
		return result, err
	}
	bodyB, err := nativeReloadEditedSource(lanes[0].original, "matrix-size-b")
	if err != nil {
		return result, err
	}
	if len(bodyA) != len(bodyB) {
		return result, fmt.Errorf("same-size correctness sources differ in length")
	}
	for _, lane := range lanes {
		row, err := run.measureLaneSource(lane, 0, 1, 0, "matrix-size-a", bodyA, "query must be at most 200 characters [matrix-size-a]", nil)
		if err != nil {
			return result, err
		}
		positive[lane.backend+"_body_a"] = row
	}
	for _, lane := range lanes {
		info, err := os.Stat(lane.source)
		if err != nil {
			return result, err
		}
		mtime := info.ModTime()
		row, err := run.measureLaneSource(lane, 0, 2, 0, "matrix-size-b", bodyB, "query must be at most 200 characters [matrix-size-b]", &mtime)
		if err != nil {
			return result, err
		}
		positive[lane.backend+"_same_size_restored_mtime"] = row
	}
	for _, lane := range lanes {
		row, err := run.measureLaneSource(lane, 0, 3, 0, "matrix-restore-a", lane.original, "query must be at most 200 characters", nil)
		if err != nil {
			return result, err
		}
		if row.ImplementationRevision != lane.initialIdentity.ImplementationRevision {
			return result, fmt.Errorf("%s A/B/A implementation identity did not restore", lane.backend)
		}
		positive[lane.backend+"_a_b_a_restore"] = row
	}
	for _, lane := range lanes {
		generic, err := nativeBuildGenericSource(lane.original, "matrix-generic")
		if err != nil {
			return result, err
		}
		row, err := run.measureLaneSource(lane, 0, 4, 0, "matrix-generic", generic, "query must be at most 200 characters [matrix-generic]", nil)
		if err != nil {
			return result, err
		}
		positive[lane.backend+"_generic_inline_candidate"] = row
		row, err = run.measureLaneSource(lane, 0, 5, 0, "matrix-generic-restore", lane.original, "query must be at most 200 characters", nil)
		if err != nil {
			return result, err
		}
		positive[lane.backend+"_generic_restore"] = row
	}
	result["positive"] = positive
	for name, value := range positive {
		row, ok := value.(nativeBuildDriverSample)
		if !ok || !row.OK {
			return result, fmt.Errorf("positive correctness case %s did not pass", name)
		}
		if row.Backend != "driver" {
			continue
		}
		if strings.Contains(name, "restore") {
			if row.ToolInvocations < 1 || row.LinkMS <= 0 {
				return result, fmt.Errorf("driver restore case %s did not relink a verified cached archive: tools=%d link_ms=%f", name, row.ToolInvocations, row.LinkMS)
			}
			continue
		}
		if !containsString(row.RebuiltPackages, "clean.tech/solar/ahjs") || !containsString(row.RebuiltPackages, "clean.tech/scenery_internal_main") || len(row.RebuiltPackages) < 2 {
			return result, fmt.Errorf("driver case %s did not rebuild transitive consumers: %v", name, row.RebuiltPackages)
		}
	}
	result["differential"] = map[string]any{"stock_and_driver_source_bytes_equal": true, "typed_behavior_equal": true, "driver_rebuilt_transitive_consumers": true, "owner_reused_across_edits": true}

	for _, lane := range lanes {
		if _, err := run.commandEnv(lane.env, lane.appRoot, lane.name+"-down-after-positive", lane.scenery, "down", "-o", "json"); err != nil {
			return result, err
		}
	}
	recipeData, err := os.ReadFile(filepath.Join(driver.stateRoot, "recipe.json"))
	if err != nil {
		return result, err
	}
	var recipe nativebuilddriver.Recipe
	if err := json.Unmarshal(recipeData, &recipe); err != nil {
		return result, err
	}
	ownerPackage, ok := recipe.Bootstrap.Packages["clean.tech/solar/ahjs"]
	if !ok {
		return result, fmt.Errorf("owner rejection recipe has no AHJ package")
	}
	ownerService := filepath.Join(ownerPackage.Dir, "service.go")
	ownerOriginal, err := os.ReadFile(ownerService)
	if err != nil {
		return result, err
	}
	owner, err := run.runNativeBuildOwnerRejection(driver, recipe, ownerService, ownerOriginal)
	negative["owner_and_cancellation"] = owner
	if err != nil {
		return result, err
	}
	result["status"] = "passed"
	return result, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return fmt.Errorf("%v; %w", left, right)
}

func cloneNativeBuildRecipe(recipe nativebuilddriver.Recipe) (nativebuilddriver.Recipe, error) {
	data, err := json.Marshal(recipe)
	if err != nil {
		return nativebuilddriver.Recipe{}, err
	}
	var result nativebuilddriver.Recipe
	return result, json.Unmarshal(data, &result)
}

type nativeBuildRejectedCase struct {
	Name      string
	Expected  string
	Mutate    func(*nativebuilddriver.Recipe) (func() error, error)
	Configure func(*nativebuilddriver.Recipe, *nativebuilddriver.BuildRequest)
	Cancel    bool
	Mechanism string
}

func (run *nativeBuildDriverRun) runNativeBuildDriverNegativeMatrix(lane *nativeBuildDriverLane) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(lane.stateRoot, "recipe.json"))
	if err != nil {
		return nil, err
	}
	var base nativebuilddriver.Recipe
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, err
	}
	if err := base.Validate(); err != nil {
		return nil, err
	}
	pkg, ok := base.Bootstrap.Packages["clean.tech/solar/ahjs"]
	if !ok {
		return nil, fmt.Errorf("actual AHJ package is absent from retained recipe")
	}
	service := filepath.Join(pkg.Dir, "service.go")
	detail := filepath.Join(pkg.Dir, "detail.go")
	originalService, err := os.ReadFile(service)
	if err != nil {
		return nil, err
	}
	originalDetail, err := os.ReadFile(detail)
	if err != nil {
		return nil, err
	}
	archivePaths := make([]string, 0, len(base.Retained))
	for path := range base.Retained {
		archivePaths = append(archivePaths, path)
	}
	sort.Strings(archivePaths)
	if len(archivePaths) == 0 {
		return nil, fmt.Errorf("actual retained recipe has no archives")
	}
	archive := archivePaths[0]
	compile := base.Compiles["clean.tech/solar/ahjs"]
	if compile == nil {
		return nil, fmt.Errorf("actual AHJ compile action is absent")
	}
	importCfg := compile.Files[compile.ImportCfgAt].Copy
	embed, err := smallestOwnedInput(base, lane.appRoot, func(pkg nativebuilddriver.Package, name string) bool { return containsString(pkg.EmbedFiles, name) })
	if err != nil {
		return nil, err
	}
	localModule, err := ownedExternalModule(base, lane.appRoot)
	if err != nil {
		return nil, err
	}
	module := filepath.Join(base.Workspace, "go.mod")

	cases := []nativeBuildRejectedCase{
		{Name: "syntax_error", Expected: "error", Mechanism: "actual workspace source", Mutate: replaceFileMutation(service, append(append([]byte(nil), originalService...), []byte("\nfunc nativeBuildBroken(\n")...))},
		{Name: "type_error", Expected: "error", Mechanism: "actual workspace source and standard compiler", Mutate: replaceFileMutation(service, append(append([]byte(nil), originalService...), []byte("\nvar nativeBuildTypeError int = \"wrong\"\n")...))},
		{Name: "missing_dependency_archive", Expected: "needs_rebootstrap:retained_archive_invalid", Mechanism: "actual retained archive", Mutate: missingFileMutation(archive)},
		{Name: "corrupt_dependency_archive", Expected: "needs_rebootstrap:retained_archive_invalid", Mechanism: "actual retained archive", Mutate: replaceFileMutation(archive, []byte("corrupt retained archive"))},
		{Name: "corrupt_importcfg", Expected: "needs_rebootstrap:retained_support_invalid", Mechanism: "actual captured compiler importcfg", Mutate: replaceFileMutation(importCfg, []byte("packagefile malformed\n"))},
		{Name: "corrupt_retained_recipe", Expected: "needs_rebootstrap:retained_recipe_invalid", Mechanism: "actual recipe clone", Configure: func(recipe *nativebuilddriver.Recipe, _ *nativebuilddriver.BuildRequest) { recipe.Link = nil }},
		{Name: "add_go_file", Expected: "needs_rebootstrap:package_selection_changed", Mechanism: "actual AHJ package directory", Mutate: createFileMutation(filepath.Join(pkg.Dir, "native_build_added.go"), []byte("package ahjs\n"))},
		{Name: "remove_go_file", Expected: "needs_rebootstrap:package_selection_changed", Mechanism: "actual AHJ source", Mutate: missingFileMutationWithBytes(detail, originalDetail)},
		{Name: "changed_import", Expected: "needs_rebootstrap:package_selection_changed", Mechanism: "actual AHJ source", Mutate: replaceFileMutation(service, bytes.Replace(originalService, []byte("import (\n"), []byte("import (\n\t\"os\"\n"), 1))},
		{Name: "changed_build_tag", Expected: "needs_rebootstrap:package_selection_changed", Mechanism: "actual AHJ source", Mutate: replaceFileMutation(service, append([]byte("//go:build nativebuildmatrix\n\n"), originalService...))},
		{Name: "changed_go_directive", Expected: "needs_rebootstrap:unsupported_input_changed", Mechanism: "actual AHJ source", Mutate: replaceFileMutation(service, bytes.Replace(originalService, []byte("func validatedSearchQuery"), []byte("//go:noinline\nfunc validatedSearchQuery"), 1))},
		{Name: "changed_embed_content", Expected: "needs_rebootstrap:unsupported_input_changed", Mechanism: "actual owned embed input", Mutate: mutateFirstByte(embed)},
		{Name: "changed_embed_membership", Expected: "needs_rebootstrap:package_selection_changed", Mechanism: "actual owned embed directory", Mutate: createFileMutation(filepath.Join(filepath.Dir(embed), "native-build-extra.embed"), []byte("extra"))},
		{Name: "changed_go_mod", Expected: "needs_rebootstrap:unsupported_input_changed", Mechanism: "actual private workspace module", Mutate: appendFileMutation(module, []byte("\n// native build matrix\n"))},
		{Name: "changed_local_replacement", Expected: "needs_rebootstrap:unsupported_input_changed", Mechanism: "actual owned external module metadata", Mutate: appendFileMutation(localModule, []byte("\n// native build matrix\n"))},
		{Name: "changed_build_flag", Expected: "needs_rebootstrap:build_configuration_changed", Mechanism: "actual full capture with changed tags", Configure: func(_ *nativebuilddriver.Recipe, request *nativebuilddriver.BuildRequest) {
			request.BuildFlags = []string{"-tags=nativebuildmatrix"}
		}},
		{Name: "changed_tool_identity", Expected: "needs_rebootstrap:tool_identity_changed", Mechanism: "actual recipe with mismatched expected tool digest", Configure: func(recipe *nativebuilddriver.Recipe, _ *nativebuilddriver.BuildRequest) {
			for tool := range recipe.ToolDigests {
				recipe.ToolDigests[tool] = "sha256:wrong"
				break
			}
		}},
		{Name: "canceled_build", Expected: "error", Mechanism: "pre-canceled actual full capture", Cancel: true},
	}
	results := map[string]any{}
	for _, test := range cases {
		entry, err := run.runNativeBuildRejectedCase(lane, base, test)
		results[test.Name] = entry
		if err != nil {
			return results, err
		}
	}

	manifest, err := cloneCaptureForMatrix(base.Bootstrap)
	if err != nil {
		return results, err
	}
	nativePath := firstNativeInput(manifest)
	if nativePath == "" {
		return results, fmt.Errorf("actual full-ONLV capture has no native input")
	}
	manifest.Files[nativePath] = "sha256:changed-native-input"
	_, nativeReason := base.CheckEligibility(manifest)
	nativePass := strings.HasPrefix(nativeReason, "unsupported_")
	results["changed_native_input"] = map[string]any{"expected": "needs_rebootstrap", "observed_reason": nativeReason, "mechanism": "actual captured native-input manifest mutation; external toolchain/module file was not modified", "passed": nativePass}
	if !nativePass {
		return results, fmt.Errorf("native input mutation was not rejected: %s", nativeReason)
	}

	snapshot, err := run.runNativeBuildSnapshotIsolation(lane, base, service, originalService)
	results["live_source_a_b_a_during_compile"] = snapshot
	if err != nil {
		return results, err
	}
	return results, nil
}

func (run *nativeBuildDriverRun) runNativeBuildRejectedCase(lane *nativeBuildDriverLane, base nativebuilddriver.Recipe, test nativeBuildRejectedCase) (map[string]any, error) {
	recipe, err := cloneNativeBuildRecipe(base)
	if err != nil {
		return nil, err
	}
	request := nativeBuildRequestForMatrix(lane, &recipe, test.Name)
	if test.Configure != nil {
		test.Configure(&recipe, &request)
	}
	restore := func() error { return nil }
	if test.Mutate != nil {
		restore, err = test.Mutate(&recipe)
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = restore() }()
	if err := os.MkdirAll(filepath.Dir(request.Output), 0o700); err != nil {
		return nil, err
	}
	sentinel := []byte("valid predecessor remains published")
	if err := os.WriteFile(request.Output, sentinel, 0o700); err != nil {
		return nil, err
	}
	ctx := run.ctx
	if test.Cancel {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(run.ctx)
		cancel()
	}
	build, buildErr := recipe.Build(ctx, request)
	current, readErr := os.ReadFile(request.Output)
	if readErr != nil {
		return nil, readErr
	}
	observed := build.Status + ":" + build.Reason
	if buildErr != nil {
		observed = "error"
	}
	passed := bytes.Equal(current, sentinel) && ((test.Expected == "error" && buildErr != nil) || (buildErr == nil && observed == test.Expected))
	entry := map[string]any{"expected": test.Expected, "observed": observed, "error": fmt.Sprint(buildErr), "previous_output_unchanged": bytes.Equal(current, sentinel), "mechanism": test.Mechanism, "passed": passed,
		"expected_config": build.ExpectedConfig, "observed_config": build.ObservedConfig, "archive_validation_ms": build.ArchiveMS, "support_validation_ms": build.SupportMS}
	if restoreErr := restore(); restoreErr != nil {
		return entry, restoreErr
	}
	restore = func() error { return nil }
	if !passed {
		return entry, fmt.Errorf("negative case %s failed: expected=%s observed=%s error=%v", test.Name, test.Expected, observed, buildErr)
	}
	return entry, nil
}

func nativeBuildRequestForMatrix(lane *nativeBuildDriverLane, recipe *nativebuilddriver.Recipe, name string) nativebuilddriver.BuildRequest {
	root := filepath.Join(lane.stateRoot, "correctness", name)
	env := append([]string(nil), lane.env...)
	keys := make([]string, 0, len(recipe.Bootstrap.RequestEnv))
	for key := range recipe.Bootstrap.RequestEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = envWithOverrides(env, key+"="+recipe.Bootstrap.RequestEnv[key])
	}
	return nativebuilddriver.BuildRequest{Workspace: recipe.Workspace, Output: filepath.Join(root, "published"), GenerationRoot: root,
		BuildArgv: append([]string(nil), lane.buildArgv...), Environment: env, BuildFlags: append([]string(nil), recipe.Bootstrap.BuildFlags...), CaptureMode: "full"}
}

func replaceFileMutation(path string, replacement []byte) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(_ *nativebuilddriver.Recipe) (func() error, error) {
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		mode := info.Mode().Perm()
		if mode&0o200 == 0 {
			if err := os.Chmod(path, mode|0o200); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(path, replacement, mode|0o200); err != nil {
			_ = os.Chmod(path, mode)
			return nil, err
		}
		return func() error {
			if err := os.Chmod(path, mode|0o200); err != nil {
				return err
			}
			if err := os.WriteFile(path, original, mode|0o200); err != nil {
				return err
			}
			return os.Chmod(path, mode)
		}, nil
	}
}

func appendFileMutation(path string, suffix []byte) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(recipe *nativebuilddriver.Recipe) (func() error, error) {
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return replaceFileMutation(path, append(append([]byte(nil), original...), suffix...))(recipe)
	}
}

func createFileMutation(path string, data []byte) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(_ *nativebuilddriver.Recipe) (func() error, error) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return nil, fmt.Errorf("refusing to replace existing correctness path %s", path)
		}
		parent := filepath.Dir(path)
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return nil, err
		}
		parentMode := parentInfo.Mode().Perm()
		if parentMode&0o200 == 0 {
			if err := os.Chmod(parent, parentMode|0o200); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			_ = os.Chmod(parent, parentMode)
			return nil, err
		}
		return func() error {
			removeErr := os.Remove(path)
			modeErr := os.Chmod(parent, parentMode)
			return errorsJoin(removeErr, modeErr)
		}, nil
	}
}

func missingFileMutation(path string) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(_ *nativebuilddriver.Recipe) (func() error, error) {
		backup := path + ".native-build-matrix"
		if err := os.Rename(path, backup); err != nil {
			return nil, err
		}
		return func() error { return os.Rename(backup, path) }, nil
	}
}

func missingFileMutationWithBytes(path string, original []byte) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(_ *nativebuilddriver.Recipe) (func() error, error) {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		return func() error { return os.WriteFile(path, original, info.Mode().Perm()) }, nil
	}
}

func mutateFirstByte(path string) func(*nativebuilddriver.Recipe) (func() error, error) {
	return func(recipe *nativebuilddriver.Recipe) (func() error, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("cannot mutate empty input %s", path)
		}
		changed := append([]byte(nil), data...)
		changed[0] ^= 0xff
		return replaceFileMutation(path, changed)(recipe)
	}
}

func smallestOwnedInput(recipe nativebuilddriver.Recipe, root string, match func(nativebuilddriver.Package, string) bool) (string, error) {
	best, bestSize := "", int64(^uint64(0)>>1)
	for _, pkg := range recipe.Bootstrap.Packages {
		for _, name := range append(append([]string(nil), pkg.EmbedFiles...), pkg.CFiles...) {
			if !match(pkg, name) {
				continue
			}
			path := filepath.Join(pkg.Dir, name)
			if !pathWithin(root, path) {
				continue
			}
			info, err := os.Stat(path)
			if err == nil && info.Size() < bestSize {
				best, bestSize = path, info.Size()
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("actual recipe has no matching owned input")
	}
	return best, nil
}

func ownedExternalModule(recipe nativebuilddriver.Recipe, root string) (string, error) {
	for path := range recipe.Bootstrap.Files {
		if strings.HasSuffix(path, "go.mod") && pathWithin(root, path) && !pathWithin(recipe.Workspace, path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("actual recipe has no owned external module metadata")
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneCaptureForMatrix(value nativebuilddriver.Capture) (nativebuilddriver.Capture, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nativebuilddriver.Capture{}, err
	}
	var result nativebuilddriver.Capture
	return result, json.Unmarshal(data, &result)
}

func firstNativeInput(capture nativebuilddriver.Capture) string {
	packages := make([]string, 0, len(capture.Packages))
	for name := range capture.Packages {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	for _, name := range packages {
		pkg := capture.Packages[name]
		groups := [][]string{pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles}
		for _, group := range groups {
			if len(group) != 0 {
				return filepath.Join(pkg.Dir, group[0])
			}
		}
	}
	return ""
}

func (run *nativeBuildDriverRun) runNativeBuildSnapshotIsolation(lane *nativeBuildDriverLane, recipe nativebuilddriver.Recipe, service string, original []byte) (map[string]any, error) {
	sourceA, err := nativeReloadEditedSource(original, "captured-source-a")
	if err != nil {
		return nil, err
	}
	sourceB, err := nativeReloadEditedSource(original, "mutable-source-b")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(service, sourceA, 0o600); err != nil {
		return nil, err
	}
	defer func() { _ = os.WriteFile(service, original, 0o600) }()
	request := nativeBuildRequestForMatrix(lane, &recipe, "snapshot-isolation")
	_ = os.Remove(request.Output)
	type outcome struct {
		result nativebuilddriver.BuildResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := recipe.Build(run.ctx, request)
		done <- outcome{result, err}
	}()
	rel, err := filepath.Rel(recipe.Workspace, service)
	if err != nil {
		return nil, err
	}
	snapshot := filepath.Join(request.GenerationRoot, "snapshot", "workspace", rel)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if data, readErr := os.ReadFile(snapshot); readErr == nil && bytes.Equal(data, sourceA) {
			break
		}
		select {
		case observed := <-done:
			return map[string]any{"passed": false, "build": observed.result, "error": fmt.Sprint(observed.err)}, fmt.Errorf("build completed before snapshot mutation barrier")
		default:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("snapshot mutation barrier timed out")
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(service, sourceB, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(service, sourceA, 0o600); err != nil {
		return nil, err
	}
	observed := <-done
	artifact, readErr := os.ReadFile(request.Output)
	passed := observed.err == nil && observed.result.Status == "supported_and_rebuilt" && readErr == nil && bytes.Contains(artifact, []byte("captured-source-a")) && !bytes.Contains(artifact, []byte("mutable-source-b"))
	entry := map[string]any{"passed": passed, "status": observed.result.Status, "error": fmt.Sprint(observed.err), "snapshot_path": snapshot,
		"captured_a_present_in_artifact": bytes.Contains(artifact, []byte("captured-source-a")), "mutable_b_absent_from_artifact": !bytes.Contains(artifact, []byte("mutable-source-b"))}
	if !passed {
		return entry, fmt.Errorf("captured-source isolation proof failed")
	}
	return entry, nil
}

func (run *nativeBuildDriverRun) runNativeBuildOwnerRejection(lane *nativeBuildDriverLane, recipe nativebuilddriver.Recipe, service string, original []byte) (map[string]any, error) {
	changed, err := nativeReloadEditedSource(original, "owner-cancel")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(service, changed, 0o600); err != nil {
		return nil, err
	}
	defer func() { _ = os.WriteFile(service, original, 0o600) }()
	sequence := uint64(readNativeBuildCounter(lane.stateRoot) + 1000)
	request1 := nativebuilddriver.OwnerRequest{Protocol: nativebuilddriver.ProtocolVersion, Session: lane.session, Workspace: recipe.Workspace, Sequence: sequence,
		Build: nativeBuildRequestForMatrix(lane, &recipe, "owner-cancel-old")}
	request2 := nativebuilddriver.OwnerRequest{Protocol: nativebuilddriver.ProtocolVersion, Session: lane.session, Workspace: recipe.Workspace, Sequence: sequence + 1,
		Build: nativeBuildRequestForMatrix(lane, &recipe, "owner-cancel-new")}
	sentinel := []byte("valid predecessor remains published")
	if err := os.MkdirAll(filepath.Dir(request1.Build.Output), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(request1.Build.Output, sentinel, 0o700); err != nil {
		return nil, err
	}
	type ownerOutcome struct {
		response nativebuilddriver.OwnerResponse
		err      error
	}
	first := make(chan ownerOutcome, 1)
	go func() {
		response, err := nativebuilddriver.RequestOwner(run.ctx, lane.socket, request1)
		first <- ownerOutcome{response, err}
	}()
	rel, _ := filepath.Rel(recipe.Workspace, service)
	snapshot := filepath.Join(request1.Build.GenerationRoot, "snapshot", "workspace", rel)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(snapshot); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("owner cancellation barrier timed out")
		}
		time.Sleep(time.Millisecond)
	}
	secondResponse, secondErr := nativebuilddriver.RequestOwner(run.ctx, lane.socket, request2)
	firstObserved := <-first
	foreignResponse, foreignErr := nativebuilddriver.RequestOwner(run.ctx, lane.socket, nativebuilddriver.OwnerRequest{Protocol: nativebuilddriver.ProtocolVersion, Session: lane.session + "-foreign", Workspace: recipe.Workspace, Sequence: sequence + 2, Build: request2.Build})
	staleResponse, staleErr := nativebuilddriver.RequestOwner(run.ctx, lane.socket, nativebuilddriver.OwnerRequest{Protocol: nativebuilddriver.ProtocolVersion, Session: lane.session, Workspace: recipe.Workspace, Sequence: sequence, Build: request2.Build})
	oldOutput, _ := os.ReadFile(request1.Build.Output)
	passed := firstObserved.err == nil && firstObserved.response.Result.Reason == "superseded_or_canceled" && secondErr == nil && secondResponse.Error == "" && secondResponse.Result.Status == "supported_and_rebuilt" &&
		foreignErr == nil && foreignResponse.Result.Reason == "foreign_owner_identity" && staleErr == nil && staleResponse.Result.Reason == "stale_generation" && bytes.Equal(oldOutput, sentinel)
	entry := map[string]any{"passed": passed, "canceled_old": firstObserved.response, "newest_completed": secondResponse, "foreign": foreignResponse, "stale": staleResponse,
		"old_output_unchanged": bytes.Equal(oldOutput, sentinel), "errors": []string{fmt.Sprint(firstObserved.err), fmt.Sprint(secondErr), fmt.Sprint(foreignErr), fmt.Sprint(staleErr)}}
	if !passed {
		return entry, fmt.Errorf("owner cancellation or identity rejection proof failed")
	}
	return entry, nil
}

func (run *nativeBuildDriverRun) nativeBuildResourceSnapshot(name string, lane *nativeBuildDriverLane) map[string]any {
	result := map[string]any{"owner_pid": 0, "runtime_pid": lane.lastPID, "observed": false}
	if lane.owner == nil || lane.owner.Process == nil {
		return result
	}
	pid := lane.owner.Process.Pid
	result["owner_pid"] = pid
	if data, err := run.command(run.repoRoot, name+"-ps", "ps", "-p", strconv.Itoa(pid), "-o", "rss=", "-o", "ppid=", "-o", "comm="); err == nil {
		result["ps"] = strings.TrimSpace(string(data))
		var rssKB int64
		if _, err := fmt.Sscan(string(data), &rssKB); err == nil {
			result["owner_rss_bytes"] = rssKB * 1024
		}
		result["observed"] = true
	}
	if data, err := run.command(run.repoRoot, name+"-lsof", "lsof", "-p", strconv.Itoa(pid)); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > 0 {
			result["owner_file_descriptors"] = len(lines) - 1
		}
	}
	result["process_count"] = 2
	return result
}
