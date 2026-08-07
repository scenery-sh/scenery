package main

// Assistant doctor checks are deliberately read-only.  They inspect the
// authored graph, the managed Node store, and provider-neutral supervisor
// artifacts; they never run npm/node, download a toolchain, or print helper
// implementation details.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/toolchain"
)

const (
	doctorAssistantCategory = "assistant"
	doctorAssistantSeverity = doctor.SeverityRequired

	doctorAssistantNodeID     = "assistant.node"
	doctorAssistantPlatformID = "assistant.platform"
	doctorAssistantSourceID   = "assistant.source"
	doctorAssistantPackageID  = "assistant.package_lock"
	doctorAssistantReservedID = "assistant.reserved_paths"
	doctorAssistantTokenID    = "assistant.production_token"
	doctorAssistantAssetID    = "assistant.assets"
	doctorAssistantRevisionID = "assistant.revisions"
	doctorAssistantStatusID   = "assistant.status"

	assistantTokenKeyEnv     = "SCENERY_ASSISTANT_TOKEN_KEY"
	assistantTokenKeyFileEnv = "SCENERY_ASSISTANT_TOKEN_KEY_FILE"
)

var assistantGeneratedReservedPaths = [...]string{
	"agent/channels/scenery.ts",
	"agent/connections/scenery.ts",
	".scenery/bootstrap.mjs",
	".scenery/runtime-manifest.json",
}

// doctorAssistantChecks inspects every declared assistant in one compiled
// snapshot.  A source compile failure may still carry a PartialGraph; using it
// lets doctor report path/package issues alongside the ordinary compiler
// diagnostic instead of silently dropping the assistant checks.
func doctorAssistantChecks(ctx context.Context, root string, cfg appcfg.Config, runtimeInfo doctor.RuntimeInfo) []doctor.Check {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil
	}
	result, err := compiler.Compile(root)
	if err != nil || result == nil {
		return nil
	}
	resources := []compiler.Resource(nil)
	if result.Manifest != nil {
		resources = result.Manifest.Resources
	} else if result.PartialGraph != nil {
		resources = result.PartialGraph.Resources
	}
	assistants := make([]compiler.Resource, 0)
	for _, resource := range resources {
		if resource.Kind == "scenery.assistant" {
			assistants = append(assistants, resource)
		}
	}
	if len(assistants) == 0 {
		return nil
	}
	sort.Slice(assistants, func(i, j int) bool { return assistants[i].Address < assistants[j].Address })
	statuses, statusErr := doctorAssistantStatuses(root)
	checks := make([]doctor.Check, 0, len(assistants)*7+1)
	if statusErr != nil {
		checks = append(checks, checkError(doctorAssistantStatusID, "Assistant supervisor status", "assistant runtime status could not be read", "Restart `scenery up` to republish assistant status."))
	}
	for _, assistant := range assistants {
		name := assistantDoctorName(assistant)
		implementation, _ := assistant.Spec["implementation"].(map[string]any)
		source := assistantStringValue(implementation["source"])
		packagePath := assistantStringValue(implementation["package"])
		lockPath := assistantStringValue(implementation["package_lock"])
		sourceRoot, sourceCheck, sourceOK := doctorAssistantSourceCheck(root, name, source)
		checks = append(checks, sourceCheck)
		checks = append(checks, doctorAssistantReservedCheck(name, sourceRoot, sourceOK))
		checks = append(checks, doctorAssistantPackageCheck(root, name, sourceRoot, sourceOK, packagePath, lockPath, statuses[assistant.Address].PackageLockDigest))
		checks = append(checks, doctorAssistantPlatformCheck(name, runtimeInfo))
		checks = append(checks, doctorAssistantNodeCheck(ctx, root, name, runtimeInfo))
		checks = append(checks, doctorAssistantProductionTokenCheck(root, cfg, name))
		checks = append(checks, doctorAssistantAssetCheck(root, assistant, result))
		checks = append(checks, doctorAssistantRevisionCheck(name, assistant, result, statuses[assistant.Address], statusErr))
	}
	return checks
}

