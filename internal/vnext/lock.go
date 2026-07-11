package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const LockfileSchema = "scenery.lock/v1"

type Lockfile struct {
	Schema  string      `json:"schema"`
	Entries []LockEntry `json:"entries"`
}

type LockEntry struct {
	Kind                       string   `json:"kind"`
	Name                       string   `json:"name"`
	Source                     string   `json:"source"`
	Version                    string   `json:"version"`
	Integrity                  string   `json:"integrity"`
	CompileDescriptorDigest    string   `json:"compile_descriptor_digest,omitempty"`
	RuntimeABI                 string   `json:"runtime_abi,omitempty"`
	DeploymentABI              string   `json:"deployment_abi,omitempty"`
	MigrationABI               string   `json:"migration_abi,omitempty"`
	PackageContractABIRevision string   `json:"package_contract_abi_revision,omitempty"`
	Dependencies               []string `json:"dependencies,omitempty"`
}

type ProviderDescriptor struct {
	APIVersion    string                                `json:"api_version"`
	Source        string                                `json:"source"`
	Version       string                                `json:"version"`
	Editions      []string                              `json:"editions"`
	Profiles      []string                              `json:"profiles"`
	Capabilities  []string                              `json:"capabilities"`
	ConfigSchema  map[string]any                        `json:"config_schema"`
	InstanceKinds map[string]ProviderInstanceDescriptor `json:"instance_kinds"`
	RuntimeABI    string                                `json:"runtime_abi"`
	DeploymentABI string                                `json:"deployment_abi"`
	MigrationABI  string                                `json:"migration_abi,omitempty"`
}

type ProviderInstanceDescriptor struct {
	Capabilities []string `json:"capabilities"`
	Lifecycles   []string `json:"lifecycles"`
}

type resolvedModuleLocation struct {
	Directory   string
	LogicalBase string
	LockEntry   *LockEntry
}

func resolveModuleLocation(root, callerDirectory, source, constraint string, lockfile *Lockfile) (resolvedModuleLocation, error) {
	if strings.TrimSpace(source) == "" || filepath.IsAbs(source) {
		return resolvedModuleLocation{}, fmt.Errorf("module source must be a non-empty portable path or registry identity")
	}
	localPath := filepath.Clean(filepath.Join(callerDirectory, filepath.FromSlash(source)))
	localInfo, localErr := os.Stat(localPath)
	local := strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || localErr == nil && localInfo.IsDir()
	if local {
		if !pathWithin(root, localPath) {
			return resolvedModuleLocation{}, fmt.Errorf("local module source escapes the workspace")
		}
		if err := rejectPathSymlinks(root, localPath); err != nil {
			return resolvedModuleLocation{}, err
		}
		if localErr != nil || !localInfo.IsDir() {
			return resolvedModuleLocation{}, fmt.Errorf("local module source is unavailable: %s", source)
		}
		return resolvedModuleLocation{Directory: localPath}, nil
	}
	if strings.TrimSpace(constraint) == "" {
		return resolvedModuleLocation{}, fmt.Errorf("registry module %s requires an explicit version constraint", source)
	}
	entry, ok := lockfile.find("module", source)
	if !ok {
		return resolvedModuleLocation{}, fmt.Errorf("missing locked module %s; install it explicitly before offline compilation", source)
	}
	if !semanticVersionSatisfies(entry.Version, constraint) {
		return resolvedModuleLocation{}, fmt.Errorf("locked module %s version %s does not satisfy %s", source, entry.Version, constraint)
	}
	directory, err := lockedCachePath(root, entry.Integrity)
	if err != nil {
		return resolvedModuleLocation{}, err
	}
	digest, err := registryContentDigest(directory)
	if err != nil || digest != entry.Integrity {
		return resolvedModuleLocation{}, fmt.Errorf("locked module cache integrity mismatch for %s", source)
	}
	entryCopy := entry
	logical := "registry/" + strings.Trim(source, "/") + "@" + entry.Version
	return resolvedModuleLocation{Directory: directory, LogicalBase: logical, LockEntry: &entryCopy}, nil
}

