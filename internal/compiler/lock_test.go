package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func TestLockedBuiltinProviderDerivesCapabilitiesAndSchema(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "external")
	result, err := CompileContractGraph(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	resources := resourcesByAddress(result.Manifest)
	provider := resources["app/provider/postgres"]
	if provider.Spec["compile_descriptor_digest"] == "" {
		t.Fatalf("provider = %#v", provider.Spec)
	}
	source := resources["app/data_source/database"]
	capabilities := stringSliceSet(stringValues(source.Spec["effective_capabilities"]))
	for _, capability := range []string{"sql.query/v1", "sql.transaction/v1", "sql.migration/v1"} {
		if !capabilities[capability] {
			t.Errorf("missing derived capability %s in %#v", capability, source.Spec)
		}
	}
}

func TestBuiltinProviderLockDigestsAreStable(t *testing.T) {
	want := map[string]string{
		"registry.scenery.dev/core/durable":  "sha256:1d096d5c72c3433f0b728da8279fbb91b9a5076b0f656b695d1a9a60fbd62079",
		"registry.scenery.dev/core/kafka":    "sha256:66d2a8e53f7740ee8bd39c12e8946922c092189312de25be34f388e8dc87562d",
		"registry.scenery.dev/core/postgres": "sha256:42156917276bd7d0b85efb35c54c0fa898101d8ed4db1da6876cb2a34b27152f",
		"registry.scenery.dev/core/storage":  "sha256:1772629d332b4d3276fd042f5b13ea4fd770a3b3448a7e15430f7a401a0817e6",
		"registry.scenery.dev/core/vault":    "sha256:3b0b5bff3751155cbbe028c719a7b5477db6265c2dcaf6aa889867d7d6470ea1",
	}
	for source, expected := range want {
		integrity, ok := BuiltinProviderLock(source)
		if got := integrity; !ok || got != expected {
			t.Errorf("builtin lock %s = %q, %t; want %q", source, got, ok, expected)
		}
	}
}

func TestProviderDescriptorDigestIgnoresProducer(t *testing.T) {
	descriptor := builtinProviderDescriptors()["registry.scenery.dev/core/postgres"]
	want := providerDescriptorDigest(descriptor)
	descriptor.Producer = machine.Producer{
		Version: "release", Commit: "different-build", BuiltAt: "tomorrow",
		Toolchain: machine.Toolchain{GoVersion: "different-go", ManifestRevision: "sha256:different"},
	}
	descriptor.SpecRevision = "sha256:unrelated-typescript-generation-change"
	if got := providerDescriptorDigest(descriptor); got != want {
		t.Fatalf("incidental identity changed digest from %s to %s", want, got)
	}
	descriptor.RuntimeABI += ".changed"
	if got := providerDescriptorDigest(descriptor); got == want {
		t.Fatalf("semantic descriptor change left digest at %s", got)
	}
}

func TestProviderDescriptorDigestBindsSemanticContent(t *testing.T) {
	for name, mutate := range map[string]func(*ProviderDescriptor){
		"schema":       func(d *ProviderDescriptor) { d.SchemaRevision = "sha256:changed-schema" },
		"source":       func(d *ProviderDescriptor) { d.Source += "/changed" },
		"capabilities": func(d *ProviderDescriptor) { d.Capabilities = append(d.Capabilities, "new.capability/v1") },
		"config":       func(d *ProviderDescriptor) { d.ConfigSchema["new_field"] = map[string]any{"type": "string"} },
		"instances": func(d *ProviderDescriptor) {
			d.InstanceKinds["new_kind"] = ProviderInstanceDescriptor{Lifecycles: []string{"managed"}}
		},
		"runtime":    func(d *ProviderDescriptor) { d.RuntimeABI += ".changed" },
		"deployment": func(d *ProviderDescriptor) { d.DeploymentABI += ".changed" },
		"migration":  func(d *ProviderDescriptor) { d.MigrationABI += ".changed" },
	} {
		t.Run(name, func(t *testing.T) {
			descriptor := builtinProviderDescriptors()["registry.scenery.dev/core/postgres"]
			before := providerDescriptorDigest(descriptor)
			mutate(&descriptor)
			if after := providerDescriptorDigest(descriptor); before == after {
				t.Fatal("semantic change did not change content identity")
			}
		})
	}
}