func assistantDoctorName(resource compiler.Resource) string {
	name := strings.TrimSpace(resource.Name)
	if name != "" {
		return name
	}
	address := strings.TrimSuffix(strings.TrimSpace(resource.Address), "/")
	if index := strings.LastIndexByte(address, '/'); index >= 0 {
		address = address[index+1:]
	}
	if address == "" {
		return "unknown"
	}
	return address
}

func assistantCheckID(prefix, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	// Resource names are compiler-validated identifiers.  Keep a defensive
	// stable suffix for partial graphs without copying arbitrary source text.
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return prefix + "." + b.String()
}

func checkOK(id, name, message string, observed map[string]any) doctor.Check {
	return doctor.Check{ID: id, Category: doctorAssistantCategory, Name: name, Status: doctor.StatusOK, Severity: doctorAssistantSeverity, Message: message, Observed: observed}
}

func checkError(id, name, message, action string) doctor.Check {
	return doctor.Check{ID: id, Category: doctorAssistantCategory, Name: name, Status: doctor.StatusError, Severity: doctorAssistantSeverity, Message: message, SuggestedAction: action}
}

func checkSkipped(id, name, message string) doctor.Check {
	return doctor.Check{ID: id, Category: doctorAssistantCategory, Name: name, Status: doctor.StatusSkipped, Severity: doctor.SeverityInformational, Message: message}
}

func doctorAssistantSourceCheck(root, name, raw string) (string, doctor.Check, bool) {
	id := assistantCheckID(doctorAssistantSourceID, name)
	checkName := "Assistant source (" + name + ")"
	raw = strings.TrimSpace(raw)
	if raw == "" || filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" || strings.ContainsAny(raw, "\\\x00") {
		return "", checkError(id, checkName, "assistant source must be a workspace-relative directory", "Declare an assistant source directory beneath the app workspace."), false
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(raw)))
	if !pathWithinDoctor(root, candidate) {
		return "", checkError(id, checkName, "assistant source escapes the app workspace", "Move the assistant source beneath the app workspace."), false
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return candidate, checkError(id, checkName, "assistant source directory is unavailable", "Create the declared assistant source directory."), false
		}
		return candidate, checkError(id, checkName, "assistant source directory could not be inspected", "Check permissions for the declared assistant source directory."), false
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return candidate, checkError(id, checkName, "assistant source must be a real directory inside the app workspace", "Replace the source path with a non-symlink directory beneath the app workspace."), false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !pathWithinDoctor(root, resolved) {
		return candidate, checkError(id, checkName, "assistant source path is not contained by the app workspace", "Remove symlink traversal from the assistant source path."), false
	}
	return candidate, checkOK(id, checkName, "assistant source directory is contained by the app workspace", map[string]any{"path": filepath.ToSlash(filepath.Join(filepath.Clean(root), filepath.FromSlash(raw)))}), true
}

func pathWithinDoctor(root, candidate string) bool {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return false
	}
	// macOS commonly exposes temporary directories through a /var symlink.
	// Resolve both sides before comparing so an in-root path is not reported
	// as escaping merely because only the candidate was evaluated.
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = filepath.Clean(resolved)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(candidate); resolveErr == nil {
		candidate = filepath.Clean(resolved)
	}
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func doctorAssistantReservedCheck(name, sourceRoot string, sourceOK bool) doctor.Check {
	id := assistantCheckID(doctorAssistantReservedID, name)
	checkName := "Assistant generated paths (" + name + ")"
	if !sourceOK {
		return checkSkipped(id, checkName, "reserved generated paths are not inspectable until the assistant source is valid")
	}
	for _, relative := range assistantGeneratedReservedPaths {
		if _, err := os.Lstat(filepath.Join(sourceRoot, filepath.FromSlash(relative))); err == nil {
			return checkError(id, checkName, "assistant source claims a reserved generated path", "Remove the authored file at the reserved generated path and rerun doctor.")
		} else if !errors.Is(err, os.ErrNotExist) {
			return checkError(id, checkName, "assistant generated paths could not be inspected", "Check permissions for the assistant source directory.")
		}
	}
	return checkOK(id, checkName, "assistant source does not claim reserved generated paths", nil)
}