func rejectPathSymlinks(root, target string) error {
	root, _ = filepath.Abs(root)
	target, _ = filepath.Abs(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes workspace")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink: %s", filepath.ToSlash(relative))
		}
	}
	return nil
}

func loadLockfile(root string) (*Lockfile, []Diagnostic, error) {
	path := filepath.Join(root, "scenery.lock.scn")
	info, lstatErr := os.Lstat(path)
	if lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("lockfile must be a regular non-symlink file")
		}
		if err := rejectPathSymlinks(root, path); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(lstatErr) {
		return nil, nil, lstatErr
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	sourceID := sourceID("scenery.lock.scn")
	positions := newSourcePositionIndex(data)
	file, hclDiagnostics := hclsyntax.ParseConfig(data, "scenery.lock.scn", hcl.Pos{Line: 1, Column: 1})
	diagnostics := diagnosticsFromHCL(sourceID, positions, hclDiagnostics)
	if file == nil || hclDiagnostics.HasErrors() {
		return nil, diagnostics, nil
	}
	body := file.Body.(*hclsyntax.Body)
	lockfile := &Lockfile{}
	lockBlocks := 0
	previousKey := ""
	seen := map[string]bool{}
	for _, rawBlock := range body.Blocks {
		block := convertBlock(sourceID, data, positions, rawBlock)
		switch block.Type {
		case "lock":
			lockBlocks++
			if len(block.Labels) != 0 {
				diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "lock block does not accept labels", block))
				continue
			}
			lockfile.Schema, _ = literalString(block, "schema")
			for name := range block.Attributes {
				if name != "schema" {
					diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "unknown lock field "+name, block))
				}
			}
		case "module", "provider", "extension":
			entry, entryDiagnostics := lockEntryFromBlock(block)
			diagnostics = append(diagnostics, entryDiagnostics...)
			key := entry.Kind + "\x00" + entry.Source
			if seen[key] {
				diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "duplicate locked dependency "+entry.Source, block))
			}
			seen[key] = true
			if previousKey != "" && key < previousKey {
				diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "lock entries must be sorted by kind and source", block))
			}
			previousKey = key
			lockfile.Entries = append(lockfile.Entries, entry)
		default:
			diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "unknown lock block "+block.Type, block))
		}
	}
	if lockBlocks != 1 || lockfile.Schema != LockfileSchema {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN3100", Severity: "error", Message: "lockfile requires exactly one lock block with schema scenery.lock/v1"})
	}
	return lockfile, diagnostics, nil
}

func lockEntryFromBlock(block *Block) (LockEntry, []Diagnostic) {
	entry := LockEntry{Kind: block.Type}
	if len(block.Labels) == 1 {
		entry.Name = block.Labels[0]
	}
	entry.Source, _ = literalString(block, "source")
	entry.Version, _ = literalString(block, "version")
	entry.Integrity, _ = literalString(block, "integrity")
	entry.CompileDescriptorDigest, _ = literalString(block, "compile_descriptor_digest")
	entry.RuntimeABI, _ = literalString(block, "runtime_abi")
	entry.DeploymentABI, _ = literalString(block, "deployment_abi")
	entry.MigrationABI, _ = literalString(block, "migration_abi")
	entry.PackageContractABIRevision, _ = literalString(block, "package_contract_abi_revision")
	entry.Dependencies = literalStringList(block, "dependencies")
	allowed := map[string]bool{
		"source": true, "version": true, "integrity": true, "compile_descriptor_digest": true,
		"runtime_abi": true, "deployment_abi": true, "migration_abi": true,
		"package_contract_abi_revision": true, "dependencies": true,
	}
	var diagnostics []Diagnostic
	if len(block.Labels) != 1 || !sceneryIdentifierPattern.MatchString(entry.Name) {
		diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "locked dependency requires one lower_snake label", block))
	}
	for name := range block.Attributes {
		if !allowed[name] {
			diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "unknown locked dependency field "+name, block))
		}
	}
	if entry.Source == "" || entry.Version == "" || entry.Integrity == "" || !isCanonicalSHA256Digest(entry.Integrity) {
		diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "locked dependency requires source, exact version, and SHA-256 integrity", block))
	}
	if _, err := parseSemanticVersion(entry.Version); err != nil {
		diagnostics = append(diagnostics, lockDiagnostic("SCN3100", "locked dependency version must be exact semantic version", block))
	}
	entry.Dependencies = canonicalStrings(entry.Dependencies)
	return entry, diagnostics
}

