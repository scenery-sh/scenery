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
		"registry.scenery.dev/core/durable":  "sha256:7742f701a51958117534f2ff034e51896da732f52fecc29a507ccd7531aa4656",
		"registry.scenery.dev/core/kafka":    "sha256:9c238f03e614da6a4032a7de34e68cd242dee7f05ee6883079cea29b19c97dea",
		"registry.scenery.dev/core/postgres": "sha256:43bf851d022d3cc066de6128eef2a4cee9c62069a04040a5b6add12a3d8ac25e",
		"registry.scenery.dev/core/storage":  "sha256:3aa81185c7a43eadda26fed1f4409cbe1581eccd9514abd9e829d095306c220c",
		"registry.scenery.dev/core/vault":    "sha256:09013cd0bf06dcd03f1742cde3bead2a635b6b24232407bc9c4e5d372fef9d6c",
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
	if got := providerDescriptorDigest(descriptor); got != want {
		t.Fatalf("producer changed digest from %s to %s", want, got)
	}
	descriptor.RuntimeABI += ".changed"
	if got := providerDescriptorDigest(descriptor); got == want {
		t.Fatalf("semantic descriptor change left digest at %s", got)
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
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(rootSource), 0o644); err != nil {
		t.Fatal(err)
	}
	lockfile := fmt.Sprintf(`lock {}

module "geometry" {
  source                    = "registry.scenery.dev/geo/geometry"
  integrity                 = %q
  compile_descriptor_digest = %q
}
`, integrity, compileDigest)
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
		registrySource = registrySource || strings.HasPrefix(source.URI, "registry/registry.scenery.dev/geo/geometry#")
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