func doctorAssistantPackageCheck(root, name, sourceRoot string, sourceOK bool, packagePath, lockPath, expectedDigest string) doctor.Check {
	id := assistantCheckID(doctorAssistantPackageID, name)
	checkName := "Assistant package lock (" + name + ")"
	if !sourceOK {
		return checkSkipped(id, checkName, "package and lock files are not inspectable until the assistant source is valid")
	}
	packagePath = strings.TrimSpace(packagePath)
	lockPath = strings.TrimSpace(lockPath)
	if packagePath == "" || lockPath == "" || filepath.IsAbs(packagePath) || filepath.IsAbs(lockPath) || filepath.VolumeName(packagePath) != "" || filepath.VolumeName(lockPath) != "" || strings.ContainsAny(packagePath+lockPath, "\\\x00") || filepath.Base(filepath.FromSlash(packagePath)) != "package.json" || filepath.Base(filepath.FromSlash(lockPath)) != "package-lock.json" {
		return checkError(id, checkName, "assistant requires an exact package.json and package-lock.json", "Declare package.json and package-lock.json beneath the assistant source directory.")
	}
	packageFile, packageOK := doctorAssistantContainedFile(root, sourceRoot, packagePath)
	lockFile, lockOK := doctorAssistantContainedFile(root, sourceRoot, lockPath)
	if !packageOK || !lockOK {
		return checkError(id, checkName, "assistant package.json or package-lock.json is unavailable", "Create both exact package files beneath the assistant source directory.")
	}
	packageData, err := readJSONObject(packageFile)
	if err != nil {
		return checkError(id, checkName, "assistant package.json is not valid JSON", "Repair package.json and rerun doctor.")
	}
	lockData, err := readJSONObject(lockFile)
	if err != nil {
		return checkError(id, checkName, "assistant package-lock.json is not valid JSON", "Regenerate package-lock.json with the managed npm lane.")
	}
	if err := exactAssistantPackageLock(packageData, lockData); err != nil {
		return checkError(id, checkName, "assistant package.json and package-lock.json drifted", "Regenerate package-lock.json with the managed npm lane.")
	}
	if expectedDigest != "" {
		digest, err := digestDoctorFile(lockFile)
		if err != nil || digest != expectedDigest {
			return checkError(id, checkName, "assistant package-lock.json digest does not match persisted status", "Rebuild or restart the assistant so its dependency identity is refreshed.")
		}
	}
	return checkOK(id, checkName, "assistant package.json and package-lock.json are exact", map[string]any{"package": filepath.ToSlash(packagePath), "package_lock": filepath.ToSlash(lockPath)})
}

func doctorAssistantContainedFile(root, sourceRoot, raw string) (string, bool) {
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(raw)))
	if !pathWithinDoctor(root, candidate) || !pathWithinDoctor(sourceRoot, candidate) {
		return "", false
	}
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !pathWithinDoctor(root, resolved) || !pathWithinDoctor(sourceRoot, resolved) {
		return "", false
	}
	return candidate, true
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("object required")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}

func exactAssistantPackageLock(packageData, lockData map[string]any) error {
	packages, ok := lockData["packages"].(map[string]any)
	if !ok {
		return errors.New("lock packages are missing")
	}
	root, ok := packages[""].(map[string]any)
	if !ok {
		return errors.New("lock root package is missing")
	}
	if version, ok := lockData["lockfileVersion"].(float64); !ok || version < 1 {
		return errors.New("lockfile version is missing")
	}
	for _, key := range []string{"name", "version"} {
		if value, exists := packageData[key]; exists {
			if !reflect.DeepEqual(value, lockData[key]) {
				return fmt.Errorf("lock metadata %s differs", key)
			}
			if !reflect.DeepEqual(value, root[key]) {
				return fmt.Errorf("lock root %s differs", key)
			}
		}
	}
	for _, key := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		if value, exists := packageData[key]; exists && !reflect.DeepEqual(value, root[key]) {
			return fmt.Errorf("lock root %s differs", key)
		}
	}
	return nil
}

