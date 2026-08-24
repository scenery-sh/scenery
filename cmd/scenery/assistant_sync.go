package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

const (
	assistantSyncKind                        = "scenery.assistant.sync"
	assistantDependencyCacheKind             = "scenery.assistant.dependency-cache"
	assistantDependencyCacheSchemaDescriptor = `{"kind":"scenery.assistant.dependency-cache","schema_revision":"digest","lock_digest":"digest","package_digest":"digest"}`
)

var assistantDependencyCacheRevision = string(spec.SchemaRevision(assistantDependencyCacheSchemaDescriptor))

type assistantSyncResponse struct {
	cliPayloadIdentity
	Assistant     string `json:"assistant"`
	Address       string `json:"address"`
	Source        string `json:"source"`
	Package       string `json:"package"`
	PackageLock   string `json:"package_lock"`
	LockDigest    string `json:"lock_digest"`
	PackageDigest string `json:"package_digest"`
	CachePath     string `json:"cache_path"`
	Status        string `json:"status"`
	Reused        bool   `json:"reused"`
	NodePath      string `json:"node_path"`
	NPMPath       string `json:"npm_path"`
}

type assistantDependencyCacheMetadata struct {
	Kind           string `json:"kind"`
	SchemaRevision string `json:"schema_revision"`
	LockDigest     string `json:"lock_digest"`
	PackageDigest  string `json:"package_digest"`
}

type assistantSyncDependencies struct {
	resolveManagedNode func(context.Context, string) (string, string, string, error)
	install            func(context.Context, string, string, string) error
}

func runAssistantSync(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("missing assistant name")
	}
	name := strings.TrimSpace(args[0])
	if !validAssistantName(name) {
		return fmt.Errorf("assistant name %q must be lower_snake_case", name)
	}
	flags := newCLIFlagSet("assistant sync")
	var appRoot string
	var jsonOutput bool
	flags.StringVar(&appRoot, "app-root", "", "")
	registerJSONOutput(flags, &jsonOutput)
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if len(positionals) != 0 {
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	if !jsonOutput {
		return errors.New("scenery assistant sync currently requires -o json")
	}
	root, _, compiled, err := loadAssistantApp(appRoot)
	if err != nil {
		return err
	}
	response, err := syncAssistant(context.Background(), root, compiled, name)
	if err != nil {
		return err
	}
	return writeCLIJSON(stdout, response)
}

func syncAssistant(ctx context.Context, root string, compiled *compiler.Result, name string) (assistantSyncResponse, error) {
	return syncAssistantWithDependencies(ctx, root, compiled, name, assistantSyncDependencies{
		resolveManagedNode: resolveAssistantManagedNode,
		install:            installAssistantSyncDependencies,
	})
}

