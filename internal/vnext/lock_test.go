package vnext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockedBuiltinProviderDerivesCapabilitiesAndSchema(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "external")
	result, err := compileContractGraph(root, false)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	resources := resourcesByAddress(result.Manifest)
	provider := resources["app/provider/postgres"]
	if provider.Spec["locked_version"] != "2.1.0" || provider.Spec["compile_descriptor_digest"] == "" {
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
		"registry.scenery.dev/core/durable":  "1.0.0 sha256:bd3b09a61fe989b3261d8a50f91021a0919f1d37f6d228605bb1613e25a1dd55",
		"registry.scenery.dev/core/postgres": "2.1.0 sha256:ef86899a2d565d65e88a47a9d1099c3fd0fc8b9cdc021a9c5e54ce19636e0ec6",
	}
	for source, expected := range want {
		version, integrity, ok := BuiltinProviderLock(source)
		if got := version + " " + integrity; !ok || got != expected {
			t.Fatalf("builtin lock %s = %q, %t; want %q", source, got, ok, expected)
		}
	}
}

func TestProviderCompilationFailsClosedForMissingOrTamperedLock(t *testing.T) {
	root := deploymentPlanFixture(t, "external")
	lockPath := filepath.Join(root, "scenery.lock.scn")
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
	path := filepath.Join(root, "scenery.scn")
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

func TestLocalModuleVersionConstraintUsesPackageDeclaration(t *testing.T) {
	root := deploymentPlanFixture(t, "external")
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `source = "./house"`, `source  = "./house"
  version = ">= 2.0.0, < 3.0.0"`, 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !hasDiagnostic(result.Diagnostics, "SCN3102") {
		t.Fatalf("err=%v diagnostics=%#v", err, result.Diagnostics)
	}
}

func TestNonBuiltinProviderDescriptorRequiresVerifiedImmutableCache(t *testing.T) {
	root := t.TempDir()
	descriptor := ProviderDescriptor{
		APIVersion: "scenery.provider-descriptor/v1", Source: "registry.example.test/acme/store", Version: "1.2.3",
		Editions: []string{Edition}, Profiles: []string{"scenery.data/v1"}, ConfigSchema: map[string]any{},
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
	cache := filepath.Join(root, ".scenery", "cache", "vnext", "sha256", strings.TrimPrefix(integrity, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, cache); err != nil {
		t.Fatal(err)
	}
	loaded, digest, err := lockedProviderDescriptor(root, LockEntry{Kind: "provider", Source: descriptor.Source, Version: descriptor.Version, Integrity: integrity, CompileDescriptorDigest: providerDescriptorDigest(descriptor)})
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
	cacheParent := filepath.Join(root, ".scenery", "cache", "vnext", "sha256")
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
  version         = "2.4.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}

record "point" {
  field "x" { type = float64 }
  field "y" { type = float64 }
}

export "point" {
  value = record.point
}
`
	if err := os.WriteFile(filepath.Join(staging, "scenery.package.scn"), []byte(packageSource), 0o644); err != nil {
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
	cache := filepath.Join(root, ".scenery", "cache", "vnext", "sha256", strings.TrimPrefix(integrity, "sha256:"))
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, cache); err != nil {
		t.Fatal(err)
	}
	rootSource := `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1"]
}
application "registry_app" { version = "1.0.0" }
module "geometry" {
  source  = "registry.scenery.dev/geo/geometry"
  version = ">= 2.0.0, < 3.0.0"
}
`
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(rootSource), 0o644); err != nil {
		t.Fatal(err)
	}
	lockfile := fmt.Sprintf(`lock { schema = %q }

module "geometry" {
  source                    = "registry.scenery.dev/geo/geometry"
  version                   = "2.4.0"
  integrity                 = %q
  compile_descriptor_digest = %q
}
`, LockfileSchema, integrity, compileDigest)
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte(lockfile), 0o644); err != nil {
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
		registrySource = registrySource || strings.HasPrefix(source.URI, "registry/registry.scenery.dev/geo/geometry@2.4.0/")
	}
	if !registrySource {
		t.Fatalf("portable registry source map = %#v", result.Manifest.SourceMap)
	}
	workspaceRevision := result.WorkspaceRevision
	if err := os.WriteFile(filepath.Join(cache, "scenery.package.scn"), append([]byte(packageSource), []byte("# tampered\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	tampered, err := Compile(root)
	if err != nil || tampered.Valid() || tampered.WorkspaceRevision != workspaceRevision || !hasDiagnostic(tampered.Diagnostics, "SCN3101") {
		t.Fatalf("tampered: err=%v workspace=%q/%q diagnostics=%#v", err, workspaceRevision, tampered.WorkspaceRevision, tampered.Diagnostics)
	}
}