func doctorAssistantPlatformCheck(name string, runtimeInfo doctor.RuntimeInfo) doctor.Check {
	id := assistantCheckID(doctorAssistantPlatformID, name)
	checkName := "Assistant platform (" + name + ")"
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return checkError(id, checkName, "managed Node platform manifest is unavailable", "Repair the bundled Scenery toolchain manifest.")
	}
	artifact, ok := manifest.Artifact("node")
	if !ok {
		return checkError(id, checkName, "managed Node artifact is not declared", "Use a Scenery build that includes the managed Node artifact.")
	}
	platform := toolchain.Platform{GOOS: runtimeInfo.GOOS, GOARCH: runtimeInfo.GOARCH}
	if _, ok := artifact.PlatformArtifact(platform); !ok {
		return checkError(id, checkName, "managed Node is unsupported on this platform", "Run Scenery on a platform with a pinned managed Node artifact.")
	}
	return checkOK(id, checkName, "managed Node has a pinned artifact for this platform", map[string]any{"platform": platform.String()})
}

func doctorAssistantNodeCheck(ctx context.Context, root, name string, runtimeInfo doctor.RuntimeInfo) doctor.Check {
	id := assistantCheckID(doctorAssistantNodeID, name)
	checkName := "Assistant managed Node (" + name + ")"
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return checkError(id, checkName, "managed Node manifest is unavailable", "Repair the bundled Scenery toolchain manifest.")
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(root), manifest)
	if err != nil {
		return checkError(id, checkName, "managed Node store is invalid", "Repair the app's managed toolchain state.")
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	platform := toolchain.Platform{GOOS: runtimeInfo.GOOS, GOARCH: runtimeInfo.GOARCH}
	status, err := store.Verify(ctx, toolchain.Options{RootDir: root, Platform: platform, Tool: "node", Strict: true})
	if err != nil {
		return checkError(id, checkName, "managed Node could not be verified", "Run `scenery system toolchain sync --tool node -o json` to repair managed Node.")
	}
	for _, artifact := range status.Artifacts {
		if artifact.Name != "node" {
			continue
		}
		switch artifact.Status {
		case "installed":
			return checkOK(id, checkName, "checksummed managed Node is installed", map[string]any{"version": artifact.Version})
		case "unsupported":
			return checkSkipped(id, checkName, "managed Node check is not applicable on this platform")
		case "invalid":
			return checkError(id, checkName, "managed Node failed integrity verification", "Run `scenery system toolchain sync --tool node -o json` to repair managed Node.")
		default:
			return checkError(id, checkName, "managed Node is not installed", "Run `scenery system toolchain sync --tool node -o json` to install managed Node.")
		}
	}
	return checkError(id, checkName, "managed Node artifact is unavailable", "Use a Scenery build that includes managed Node.")
}

func doctorAssistantProductionTokenCheck(root string, cfg appcfg.Config, name string) doctor.Check {
	id := assistantCheckID(doctorAssistantTokenID, name)
	checkName := "Assistant production token key (" + name + ")"
	if _, ok := cfg.Envs["production"]; !ok {
		return checkSkipped(id, checkName, "production token key check is not applicable because no production environment is declared")
	}
	if validAssistantTokenKeyValue(envpolicy.Get(assistantTokenKeyEnv)) {
		return checkOK(id, checkName, "production assistant token key is available", nil)
	}
	keyFile := strings.TrimSpace(envpolicy.Get(assistantTokenKeyFileEnv))
	if keyFile != "" && validAssistantTokenKeyFile(keyFile) {
		return checkOK(id, checkName, "production assistant token key is available", nil)
	}
	// The development supervisor persists its framework-owned key beneath the
	// app state root. It is also a valid inspectable key when production is
	// being rehearsed locally, without exposing its value in diagnostics.
	if validAssistantTokenKeyFile(filepath.Join(root, ".scenery", "assistants", "token-key")) {
		return checkOK(id, checkName, "production assistant token key is available", nil)
	}
	// A declared production environment is itself an inspectable production
	// context.  Missing secret files must therefore be reported instead of
	// being silently downgraded to a skip.
	values, err := envfile.MergeFiles(root, ".env", ".env.production", ".env.local", ".env.production.local")
	if err == nil {
		if validAssistantTokenKeyValue(values[assistantTokenKeyEnv]) || validAssistantTokenKeyFile(values[assistantTokenKeyFileEnv]) {
			return checkOK(id, checkName, "production assistant token key is available", nil)
		}
	}
	return checkError(id, checkName, "production assistant token key is missing", "Provide the framework-owned assistant token key through the production secret mechanism.")
}

func validAssistantTokenKeyValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if len([]byte(value)) == 32 {
		return true
	}
	if key, err := hex.DecodeString(value); err == nil && len(key) == 32 {
		return true
	}
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if key, err := encoding.DecodeString(value); err == nil && len(key) == 32 {
			return true
		}
	}
	return false
}

func validAssistantTokenKeyFile(path string) bool {
	info, err := os.Lstat(strings.TrimSpace(path))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && len(data) <= 4096 && validAssistantTokenKeyValue(string(data))
}

func doctorAssistantRevisionCheck(name string, assistant compiler.Resource, result *compiler.Result, status doctorAssistantStatus, statusErr error) doctor.Check {
	id := assistantCheckID(doctorAssistantRevisionID, name)
	checkName := "Assistant runtime revisions (" + name + ")"
	if statusErr != nil || !status.Present {
		return checkSkipped(id, checkName, "provider-neutral assistant runtime status is not available yet")
	}
	expectedRuntime := assistantExpectedRuntimeRevision(result)
	expectedCapability := ""
	if result != nil && result.Manifest != nil {
		expectedCapability = strings.TrimSpace(result.Manifest.ContractRevision)
	}
	if status.ExpectedRuntime != "" && expectedRuntime != "" && status.ExpectedRuntime != expectedRuntime {
		return checkError(id, checkName, "assistant runtime revision is stale", "Restart the assistant helper from the current compiled application.")
	}
	if status.ExpectedCapability != "" && expectedCapability != "" && status.ExpectedCapability != expectedCapability {
		return checkError(id, checkName, "assistant capability revision is stale", "Restart the assistant helper from the current compiled application.")
	}
	if status.ActualRuntime != "" && expectedRuntime != "" && status.ActualRuntime != expectedRuntime {
		return checkError(id, checkName, "assistant runtime revision does not match the compiled application", "Restart the assistant helper from the current compiled application.")
	}
	if status.ActualCapability != "" && expectedCapability != "" && status.ActualCapability != expectedCapability {
		return checkError(id, checkName, "assistant capability revision does not match the compiled application", "Restart the assistant helper from the current compiled application.")
	}
	if !status.Ready {
		return checkSkipped(id, checkName, "assistant runtime status is present but the helper is not ready")
	}
	return checkOK(id, checkName, "assistant runtime and capability revisions match", map[string]any{"runtime_revision": expectedRuntime, "capability_revision": expectedCapability})
}

type doctorAssistantStatus struct {
	Present            bool
	Ready              bool
	ExpectedRuntime    string
	ExpectedCapability string
	ActualRuntime      string
	ActualCapability   string
	PackageLockDigest  string
}

func doctorAssistantStatuses(root string) (map[string]doctorAssistantStatus, error) {
	path, err := assistantStatusSnapshotPath(root)
	if err != nil {
		return nil, err
	}
	_, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]doctorAssistantStatus{}, nil
	}
	if err != nil {
		return nil, err
	}
	records, err := readAssistantStatusSnapshot(root)
	if err != nil {
		return nil, errors.New("invalid assistant status snapshot")
	}
	result := make(map[string]doctorAssistantStatus, len(records))
	for address, record := range records {
		status := doctorAssistantStatus{Present: true, Ready: record.Ready, ExpectedRuntime: record.ExpectedRuntimeRevision, ExpectedCapability: record.ExpectedCapabilityRevision, ActualRuntime: record.ActualRuntimeRevision, ActualCapability: record.ActualCapabilityRevision}
		if record.Implementation != nil {
			status.PackageLockDigest = strings.TrimSpace(record.Implementation.PackageLockDigest)
		}
		result[address] = status
	}
	return result, nil
}

type doctorAssistantAssetDescriptor struct {
	Kind                 string `json:"kind"`
	SchemaRevision       string `json:"schema_revision"`
	AssistantAddress     string `json:"assistant_address"`
	Target               string `json:"target"`
	RuntimeRevision      string `json:"runtime_revision"`
	CapabilityRevision   string `json:"capability_revision"`
	NodeArchiveDigest    string `json:"node_archive_digest"`
	NodeTreeDigest       string `json:"node_tree_digest"`
	CapsuleArchiveDigest string `json:"capsule_archive_digest"`
	CapsuleTreeDigest    string `json:"capsule_tree_digest"`
	CapsuleEntry         string `json:"capsule_entry"`
	PackageLockDigest    string `json:"package_lock_digest"`
	// DescriptorDigest was used by an early sidecar fixture. Keep accepting it
	// only when explicitly present so a self-digest failure is still surfaced,
	// while the current runtime-assets.v1 descriptor remains exact.
	DescriptorDigest string `json:"descriptor_digest,omitempty"`
}