func lockDiagnostic(code, message string, block *Block) Diagnostic {
	rng := block.Range
	return Diagnostic{Code: code, Severity: "error", Message: message, Range: &rng}
}

func (lockfile *Lockfile) find(kind, source string) (LockEntry, bool) {
	if lockfile == nil {
		return LockEntry{}, false
	}
	for _, entry := range lockfile.Entries {
		if entry.Kind == kind && entry.Source == source {
			return entry, true
		}
	}
	return LockEntry{}, false
}

func resolveLockedProviders(root string, resources []Resource, lockfile *Lockfile) ([]Resource, []Diagnostic) {
	resolved := append([]Resource(nil), resources...)
	var diagnostics []Diagnostic
	for index := range resolved {
		provider := &resolved[index]
		if provider.Kind != "scenery.provider/v1" {
			continue
		}
		source, constraint := stringValue(provider.Spec["source"]), stringValue(provider.Spec["version"])
		entry, ok := lockfile.find("provider", source)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3101", Severity: "error", Message: "missing locked provider " + source, Address: provider.Address, Suggestions: []string{"install the provider explicitly before offline compilation"}})
			continue
		}
		if !semanticVersionSatisfies(entry.Version, constraint) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3102", Severity: "error", Message: fmt.Sprintf("locked provider %s version %s does not satisfy %s", source, entry.Version, constraint), Address: provider.Address})
			continue
		}
		descriptor, digest, err := lockedProviderDescriptor(root, entry)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3103", Severity: "error", Message: err.Error(), Address: provider.Address, Suggestions: []string{"restore the immutable provider cache entry"}})
			continue
		}
		if descriptor.Source != source || descriptor.Version != entry.Version || !containsExactString(descriptor.Editions, Edition) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3103", Severity: "error", Message: "locked provider descriptor identity is incompatible", Address: provider.Address})
			continue
		}
		for _, field := range []string{"config", "config_schema", "capabilities", "require_capabilities"} {
			if provider.Spec[field] != nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3104", Severity: "error", Message: "provider compile metadata comes from the locked descriptor, not application source", Address: provider.Address, Path: "/spec/" + field})
			}
		}
		provider.Spec = cloneMapValue(provider.Spec)
		provider.Spec["locked_version"] = entry.Version
		provider.Spec["locked_integrity"] = entry.Integrity
		provider.Spec["compile_descriptor_digest"] = digest
		provider.Spec["runtime_abi"] = descriptor.RuntimeABI
		provider.Spec["deployment_abi"] = descriptor.DeploymentABI
		provider.Spec["migration_abi"] = descriptor.MigrationABI
		provider.Spec["capabilities"] = stringsToAny(canonicalStrings(descriptor.Capabilities))
		provider.Spec["config_schema"] = cloneMapValue(descriptor.ConfigSchema)
		provider.Spec["instance_kinds"] = providerInstanceKindsValue(descriptor.InstanceKinds)
		if provider.Origin.FieldProvenance == nil {
			provider.Origin.FieldProvenance = map[string]FieldProvenance{}
		}
		field := FieldProvenance{
			Kind: "provider_descriptor", DeclaredAt: provider.Origin.DeclarationRange,
			ProvidedBy: source + "@" + entry.Version, SourceAddress: provider.Address,
			Transformations: []string{"locked_provider_descriptor"},
		}
		for _, name := range []string{"locked_version", "locked_integrity", "compile_descriptor_digest", "runtime_abi", "deployment_abi", "migration_abi", "capabilities", "config_schema", "instance_kinds"} {
			provider.Origin.FieldProvenance["/spec/"+name] = field
		}
	}
	return resolved, diagnostics
}