func syncAssistantWithDependencies(ctx context.Context, root string, compiled *compiler.Result, name string, deps assistantSyncDependencies) (assistantSyncResponse, error) {
	if compiled == nil || compiled.Manifest == nil || !compiled.Valid() {
		return assistantSyncResponse{}, errors.New("assistant app contract is invalid")
	}
	resource, ok := assistantResourceByName(compiled.Manifest, name)
	if !ok || resource.Kind != "scenery.assistant" {
		return assistantSyncResponse{}, fmt.Errorf("assistant %q not found", name)
	}
	implementation, ok := resource.Spec["implementation"].(map[string]any)
	if !ok {
		return assistantSyncResponse{}, fmt.Errorf("assistant %q has no implementation", name)
	}
	adapter := strings.TrimSpace(assistantStringValue(implementation["adapter"]))
	if adapter != "eve" {
		return assistantSyncResponse{}, fmt.Errorf("assistant %q uses unsupported adapter %q; sync supports only the managed assistant adapter", name, adapter)
	}
	source := strings.TrimSpace(assistantStringValue(implementation["source"]))
	packageRelative := strings.TrimSpace(assistantStringValue(implementation["package"]))
	lockRelative := strings.TrimSpace(assistantStringValue(implementation["package_lock"]))
	if !assistantWorkspaceRelative(source) || !assistantWorkspaceRelative(packageRelative) || !assistantWorkspaceRelative(lockRelative) {
		return assistantSyncResponse{}, errors.New("assistant implementation paths must be workspace-relative")
	}
	packagePath, err := assistantRegularPath(root, packageRelative)
	if err != nil {
		return assistantSyncResponse{}, fmt.Errorf("assistant package: %w", err)
	}
	lockPath, err := assistantRegularPath(root, lockRelative)
	if err != nil {
		return assistantSyncResponse{}, fmt.Errorf("assistant package lock: %w", err)
	}
	packageBytes, err := os.ReadFile(packagePath)
	if err != nil {
		return assistantSyncResponse{}, err
	}
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		return assistantSyncResponse{}, err
	}
	if err := validateAssistantPackageLock(packageBytes, lockBytes); err != nil {
		return assistantSyncResponse{}, fmt.Errorf("assistant dependency lock drift: %w", err)
	}
	lockDigest := digestBytes(lockBytes)
	packageDigest := digestBytes(packageBytes)
	nodePath, npmPath, nodeHome, err := deps.resolveManagedNode(ctx, root)
	if err != nil {
		return assistantSyncResponse{}, fmt.Errorf("assistant managed Node/npm: %w", err)
	}
	cachePath, status, reused, err := syncAssistantDependencyCache(ctx, root, lockDigest, packageDigest, packageBytes, lockBytes, npmPath, nodeHome, deps.install)
	if err != nil {
		return assistantSyncResponse{}, err
	}
	return assistantSyncResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(assistantSyncKind), Assistant: resource.Name, Address: resource.Address,
		Source: source, Package: packageRelative, PackageLock: lockRelative, LockDigest: lockDigest, PackageDigest: packageDigest,
		CachePath: cachePath, Status: status, Reused: reused, NodePath: nodePath, NPMPath: npmPath,
	}, nil
}

func assistantWorkspaceRelative(value string) bool {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" || filepath.IsAbs(value) || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	return !strings.HasPrefix(value, ".scenery/") && !strings.HasPrefix(value, ".git/")
}

func assistantRegularPath(root, relative string) (string, error) {
	if !assistantWorkspaceRelative(relative) {
		return "", errors.New("path is not workspace-relative")
	}
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	if err := scn.RejectPathSymlinks(root, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("path must be a regular non-symlink file")
	}
	return path, nil
}

func validateAssistantPackageLock(packageBytes, lockBytes []byte) error {
	if err := rejectDuplicateJSONObjects(packageBytes); err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	if err := rejectDuplicateJSONObjects(lockBytes); err != nil {
		return fmt.Errorf("package-lock.json: %w", err)
	}
	var packageDoc map[string]any
	if err := decodeJSONExact(packageBytes, &packageDoc); err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	var lockDoc map[string]any
	if err := decodeJSONExact(lockBytes, &lockDoc); err != nil {
		return fmt.Errorf("package-lock.json: %w", err)
	}
	packages, ok := lockDoc["packages"].(map[string]any)
	if !ok {
		return errors.New("package-lock.json has no packages object")
	}
	rootLock, ok := packages[""].(map[string]any)
	if !ok {
		return errors.New("package-lock.json has no root package")
	}
	for _, key := range []string{"name", "version", "dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		left, leftOK := packageDoc[key]
		right, rightOK := rootLock[key]
		if leftOK != rightOK || leftOK && !reflect.DeepEqual(left, right) {
			return fmt.Errorf("root %s differs from package.json", key)
		}
	}
	dependencies, ok := packageDoc["dependencies"].(map[string]any)
	if !ok || strings.TrimSpace(assistantStringValue(dependencies["eve"])) != eve.EveVersion {
		return fmt.Errorf("package.json must pin eve to %s", eve.EveVersion)
	}
	evePackage, ok := packages["node_modules/eve"].(map[string]any)
	if !ok {
		return errors.New("package-lock.json has no node_modules/eve entry")
	}
	if got := strings.TrimSpace(assistantStringValue(evePackage["version"])); got != eve.EveVersion {
		return fmt.Errorf("locked eve version is %q, want %s", got, eve.EveVersion)
	}
	return nil
}