func doctorAssistantAssetCheck(root string, assistant compiler.Resource, result *compiler.Result) doctor.Check {
	name := assistantDoctorName(assistant)
	id := assistantCheckID(doctorAssistantAssetID, name)
	checkName := "Assistant runtime assets (" + name + ")"
	assetRoot := filepath.Join(root, ".scenery", "build", "assets")
	paths := make([]string, 0)
	walkErr := filepath.WalkDir(assetRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.HasPrefix(entry.Name(), "runtime-descriptor-") || entry.Name() == "runtime-descriptor.json" {
				return errors.New("assistant runtime descriptor is a symlink")
			}
			return nil
		}
		if entry.Name() == "runtime-descriptor.json" || (strings.HasPrefix(entry.Name(), "runtime-descriptor-") && strings.HasSuffix(entry.Name(), ".json")) {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		return checkError(id, checkName, "assistant runtime descriptors could not be inspected", "Check permissions for the generated assistant asset directory.")
	}
	if len(paths) == 0 {
		return checkSkipped(id, checkName, "production assistant runtime assets are not present; asset digest verification is not applicable")
	}
	sort.Strings(paths)
	found := false
	for _, path := range paths {
		descriptor, raw, err := readAssistantAssetDescriptor(path)
		if err != nil {
			return checkError(id, checkName, "assistant runtime descriptor failed validation", "Rebuild the production assistant assets from the current application graph.")
		}
		if descriptor.AssistantAddress != assistant.Address {
			continue
		}
		found = true
		if err := validateAssistantAssetDescriptor(descriptor, raw, path, result); err != nil {
			return checkError(id, checkName, "assistant runtime asset digest verification failed", "Rebuild the production assistant assets from the current application graph.")
		}
		if err := verifyAssistantAssetArchives(root, path, descriptor); err != nil {
			return checkError(id, checkName, "assistant runtime asset digest verification failed", "Rebuild the production assistant assets from the current application graph.")
		}
	}
	if !found {
		return checkSkipped(id, checkName, "no production runtime descriptor exists for this assistant")
	}
	return checkOK(id, checkName, "assistant runtime descriptor and capsule digests are valid", nil)
}

func readAssistantAssetDescriptor(path string) (doctorAssistantAssetDescriptor, map[string]any, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return doctorAssistantAssetDescriptor{}, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return doctorAssistantAssetDescriptor{}, nil, errors.New("descriptor must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return doctorAssistantAssetDescriptor{}, nil, err
	}
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return doctorAssistantAssetDescriptor{}, nil, err
	}
	var descriptor doctorAssistantAssetDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return doctorAssistantAssetDescriptor{}, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return doctorAssistantAssetDescriptor{}, nil, errors.New("trailing JSON")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return doctorAssistantAssetDescriptor{}, nil, err
	}
	return descriptor, raw, nil
}