func enrichProviderInstances(resources []Resource) ([]Resource, []Diagnostic) {
	resolved := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: resolved})
	var diagnostics []Diagnostic
	for index := range resolved {
		instance := &resolved[index]
		kindName := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(instance.Kind, "/v1"), "scenery."), "-", "_")
		if kindName != "data_source" && kindName != "execution_engine" && kindName != "event_bus" && kindName != "secret_store" {
			continue
		}
		providerAddress := resolveResourceRef(*instance, refString(instance.Spec["provider"]), "provider")
		provider := byAddress[providerAddress]
		if provider.Kind != "scenery.provider/v1" {
			continue
		}
		kinds, _ := provider.Spec["instance_kinds"].(map[string]any)
		kind, _ := kinds[kindName].(map[string]any)
		if kind == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3105", Severity: "error", Message: "provider does not support instance kind " + kindName, Address: instance.Address})
			continue
		}
		lifecycle := stringValue(instance.Spec["lifecycle"])
		if lifecycle == "" {
			lifecycle = "external"
		}
		if !containsExactString(stringValues(kind["lifecycles"]), lifecycle) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3105", Severity: "error", Message: "provider does not support lifecycle " + lifecycle, Address: instance.Address})
		}
		capabilities := canonicalStrings(append(stringValues(provider.Spec["capabilities"]), stringValues(kind["capabilities"])...))
		available := stringSliceSet(capabilities)
		for _, required := range stringValues(instance.Spec["require_capabilities"]) {
			if !available[required] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN3106", Severity: "error", Message: "locked provider does not supply required capability " + required, Address: instance.Address})
			}
		}
		diagnostics = append(diagnostics, validateProviderConfig(*instance, provider.Spec["config_schema"])...)
		instance.Spec = cloneMapValue(instance.Spec)
		instance.Spec["effective_capabilities"] = stringsToAny(capabilities)
		instance.Spec["provider_descriptor_digest"] = provider.Spec["compile_descriptor_digest"]
		if instance.Origin.FieldProvenance == nil {
			instance.Origin.FieldProvenance = map[string]FieldProvenance{}
		}
		field := FieldProvenance{Kind: "provider_descriptor", ProvidedBy: provider.Address, SourceAddress: provider.Address, Transformations: []string{"provider_instance_resolution"}}
		instance.Origin.FieldProvenance["/spec/effective_capabilities"] = field
		instance.Origin.FieldProvenance["/spec/provider_descriptor_digest"] = field
	}
	return resolved, diagnostics
}

func validateProviderConfig(instance Resource, rawSchema any) []Diagnostic {
	schema, _ := rawSchema.(map[string]any)
	config, _ := instance.Spec["config"].(map[string]any)
	var diagnostics []Diagnostic
	for name, value := range config {
		field, ok := schema[name].(map[string]any)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3107", Severity: "error", Message: "unknown provider config field " + name, Address: instance.Address, Path: "/spec/config/" + escapeJSONPointer(name)})
			continue
		}
		if typeName := stringValue(field["type"]); typeName != "" && !deploymentValueMatchesType(value, typeName) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3107", Severity: "error", Message: "provider config field " + name + " has the wrong type", Address: instance.Address, Path: "/spec/config/" + escapeJSONPointer(name)})
		}
	}
	for name, raw := range schema {
		field, _ := raw.(map[string]any)
		if field["required"] == true && config[name] == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3107", Severity: "error", Message: "missing required provider config field " + name, Address: instance.Address, Path: "/spec/config/" + escapeJSONPointer(name)})
		}
	}
	return diagnostics
}

