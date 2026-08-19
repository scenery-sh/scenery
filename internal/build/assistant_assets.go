package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/mcpprojection"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/toolchain"
)

// Eve places a fresh build identifier in generated server modules on every
// invocation.  The identifier is build metadata, not runtime state, so it is
// canonicalized before the capsule is archived.
var eveBuildPathPattern = regexp.MustCompile(`\.eve/builds/[A-Za-z0-9_-]+/`)

// prepareAssistantRuntimeAssets builds the immutable production inputs for
// every declared assistant and writes the generated embed package into the Go
// workspace.  It is called only for a non-reusable build, after authored files
// have been synchronized into that workspace.  The source checkout remains
// read-only throughout the operation.
func prepareAssistantRuntimeAssets(ctx context.Context, result *Result) error {
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil {
		return nil
	}
	assistants := assistantBuildResources(result.Contract.Manifest.Resources)
	if len(assistants) == 0 {
		return nil
	}
	for _, assistant := range assistants {
		if err := validateAssistantRuntimeDependency(result.AppRoot, assistant); err != nil {
			return err
		}
	}
	platform, err := assistantAssetTargetPlatform(result.Target)
	if err != nil {
		return err
	}
	nodeHome, err := resolveManagedNodeHome(ctx, result.AppRoot, platform)
	if err != nil {
		return err
	}
	if platform.GOOS != runtime.GOOS || platform.GOARCH != runtime.GOARCH {
		return fmt.Errorf("assistant asset build target %s cannot execute managed Node on host %s; cross-build capsules are not deterministic", platform.String(), runtime.GOOS+"/"+runtime.GOARCH)
	}
	nodeArchive, err := runtimeassets.BuildArchive(nodeHome)
	if err != nil {
		return fmt.Errorf("archive managed Node home for %s: %w", platform.String(), err)
	}

	workspaceAssetRoot := filepath.Join(result.Dir, ".scenery", "assistant-assets")
	if err := os.MkdirAll(workspaceAssetRoot, 0o700); err != nil {
		return err
	}
	inputs := make([]generate.AssistantAssetInput, 0, len(assistants))
	for _, assistant := range assistants {
		input, err := buildAssistantAsset(ctx, result, assistant, platform, nodeArchive, workspaceAssetRoot)
		if err != nil {
			return err
		}
		inputs = append(inputs, input)
	}
	files, err := generate.RenderAssistantAssetRegistry(result.Contract, inputs)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for relative, data := range files {
		if err := writeGeneratedAssistantAsset(result.Dir, relative, data); err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
	}
	sort.Strings(paths)
	result.GeneratedFiles = appendUniquePaths(result.GeneratedFiles, paths...)
	result.AssistantAssets = make([]generate.AssistantAssetDescriptor, 0, len(inputs))
	for _, input := range inputs {
		result.AssistantAssets = append(result.AssistantAssets, input.Descriptor)
	}
	sort.Slice(result.AssistantAssets, func(i, j int) bool {
		if result.AssistantAssets[i].AssistantAddress != result.AssistantAssets[j].AssistantAddress {
			return result.AssistantAssets[i].AssistantAddress < result.AssistantAssets[j].AssistantAddress
		}
		return result.AssistantAssets[i].Target < result.AssistantAssets[j].Target
	})
	if err := writeAssistantAssetSidecars(result.AppRoot, result.Target.Name, result.AssistantAssets); err != nil {
		return err
	}
	return nil
}