func validateAssistantAssetDescriptor(descriptor doctorAssistantAssetDescriptor, raw map[string]any, path string, result *compiler.Result) error {
	if descriptor.Kind != runtimeassets.AssistantAssetKind || descriptor.SchemaRevision != runtimeassets.AssistantAssetSchemaRevision || strings.TrimSpace(descriptor.AssistantAddress) == "" || strings.TrimSpace(descriptor.Target) == "" || descriptor.CapsuleEntry != ".scenery/bootstrap.mjs" {
		return errors.New("descriptor identity is invalid")
	}
	for _, digest := range []string{descriptor.NodeArchiveDigest, descriptor.NodeTreeDigest, descriptor.CapsuleArchiveDigest, descriptor.CapsuleTreeDigest, descriptor.PackageLockDigest} {
		if !validDoctorDigest(digest) {
			return errors.New("descriptor digest is invalid")
		}
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, "runtime-descriptor-") && strings.HasSuffix(base, ".json") {
		key := strings.TrimSuffix(strings.TrimPrefix(base, "runtime-descriptor-"), ".json")
		if key != strings.TrimPrefix(descriptor.CapsuleArchiveDigest, "sha256:") {
			return errors.New("descriptor filename does not match capsule digest")
		}
	}
	if result != nil && result.Manifest != nil {
		if descriptor.CapabilityRevision != "" && descriptor.CapabilityRevision != result.Manifest.ContractRevision {
			return errors.New("descriptor capability revision is stale")
		}
		if expected := assistantExpectedRuntimeRevision(result); descriptor.RuntimeRevision != "" && expected != "" && descriptor.RuntimeRevision != expected {
			return errors.New("descriptor runtime revision is stale")
		}
	}
	if descriptor.DescriptorDigest != "" {
		if !validDoctorDigest(descriptor.DescriptorDigest) {
			return errors.New("descriptor digest is invalid")
		}
		copyRaw := make(map[string]any, len(raw))
		for key, value := range raw {
			if key != "descriptor_digest" {
				copyRaw[key] = value
			}
		}
		encoded, err := json.Marshal(copyRaw)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != descriptor.DescriptorDigest {
			return errors.New("descriptor self-digest mismatch")
		}
	}
	return nil
}

// verifyAssistantAssetArchives validates the generated bytes when the latest
// build workspace is still available. Sidecars are retained in the app root,
// so a cleaned workspace is a valid reason to skip byte verification while
// still checking the sidecar's exact provider-neutral descriptor above.
func verifyAssistantAssetArchives(root, descriptorPath string, descriptor doctorAssistantAssetDescriptor) error {
	manifest, present, err := build.ReadLatestBuildManifest(root)
	if err != nil || !present || manifest == nil || strings.TrimSpace(manifest.Build.WorkspaceDir) == "" {
		return nil
	}
	assetRoot := filepath.Join(manifest.Build.WorkspaceDir, "internal", "scenerygen", "assets")
	nodePath := filepath.Join(assetRoot, "archives", "node-"+strings.TrimPrefix(descriptor.NodeArchiveDigest, "sha256:")+".tar.gz")
	capsulePath := filepath.Join(assetRoot, "archives", "capsule-"+strings.TrimPrefix(descriptor.CapsuleArchiveDigest, "sha256:")+".tar.gz")
	descriptorPathWorkspace := filepath.Join(assetRoot, "descriptors", strings.TrimPrefix(descriptor.CapsuleArchiveDigest, "sha256:")+".json")
	_, nodeErr := os.Stat(nodePath)
	_, capsuleErr := os.Stat(capsulePath)
	workspaceDescriptor, _, descriptorErr := readAssistantAssetDescriptor(descriptorPathWorkspace)
	if errors.Is(nodeErr, os.ErrNotExist) && errors.Is(capsuleErr, os.ErrNotExist) && errors.Is(descriptorErr, os.ErrNotExist) {
		return nil
	}
	if nodeErr != nil || capsuleErr != nil || descriptorErr != nil {
		return errors.New("generated assistant asset bytes are unavailable")
	}
	if err := verifyAssistantArchive(nodePath, descriptor.NodeArchiveDigest, descriptor.NodeTreeDigest); err != nil {
		return err
	}
	if err := verifyAssistantArchive(capsulePath, descriptor.CapsuleArchiveDigest, descriptor.CapsuleTreeDigest); err != nil {
		return err
	}
	if !reflect.DeepEqual(workspaceDescriptor, descriptor) {
		return errors.New("generated assistant descriptor differs from sidecar")
	}
	return nil
}

func verifyAssistantArchive(path, archiveDigest, treeDigest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("generated assistant archive must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != archiveDigest {
		return errors.New("generated assistant archive digest mismatch")
	}
	temporary, err := os.MkdirTemp("", "scenery-doctor-assistant-")
	if err != nil {
		return errors.New("generated assistant archive could not be verified")
	}
	defer os.RemoveAll(temporary)
	descriptor, err := runtimeassets.ExtractArchive(data, temporary)
	if err != nil || descriptor.Digest != treeDigest {
		return errors.New("generated assistant tree digest mismatch")
	}
	return nil
}

func validDoctorDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func digestDoctorFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