func lockedProviderDescriptor(root string, entry LockEntry) (ProviderDescriptor, string, error) {
	if descriptor, ok := builtinProviderDescriptors()[entry.Source]; ok {
		digest := providerDescriptorDigest(descriptor)
		if entry.Integrity != digest || entry.CompileDescriptorDigest != "" && entry.CompileDescriptorDigest != digest {
			return ProviderDescriptor{}, "", fmt.Errorf("locked builtin provider %s failed integrity verification", entry.Source)
		}
		return descriptor, digest, nil
	}
	cachePath, err := lockedCachePath(root, entry.Integrity)
	if err != nil {
		return ProviderDescriptor{}, "", err
	}
	digest, err := registryContentDigest(cachePath)
	if err != nil || digest != entry.Integrity {
		return ProviderDescriptor{}, "", fmt.Errorf("locked provider cache integrity mismatch for %s", entry.Source)
	}
	data, err := os.ReadFile(filepath.Join(cachePath, "scenery.provider.json"))
	if err != nil {
		return ProviderDescriptor{}, "", fmt.Errorf("locked provider descriptor is unavailable for %s", entry.Source)
	}
	var descriptor ProviderDescriptor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return ProviderDescriptor{}, "", fmt.Errorf("invalid locked provider descriptor for %s: %w", entry.Source, err)
	}
	descriptorDigest := providerDescriptorDigest(descriptor)
	if entry.CompileDescriptorDigest != "" && entry.CompileDescriptorDigest != descriptorDigest {
		return ProviderDescriptor{}, "", fmt.Errorf("locked provider compile descriptor digest mismatch for %s", entry.Source)
	}
	return descriptor, descriptorDigest, nil
}

func lockedCachePath(root, integrity string) (string, error) {
	hexDigest := strings.TrimPrefix(integrity, "sha256:")
	type cacheCandidate struct {
		anchor string
		path   string
	}
	candidates := []cacheCandidate{{anchor: root, path: filepath.Join(root, ".scenery", "cache", "vnext", "sha256", hexDigest)}}
	if cache, err := os.UserCacheDir(); err == nil {
		candidates = append(candidates, cacheCandidate{anchor: cache, path: filepath.Join(cache, "scenery", "vnext", "sha256", hexDigest)})
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		anchorInfo, err := os.Lstat(candidate.anchor)
		if err != nil || anchorInfo.Mode()&os.ModeSymlink != 0 || !anchorInfo.IsDir() {
			continue
		}
		if err := rejectPathSymlinks(candidate.anchor, candidate.path); err == nil {
			return candidate.path, nil
		}
	}
	return "", fmt.Errorf("immutable cache entry %s is unavailable", integrity)
}

func registryContentDigest(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("registry cache root must be a non-symlink directory")
	}
	entries := map[string]string{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("registry cache contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("registry cache contains a non-regular file")
		}
		relative, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries[filepath.ToSlash(relative)] = "sha256:" + hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return "", err
	}
	return revisionHash("scenery.registry-content.v1\x00", entries), nil
}

func providerDescriptorDigest(descriptor ProviderDescriptor) string {
	return revisionHash("scenery.provider-descriptor.v1\x00", descriptor)
}

func BuiltinProviderLock(source string) (version, integrity string, ok bool) {
	descriptor, ok := builtinProviderDescriptors()[source]
	if !ok {
		return "", "", false
	}
	return descriptor.Version, providerDescriptorDigest(descriptor), true
}