func validateAssistantRuntimeDependency(appRoot string, assistant generate.Resource) error {
	implementation, _ := assistant.Spec["implementation"].(map[string]any)
	packageRelative := stringValueForBuild(implementation["package"])
	packagePath, err := appRelativePath(appRoot, packageRelative)
	if err != nil {
		return fmt.Errorf("assistant %s production asset dependency: package path: %w", assistant.Address, err)
	}
	packageData, err := os.ReadFile(packagePath)
	if err != nil {
		return fmt.Errorf("assistant %s production asset dependency: read package.json: %w", assistant.Address, err)
	}
	var packageJSON struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(packageData, &packageJSON); err != nil {
		return fmt.Errorf("assistant %s production asset dependency: parse package.json: %w", assistant.Address, err)
	}
	if strings.TrimSpace(packageJSON.Dependencies["eve"]) != eve.EveVersion {
		return fmt.Errorf("assistant %s production asset dependency: package.json must pin eve to %s in dependencies", assistant.Address, eve.EveVersion)
	}
	lockRelative := stringValueForBuild(implementation["package_lock"])
	lockPath, err := appRelativePath(appRoot, lockRelative)
	if err != nil {
		return fmt.Errorf("assistant %s production asset dependency: package-lock path: %w", assistant.Address, err)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("assistant %s production asset dependency: read package-lock.json: %w", assistant.Address, err)
	}
	var lockJSON struct {
		Packages map[string]struct {
			Dependencies map[string]string `json:"dependencies"`
			Version      string            `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lockData, &lockJSON); err != nil {
		return fmt.Errorf("assistant %s production asset dependency: parse package-lock.json: %w", assistant.Address, err)
	}
	root, ok := lockJSON.Packages[""]
	if !ok || strings.TrimSpace(root.Dependencies["eve"]) != eve.EveVersion {
		return fmt.Errorf("assistant %s production asset dependency: package-lock.json root must pin eve to %s", assistant.Address, eve.EveVersion)
	}
	installed, ok := lockJSON.Packages["node_modules/eve"]
	if !ok || strings.TrimSpace(installed.Version) != eve.EveVersion {
		return fmt.Errorf("assistant %s production asset dependency: package-lock.json must resolve eve to %s", assistant.Address, eve.EveVersion)
	}
	return nil
}

func assistantAssetTargetPlatform(target *compiler.GoBuildTarget) (toolchain.Platform, error) {
	if target == nil {
		return toolchain.Platform{}, fmt.Errorf("assistant asset build target is unavailable")
	}
	platform := toolchain.Platform{GOOS: strings.TrimSpace(target.Context.GOOS), GOARCH: strings.TrimSpace(target.Context.GOARCH)}
	if platform.String() == "" {
		return toolchain.Platform{}, fmt.Errorf("assistant asset build target has no resolved platform")
	}
	return platform, nil
}

func writeAssistantAssetSidecars(appRoot, target string, descriptors []generate.AssistantAssetDescriptor) error {
	root := filepath.Join(appRoot, ".scenery", "build", "assets", safeAssetKey(target))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, descriptor := range descriptors {
		data, err := json.MarshalIndent(descriptor, "", "  ")
		if err != nil {
			return err
		}
		name := "runtime-descriptor-" + strings.TrimPrefix(descriptor.CapsuleArchiveDigest, "sha256:") + ".json"
		path := filepath.Join(root, name)
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("assistant runtime descriptor is a symlink: %s", path)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := writeAtomicBuildFile(path, append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomicBuildFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".assistant-asset-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func assistantBuildResources(resources []generate.Resource) []generate.Resource {
	assistants := make([]generate.Resource, 0)
	for _, resource := range resources {
		if resource.Kind == "scenery.assistant" && strings.TrimSpace(resource.Address) != "" {
			assistants = append(assistants, resource)
		}
	}
	sort.Slice(assistants, func(i, j int) bool { return assistants[i].Address < assistants[j].Address })
	return assistants
}

func resolveManagedNodeHome(ctx context.Context, appRoot string, platform toolchain.Platform) (string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", fmt.Errorf("load managed Node manifest: %w", err)
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(appRoot), manifest)
	if err != nil {
		return "", fmt.Errorf("open managed toolchain store: %w", err)
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	if _, err := store.Sync(ctx, toolchain.Options{Tool: "node", Platform: platform}); err != nil {
		return "", fmt.Errorf("sync managed Node for %s: %w", platform.String(), err)
	}
	status, err := store.Path(ctx, "node", platform)
	if err != nil {
		return "", fmt.Errorf("resolve managed Node for %s: %w", platform.String(), err)
	}
	if status.Status != "installed" || strings.TrimSpace(status.HomePath) == "" {
		return "", fmt.Errorf("managed Node for %s is not installed: %s", platform.String(), status.Message)
	}
	if info, err := os.Stat(status.HomePath); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("path is not a directory")
		}
		return "", fmt.Errorf("managed Node home for %s is unavailable: %w", platform.String(), err)
	}
	nodePath := managedNodeExecutable(status.HomePath)
	if info, err := os.Stat(nodePath); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		if err == nil {
			err = errors.New("node executable is not executable")
		}
		return "", fmt.Errorf("managed Node executable for %s is unavailable: %w", platform.String(), err)
	}
	return filepath.Clean(status.HomePath), nil
}

func buildAssistantAsset(ctx context.Context, result *Result, assistant generate.Resource, platform toolchain.Platform, nodeArchive runtimeassets.Archive, assetRoot string) (generate.AssistantAssetInput, error) {
	implementation, _ := assistant.Spec["implementation"].(map[string]any)
	sourceRelative := stringValueForBuild(implementation["source"])
	packageRelative := stringValueForBuild(implementation["package"])
	lockRelative := stringValueForBuild(implementation["package_lock"])
	sourceRoot, err := appRelativePath(result.AppRoot, sourceRelative)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s source: %w", assistant.Address, err)
	}
	packagePath, err := appRelativePath(result.AppRoot, packageRelative)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s package: %w", assistant.Address, err)
	}
	lockPath, err := appRelativePath(result.AppRoot, lockRelative)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s package lock: %w", assistant.Address, err)
	}
	if filepath.Dir(packagePath) != filepath.Clean(sourceRoot) || filepath.Dir(lockPath) != filepath.Clean(sourceRoot) {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s package and lock must be directly inside source root", assistant.Address)
	}
	packageBefore, err := os.ReadFile(packagePath)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("read assistant %s package: %w", assistant.Address, err)
	}
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("read assistant %s package lock: %w", assistant.Address, err)
	}
	lockDigest := digestBytesForBuild(lockBefore)
	key := strings.TrimPrefix(lockDigest, "sha256:")
	overlayRoot := filepath.Join(assetRoot, "overlays", safeAssetKey(assistant.Address)+"-"+key)
	if _, err := os.Lstat(overlayRoot); err == nil {
		if err := removeGeneratedAssistantTree(assetRoot, overlayRoot); err != nil {
			return generate.AssistantAssetInput{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return generate.AssistantAssetInput{}, err
	}
	// A deterministic placeholder is used at build time. Supervision supplies
	// the live loopback addresses to the extracted child through its private
	// runtime configuration; no URL or token enters the asset descriptor.
	if result.Contract == nil || !result.Contract.Valid() {
		return generate.AssistantAssetInput{}, fmt.Errorf("project assistant %s MCP approval policy: failed_precondition: compiler result is invalid", assistant.Address)
	}
	expanded, err := result.Contract.ManifestForView("expanded")
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("project assistant %s MCP approval policy: failed_precondition: %w", assistant.Address, err)
	}
	manifest, err := mcpprojection.ProjectManifest(expanded, result.Contract.WorkspaceRevision, referenceValueForBuild(assistant.Spec["mcp_server"]))
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("project assistant %s MCP approval policy: %w", assistant.Address, err)
	}
	overlay, err := eve.MaterializeOverlay(eve.OverlayRequest{
		SourceRoot: sourceRoot, OverlayRoot: overlayRoot, AssistantAddress: assistant.Address,
		RuntimeRevision: assistantRuntimeRevisionForBuild(result), CapabilityRevision: result.Contract.Manifest.ContractRevision,
		ApprovalNeverTools: eve.ApprovalNeverTools(manifest),
		ControlURL:         "http://127.0.0.1:1", MCPURL: "http://127.0.0.1:1",
	})
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("materialize assistant %s overlay: %w", assistant.Address, err)
	}
	// Resolve the executable from the selected target store rather than PATH.
	managedHome, err := nodeHomeForArchive(result.AppRoot, platform)
	if err != nil {
		return generate.AssistantAssetInput{}, err
	}
	cacheRoot := filepath.Join(result.AppRoot, ".scenery", "assistant-cache", platform.DirName(), key)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return generate.AssistantAssetInput{}, err
	}
	if err := runManagedNPM(ctx, managedHome, overlay.Root, cacheRoot, "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--prefer-offline"); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("install assistant %s dependencies from exact lock: %w", assistant.Address, err)
	}
	if err := ensureFileUnchanged(packagePath, packageBefore); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s package changed during build: %w", assistant.Address, err)
	}
	if err := ensureFileUnchanged(lockPath, lockBefore); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s package lock changed during build: %w", assistant.Address, err)
	}
	eveCLI := filepath.Join(managedHome, "lib", "node_modules", "npm", "node_modules", "eve", "bin", "eve.js")
	// Eve is an application dependency, not part of npm's own installation.
	eveCLI = filepath.Join(overlay.Root, "node_modules", "eve", "bin", "eve.js")
	if _, err := os.Stat(eveCLI); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("assistant %s exact lock did not install Eve CLI: %w", assistant.Address, err)
	}
	if err := runManagedNodeWithEnv(ctx, managedHome, overlay.Root, []string{eveCLI, "build", "--skip-sandbox-prewarm"}, []string{"SCENERY_MCP_URL=http://127.0.0.1:1"}); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("compile assistant %s capsule: %w", assistant.Address, err)
	}
	capsuleRoot := filepath.Join(assetRoot, "capsules", safeAssetKey(assistant.Address)+"-"+key)
	if _, err := os.Lstat(capsuleRoot); err == nil {
		if err := removeGeneratedAssistantTree(assetRoot, capsuleRoot); err != nil {
			return generate.AssistantAssetInput{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return generate.AssistantAssetInput{}, err
	}
	if err := copyDeterministicCapsule(overlay.Root, capsuleRoot); err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("stage assistant %s capsule: %w", assistant.Address, err)
	}
	capsuleArchive, err := runtimeassets.BuildArchive(capsuleRoot)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("archive assistant %s capsule: %w", assistant.Address, err)
	}
	nodeDescriptorJSON, err := json.Marshal(nodeArchive.Descriptor)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("marshal managed Node descriptor for assistant %s: %w", assistant.Address, err)
	}
	capsuleDescriptorJSON, err := json.Marshal(capsuleArchive.Descriptor)
	if err != nil {
		return generate.AssistantAssetInput{}, fmt.Errorf("marshal assistant %s capsule descriptor: %w", assistant.Address, err)
	}
	runtimeRevision := assistantRuntimeRevisionForBuild(result)
	descriptor := generate.AssistantAssetDescriptor{
		Kind: generate.AssistantAssetDescriptorKind, SchemaRevision: generate.AssistantAssetSchemaRevision,
		AssistantAddress: assistant.Address, Target: platform.String(), RuntimeRevision: runtimeRevision,
		CapabilityRevision: result.Contract.Manifest.ContractRevision,
		NodeArchiveDigest:  nodeArchive.ArchiveDigest, NodeTreeDigest: nodeArchive.Descriptor.Digest,
		CapsuleArchiveDigest: capsuleArchive.ArchiveDigest, CapsuleTreeDigest: capsuleArchive.Descriptor.Digest,
		CapsuleEntry: generate.AssistantAssetCapsuleEntry, PackageLockDigest: lockDigest,
	}
	return generate.AssistantAssetInput{Descriptor: descriptor, NodeArchive: nodeArchive.Data, NodeDescriptorJSON: nodeDescriptorJSON, CapsuleArchive: capsuleArchive.Data, CapsuleDescriptorJSON: capsuleDescriptorJSON}, nil
}

func nodeHomeForArchive(appRoot string, platform toolchain.Platform) (string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", err
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(appRoot), manifest)
	if err != nil {
		return "", err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	status, err := store.Path(context.Background(), "node", platform)
	if err != nil || status.Status != "installed" || status.HomePath == "" {
		if err == nil {
			err = fmt.Errorf("status %s", status.Status)
		}
		return "", fmt.Errorf("managed Node home for %s unavailable: %w", platform.String(), err)
	}
	return status.HomePath, nil
}

func runManagedNPM(ctx context.Context, home, dir, cache string, args ...string) error {
	npmCLI := filepath.Join(home, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	return runManagedNodeWithEnv(ctx, home, dir, append([]string{npmCLI}, args...), []string{"npm_config_cache=" + cache})
}

func runManagedNode(ctx context.Context, home, dir, script string, args ...string) error {
	return runManagedNodeWithEnv(ctx, home, dir, append([]string{script}, args...), nil)
}

func managedNodeExecutable(home string) string {
	return filepath.Join(filepath.Clean(home), "bin", "node")
}

func runManagedNodeWithEnv(ctx context.Context, home, dir string, args, extra []string) error {
	node := managedNodeExecutable(home)
	command := exec.CommandContext(ctx, node, args...)
	command.Dir = dir
	processEnvironment := envpolicy.Environ()
	environment := make([]string, 0, len(processEnvironment)+len(extra)+2)
	for _, item := range processEnvironment {
		name, _, _ := strings.Cut(item, "=")
		lower := strings.ToLower(name)
		if lower == "path" || lower == "node_path" || lower == "node_options" || strings.HasPrefix(lower, "npm_config_") || strings.HasPrefix(lower, "corepack_") {
			continue
		}
		environment = append(environment, item)
	}
	environment = append(environment, "PATH="+filepath.Join(home, "bin"), "COREPACK_ENABLE_PROJECT_SPEC=0")
	environment = append(environment, extra...)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("managed Node %s failed: %w\n%s", filepath.Base(scriptOrNode(args, node)), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func scriptOrNode(args []string, fallback string) string {
	if len(args) > 0 {
		return filepath.Base(args[0])
	}
	return filepath.Base(fallback)
}

func copyDeterministicCapsule(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("capsule destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(destination)
		}
	}()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relativeSlash := filepath.ToSlash(relative)
		if shouldSkipCapsulePath(relativeSlash) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(link)))
			if resolved != filepath.Clean(destination) && !strings.HasPrefix(resolved, filepath.Clean(destination)+string(filepath.Separator)) {
				return fmt.Errorf("capsule symlink escapes root: %s", relativeSlash)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("capsule contains unsupported path %s", relativeSlash)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Eve's generated server module contains absolute overlay paths and a
		// random build identifier; Nitro's metadata contains a wall-clock date.
		// Restrict canonicalization to these known textual outputs so arbitrary
		// native modules and other binary dependencies are copied byte-for-byte.
		if relativeSlash == ".output/server/index.mjs" {
			data = bytes.ReplaceAll(data, []byte(filepath.Clean(source)), []byte("/scenery-assistant"))
			data = eveBuildPathPattern.ReplaceAll(data, []byte(".eve/builds/build/"))
		} else if relativeSlash == ".output/nitro.json" {
			data = normalizeNitroMetadata(data)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode != 0o644 && mode != 0o755 {
			mode = 0o644
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return err
	}
	completed = true
	return nil
}

func removeGeneratedAssistantTree(root, target string) error {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if cleanTarget == cleanRoot || !strings.HasPrefix(cleanTarget, cleanRoot+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove assistant asset path outside generated root")
	}
	info, err := os.Lstat(cleanTarget)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("assistant generated path is a symlink: %s", cleanTarget)
	}
	return os.RemoveAll(cleanTarget)
}

func shouldSkipCapsulePath(path string) bool {
	return path == ".eve" || strings.HasPrefix(path, ".eve/") || path == ".output/.eve" || strings.HasPrefix(path, ".output/.eve/") || path == "node_modules/.cache" || strings.HasPrefix(path, "node_modules/.cache/") || path == "node_modules/.nitro" || strings.HasPrefix(path, "node_modules/.nitro/") || path == "node_modules/.package-lock.json"
}

func normalizeNitroMetadata(data []byte) []byte {
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return data
	}
	if _, ok := value["date"]; ok {
		value["date"] = "1970-01-01T00:00:00.000Z"
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return data
	}
	return append(encoded, '\n')
}

func writeGeneratedAssistantAsset(root, relative string, data []byte) error {
	relative = filepath.ToSlash(filepath.Clean(relative))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || strings.HasPrefix(relative, "/") {
		return fmt.Errorf("assistant generated asset path escapes workspace: %s", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("assistant generated asset path is a symlink: %s", relative)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeFileIfChanged(root, relative, data)
}

func appendUniquePaths(paths []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(paths)+len(additions))
	for _, path := range paths {
		seen[filepath.ToSlash(path)] = struct{}{}
	}
	for _, path := range additions {
		path = filepath.ToSlash(path)
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
			seen[path] = struct{}{}
		}
	}
	return paths
}

func appRelativePath(root, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) || filepath.IsAbs(filepath.FromSlash(relative)) {
		return "", fmt.Errorf("path must be workspace-relative")
	}
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	if absolute != base && !strings.HasPrefix(absolute, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes application root")
	}
	return absolute, nil
}

func ensureFileUnchanged(path string, before []byte) error {
	after, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("authored bytes changed")
	}
	return nil
}

func digestBytesForBuild(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func safeAssetKey(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "assistant"
	}
	return b.String()
}

func stringValueForBuild(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func referenceValueForBuild(value any) string {
	if reference, ok := value.(map[string]any); ok {
		value = reference["$ref"]
	}
	return stringValueForBuild(value)
}

func assistantRuntimeRevisionForBuild(result *Result) string {
	if result != nil {
		keys := make([]string, 0, len(result.ImplementationRevisions))
		for key := range result.ImplementationRevisions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value := strings.TrimSpace(result.ImplementationRevisions[key]); value != "" {
				return value
			}
		}
	}
	return "runtime-1"
}