func decodeJSONExact(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func syncAssistantDependencyCache(ctx context.Context, root, lockDigest, packageDigest string, packageBytes, lockBytes []byte, npmPath, nodeHome string, install func(context.Context, string, string, string) error) (string, string, bool, error) {
	if len(lockDigest) != len("sha256:")+64 || !strings.HasPrefix(lockDigest, "sha256:") {
		return "", "", false, errors.New("assistant lock digest is invalid")
	}
	digest := strings.TrimPrefix(lockDigest, "sha256:")
	cacheRoot := filepath.Join(root, ".scenery", "assistant-cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", "", false, err
	}
	if err := rejectExistingAssistantPathSymlinks(root, cacheRoot); err != nil {
		return "", "", false, err
	}
	cachePath := filepath.Join(cacheRoot, digest)
	if reused, err := validateAssistantDependencyCache(cachePath, lockDigest, packageDigest); err != nil {
		return "", "", false, err
	} else if reused {
		return cachePath, "reused", true, nil
	}
	stage, err := os.MkdirTemp(cacheRoot, ".stage-")
	if err != nil {
		return "", "", false, err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "package.json"), packageBytes, 0o644); err != nil {
		return "", "", false, err
	}
	if err := os.WriteFile(filepath.Join(stage, "package-lock.json"), lockBytes, 0o644); err != nil {
		return "", "", false, err
	}
	if err := install(ctx, stage, npmPath, nodeHome); err != nil {
		return "", "", false, err
	}
	metadata := assistantDependencyCacheMetadata{Kind: assistantDependencyCacheKind, SchemaRevision: assistantDependencyCacheRevision, LockDigest: lockDigest, PackageDigest: packageDigest}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", "", false, err
	}
	if err := atomicfile.Write(filepath.Join(stage, "metadata.json"), append(encoded, '\n'), 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return "", "", false, err
	}
	if _, err := os.Lstat(cachePath); err == nil {
		if reused, validationErr := validateAssistantDependencyCache(cachePath, lockDigest, packageDigest); validationErr == nil && reused {
			return cachePath, "reused", true, nil
		}
		return "", "", false, fmt.Errorf("assistant dependency cache appeared during sync: %s", cachePath)
	} else if !os.IsNotExist(err) {
		return "", "", false, err
	}
	if err := os.Rename(stage, cachePath); err != nil {
		return "", "", false, fmt.Errorf("publish assistant dependency cache: %w", err)
	}
	return cachePath, "synced", false, nil
}

func validateAssistantDependencyCache(path, lockDigest, packageDigest string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("assistant dependency cache %q is not a directory", path)
	}
	metadataPath := filepath.Join(path, "metadata.json")
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return false, fmt.Errorf("assistant dependency cache metadata: %w", err)
	}
	if metadataInfo.Mode()&os.ModeSymlink != 0 || !metadataInfo.Mode().IsRegular() || metadataInfo.Mode().Perm()&0o077 != 0 {
		return false, errors.New("assistant dependency cache metadata must be owner-only regular file")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return false, err
	}
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return false, err
	}
	var metadata assistantDependencyCacheMetadata
	if err := decodeJSONExact(data, &metadata); err != nil {
		return false, err
	}
	if metadata.Kind != assistantDependencyCacheKind || metadata.SchemaRevision != assistantDependencyCacheRevision || metadata.LockDigest != lockDigest || metadata.PackageDigest != packageDigest {
		return false, fmt.Errorf("assistant dependency cache metadata digest mismatch")
	}
	nodeModules := filepath.Join(path, "node_modules")
	nodeInfo, err := os.Lstat(nodeModules)
	if err != nil {
		return false, fmt.Errorf("assistant dependency cache node_modules: %w", err)
	}
	if nodeInfo.Mode()&os.ModeSymlink != 0 || !nodeInfo.IsDir() {
		return false, errors.New("assistant dependency cache node_modules is not a directory")
	}
	return true, nil
}

func installAssistantSyncDependencies(ctx context.Context, stage, npm, home string) error {
	if strings.TrimSpace(npm) == "" || strings.TrimSpace(home) == "" {
		return errors.New("managed npm path is required")
	}
	command := execCommandContext(ctx, npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--cache", filepath.Join(stage, ".npm-cache"))
	command.Dir = stage
	command.Env = []string{"PATH=" + filepath.Join(home, "bin"), "HOME=" + filepath.Join(stage, ".home"), "NPM_CONFIG_UPDATE_NOTIFIER=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_AUDIT=false"}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("assistant dependency sync: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