func builtinProviderDescriptors() map[string]ProviderDescriptor {
	field := func(typeName string, required, deploymentBindable bool) map[string]any {
		return map[string]any{"type": typeName, "required": required, "deployment_bindable": deploymentBindable}
	}
	lifecycles := []string{"attached", "ephemeral", "external", "managed"}
	return map[string]ProviderDescriptor{
		"registry.scenery.dev/core/postgres": {
			APIVersion: "scenery.provider-descriptor/v1", Source: "registry.scenery.dev/core/postgres", Version: "2.1.0", Editions: []string{Edition},
			Profiles:      []string{"scenery.data/v1", "scenery.deployment/v1"},
			ConfigSchema:  map[string]any{"database": field("string", true, true), "region": field("string", false, true), "scope": field("string", false, false)},
			InstanceKinds: map[string]ProviderInstanceDescriptor{"data_source": {Capabilities: []string{"sql.fixture/v1", "sql.migration/v1", "sql.query/v1", "sql.transaction/v1"}, Lifecycles: lifecycles}},
			RuntimeABI:    "scenery.data-runtime/v1", DeploymentABI: deploymentProviderABI, MigrationABI: "scenery.data-migration/v1",
		},
		"registry.scenery.dev/core/storage": {
			APIVersion: "scenery.provider-descriptor/v1", Source: "registry.scenery.dev/core/storage", Version: "2.0.0", Editions: []string{Edition},
			Profiles: []string{"scenery.data/v1", "scenery.deployment/v1"},
			ConfigSchema: map[string]any{
				"bucket": field("string", true, true),
				"scope":  field("string", false, false),
				"limit":  field("size", false, true),
			},
			InstanceKinds: map[string]ProviderInstanceDescriptor{"data_source": {Capabilities: []string{"object.delete/v1", "object.read/v1", "object.write/v1"}, Lifecycles: lifecycles}},
			RuntimeABI:    "scenery.object/v1", DeploymentABI: deploymentProviderABI,
		},
		"registry.scenery.dev/core/durable": {
			APIVersion: "scenery.provider-descriptor/v1", Source: "registry.scenery.dev/core/durable", Version: "1.0.0", Editions: []string{Edition},
			Profiles: []string{"scenery.runtime-durable/v1", "scenery.deployment/v1"}, ConfigSchema: map[string]any{},
			InstanceKinds: map[string]ProviderInstanceDescriptor{"execution_engine": {Capabilities: []string{"execution.durable/v1"}, Lifecycles: lifecycles}},
			RuntimeABI:    "scenery.execution-runtime/v1", DeploymentABI: deploymentProviderABI,
		},
		"registry.scenery.dev/core/kafka": {
			APIVersion: "scenery.provider-descriptor/v1", Source: "registry.scenery.dev/core/kafka", Version: "1.0.0", Editions: []string{Edition},
			Profiles: []string{"scenery.events/v1", "scenery.deployment/v1"}, ConfigSchema: map[string]any{"brokers": field("list(string)", true, true)},
			InstanceKinds: map[string]ProviderInstanceDescriptor{"event_bus": {Capabilities: []string{"events.consume/v1", "events.publish/v1"}, Lifecycles: lifecycles}},
			RuntimeABI:    "scenery.events-runtime/v1", DeploymentABI: deploymentProviderABI,
		},
		"registry.scenery.dev/core/vault": {
			APIVersion: "scenery.provider-descriptor/v1", Source: "registry.scenery.dev/core/vault", Version: "1.0.0", Editions: []string{Edition},
			Profiles: []string{"scenery.deployment/v1"}, ConfigSchema: map[string]any{"address": field("url", true, true)},
			InstanceKinds: map[string]ProviderInstanceDescriptor{"secret_store": {Capabilities: []string{"secrets.resolve/v1"}, Lifecycles: []string{"attached", "external", "managed"}}},
			RuntimeABI:    "scenery.secrets-runtime/v1", DeploymentABI: deploymentProviderABI,
		},
	}
}

func providerInstanceKindsValue(kinds map[string]ProviderInstanceDescriptor) map[string]any {
	result := make(map[string]any, len(kinds))
	for name, descriptor := range kinds {
		result[name] = map[string]any{"capabilities": stringsToAny(canonicalStrings(descriptor.Capabilities)), "lifecycles": stringsToAny(canonicalStrings(descriptor.Lifecycles))}
	}
	return result
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func stringSliceSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