func TestProviderCompilationFailsClosedForMissingOrTamperedLock(t *testing.T) {
	root := deploymentPlanFixture(t, "external")
	lockPath := filepath.Join(root, appLockFilename)
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	missing, err := Compile(root)
	if err != nil || !hasDiagnostic(missing.Diagnostics, "SCN3101") {
		t.Fatalf("missing lock: err=%v diagnostics=%#v", err, missing.Diagnostics)
	}
	tampered := strings.Replace(string(lockBytes), "sha256:", "sha256:0", 1)
	if err := os.WriteFile(lockPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	invalid, err := Compile(root)
	if err != nil || !hasDiagnostic(invalid.Diagnostics, "SCN3100") {
		t.Fatalf("tampered lock: err=%v diagnostics=%#v", err, invalid.Diagnostics)
	}
}

func TestRequiredCapabilityCannotBeGrantedByAssertion(t *testing.T) {
	root := deploymentPlanFixture(t, "external")
	path := filepath.Join(root, appFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `lifecycle = "external"`, `lifecycle = "external"
  require_capabilities = ["root.shell/v1"]`, 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !hasDiagnostic(result.Diagnostics, "SCN3106") {
		t.Fatalf("err=%v diagnostics=%#v", err, result.Diagnostics)
	}
}

func TestNonBuiltinProviderDescriptorRequiresVerifiedImmutableCache(t *testing.T) {
	root := t.TempDir()
	descriptor := ProviderDescriptor{
		ArtifactIdentity: machine.NewArtifactIdentity(providerDescriptorKind, providerSchemaDescriptor), Source: "registry.example.test/acme/store",
		ConfigSchema:  map[string]any{},
		InstanceKinds: map[string]ProviderInstanceDescriptor{"data_source": {Capabilities: []string{"kv.get/v1"}, Lifecycles: []string{"external"}}},
		RuntimeABI:    "acme.store-runtime/v1", DeploymentABI: deploymentProviderABI,
	}
	staging := filepath.Join(root, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "scenery.provider.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, err := registryContentDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".scenery", "cache", "providers", "sha256", strings.TrimPrefix(integrity, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, cache); err != nil {
		t.Fatal(err)
	}
	loaded, digest, err := lockedProviderDescriptor(root, LockEntry{Kind: "provider", Source: descriptor.Source, Integrity: integrity, CompileDescriptorDigest: providerDescriptorDigest(descriptor)})
	if err != nil || loaded.Source != descriptor.Source || digest != providerDescriptorDigest(descriptor) {
		t.Fatalf("loaded=%#v digest=%q err=%v", loaded, digest, err)
	}
}

func TestLockedCacheRejectsSymlinkRootsAndParents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	realCache := filepath.Join(root, "real-cache")
	if err := os.MkdirAll(realCache, 0o755); err != nil {
		t.Fatal(err)
	}
	integrity := "sha256:" + strings.Repeat("a", 64)
	cacheParent := filepath.Join(root, ".scenery", "cache", "providers", "sha256")
	if err := os.MkdirAll(filepath.Dir(cacheParent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realCache, cacheParent); err != nil {
		t.Fatal(err)
	}
	if _, err := lockedCachePath(root, integrity); err == nil {
		t.Fatal("lockedCachePath accepted a symlinked cache parent")
	}
	cacheRoot := filepath.Join(root, "cache-root")
	if err := os.Symlink(realCache, cacheRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := registryContentDigest(cacheRoot); err == nil {
		t.Fatal("registryContentDigest accepted a symlinked root")
	}
}

func TestRegistryModuleCompilesOnlyFromVerifiedLockAndCache(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	packageSource := `package "geometry" {
}

record "point" {
  field "x" { type = float64 }
  field "y" { type = float64 }
}

export "point" {
  value = record.point
}
`
	if err := os.WriteFile(filepath.Join(staging, packageFilename), []byte(packageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	resources, sources, diagnostics := compilePackage(root, staging, "geometry")
	if hasErrors(diagnostics) {
		t.Fatalf("package diagnostics = %#v", diagnostics)
	}
	compileDigest := packageCompileDescriptorDigest(resources, sources)
	integrity, err := registryContentDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".scenery", "cache", "providers", "sha256", strings.TrimPrefix(integrity, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, cache); err != nil {
		t.Fatal(err)
	}
	rootSource := `application "registry_app" {}
module "geometry" {
  source  = "registry.scenery.dev/geo/geometry"
}
`
	if err := os.WriteFile(filepath.Join(root, appFilename), []byte(rootSource), 0o644); err != nil {
		t.Fatal(err)
	}
	lockfile := fmt.Sprintf(`lock {}

module "geometry" {
  source                    = "registry.scenery.dev/geo/geometry"
  integrity                 = %q
  compile_descriptor_digest = %q
}
`, integrity, compileDigest)
	if err := os.WriteFile(filepath.Join(root, appLockFilename), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	if resourcesByAddress(result.Manifest)["geometry/record/point"].Address == "" {
		t.Fatalf("registry resource missing from %#v", result.Manifest.Resources)
	}
	registrySource := false
	for _, source := range result.Manifest.SourceMap {
		registrySource = registrySource || strings.HasPrefix(source.URI, "registry/registry.scenery.dev/geo/geometry#")
	}
	if !registrySource {
		t.Fatalf("portable registry source map = %#v", result.Manifest.SourceMap)
	}
	workspaceRevision := result.WorkspaceRevision
	if err := os.WriteFile(filepath.Join(cache, packageFilename), append([]byte(packageSource), []byte("# tampered\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	tampered, err := Compile(root)
	if err != nil || tampered.Valid() || tampered.WorkspaceRevision != workspaceRevision || !hasDiagnostic(tampered.Diagnostics, "SCN3101") {
		t.Fatalf("tampered: err=%v workspace=%q/%q diagnostics=%#v", err, workspaceRevision, tampered.WorkspaceRevision, tampered.Diagnostics)
	}
}
