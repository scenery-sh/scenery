package generate

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/runtimeassets"
)

func TestRenderAssistantAssetRegistryIsDeterministicAndProviderNeutral(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	node := testAssetArchive(t, "node")
	capsule := testAssetArchive(t, "capsule")
	descriptor := testAssistantAssetDescriptor("assistant/support", "darwin/arm64", node, capsule)
	input := testAssistantAssetInput(t, descriptor, node, capsule)
	first, err := RenderAssistantAssetRegistry(result, []AssistantAssetInput{input})
	if err != nil {
		t.Fatalf("render assets: %v", err)
	}
	reversed, err := RenderAssistantAssetRegistry(result, []AssistantAssetInput{input})
	if err != nil {
		t.Fatalf("render assets second time: %v", err)
	}
	if len(first) != len(reversed) {
		t.Fatalf("file count changed: %d != %d", len(first), len(reversed))
	}
	for path, data := range first {
		if !bytes.Equal(data, reversed[path]) {
			t.Fatalf("generated asset changed at %s", path)
		}
		if bytes.Contains(bytes.ToLower(data), []byte("eve")) {
			t.Fatalf("provider token escaped into generated asset %s", path)
		}
	}
	if _, ok := first["internal/scenerygen/assets/assets.gen.go"]; !ok {
		t.Fatal("generated embed registry is missing")
	}
	if _, ok := first["internal/scenerygen/assets/descriptors/node-tree-"+strings.TrimPrefix(node.Descriptor.Digest, "sha256:")+".json"]; !ok {
		t.Fatal("canonical Node tree descriptor is missing")
	}
	if _, ok := first["internal/scenerygen/assets/descriptors/capsule-tree-"+strings.TrimPrefix(capsule.Descriptor.Digest, "sha256:")+".json"]; !ok {
		t.Fatal("canonical capsule tree descriptor is missing")
	}
	if !strings.Contains(string(first["internal/scenerygen/assets/assets.gen.go"]), "NodeDescriptorJSON") || !strings.Contains(string(first["internal/scenerygen/assets/assets.gen.go"]), "CapsuleDescriptorJSON") {
		t.Fatal("generated asset API does not expose canonical tree descriptors")
	}
}

func TestRenderAssistantAssetRegistrySortsInputsAndDeduplicatesNodeArchive(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	node := testAssetArchive(t, "node")
	oneCapsule := testAssetArchive(t, "capsule-zulu")
	twoCapsule := testAssetArchive(t, "capsule-alpha")
	one := testAssistantAssetDescriptor("assistant/zulu", "darwin/arm64", node, oneCapsule)
	two := testAssistantAssetDescriptor("assistant/alpha", "darwin/arm64", node, twoCapsule)
	files, err := RenderAssistantAssetRegistry(result, []AssistantAssetInput{testAssistantAssetInput(t, one, node, oneCapsule), testAssistantAssetInput(t, two, node, twoCapsule)})
	if err != nil {
		t.Fatalf("render assets: %v", err)
	}
	var nodeFiles int
	for path := range files {
		if strings.Contains(path, "/archives/node-") {
			nodeFiles++
		}
	}
	if nodeFiles != 1 {
		t.Fatalf("node archive was not deduplicated: %d files", nodeFiles)
	}
	source := string(files["internal/scenerygen/assets/assets.gen.go"])
	if strings.Index(source, `AssistantAddress: "assistant/alpha"`) > strings.Index(source, `AssistantAddress: "assistant/zulu"`) {
		t.Fatal("asset registry is not sorted by assistant address")
	}
}

func TestRenderAssistantAssetRegistryGeneratedPackageTypeChecksInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result := nativeApplicationGenerationFixture(root)
	node := testAssetArchive(t, "node")
	capsule := testAssetArchive(t, "capsule")
	descriptor := testAssistantAssetDescriptor("assistant/support", "darwin/arm64", node, capsule)
	files, err := RenderAssistantAssetRegistry(result, []AssistantAssetInput{testAssistantAssetInput(t, descriptor, node, capsule)})
	if err != nil {
		t.Fatalf("render assets: %v", err)
	}
	source := files["internal/scenerygen/assets/assets.gen.go"]
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "assets.gen.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated asset package: %v", err)
	}
	config := types.Config{Importer: importer.Default()}
	if _, err := config.Check("clean.tech/internal/scenerygen/assets", fset, []*ast.File{file}, nil); err != nil {
		t.Fatalf("type-check generated asset package: %v", err)
	}
}

func testAssistantAssetDescriptor(address, target string, node, capsule runtimeassets.Archive) AssistantAssetDescriptor {
	return AssistantAssetDescriptor{
		Kind: AssistantAssetDescriptorKind, SchemaRevision: AssistantAssetSchemaRevision,
		AssistantAddress: address, Target: target, RuntimeRevision: "runtime-1", CapabilityRevision: "sha256:capability",
		NodeArchiveDigest: node.ArchiveDigest, NodeTreeDigest: node.Descriptor.Digest,
		CapsuleArchiveDigest: capsule.ArchiveDigest, CapsuleTreeDigest: capsule.Descriptor.Digest,
		CapsuleEntry: AssistantAssetCapsuleEntry, PackageLockDigest: "sha256:" + strings.Repeat("3", 64),
	}
}

func testAssistantAssetInput(t *testing.T, descriptor AssistantAssetDescriptor, node, capsule runtimeassets.Archive) AssistantAssetInput {
	t.Helper()
	nodeDescriptor, err := json.Marshal(node.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	capsuleDescriptor, err := json.Marshal(capsule.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return AssistantAssetInput{Descriptor: descriptor, NodeArchive: node.Data, NodeDescriptorJSON: nodeDescriptor, CapsuleArchive: capsule.Data, CapsuleDescriptorJSON: capsuleDescriptor}
}

func testAssetArchive(t *testing.T, name string) runtimeassets.Archive {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := runtimeassets.BuildArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}
