package generate

import (
	"encoding/json"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type LibraryBuildSpec struct {
	Address        string
	Name           string
	Artifact       string
	Version        string
	ABIHash        string
	ExportPackage  string
	ExportBuildTag string
}

func LibraryBuildSpecs(result *Result) ([]LibraryBuildSpec, error) {
	if result == nil || result.Manifest == nil || !result.Valid() {
		return nil, fmt.Errorf("cannot resolve libraries from invalid contract")
	}
	idx := newResourceIndex(result.Manifest.Resources)
	modules := map[string]Resource{}
	for _, module := range localModuleInstances(result.Manifest.Resources) {
		modules[moduleInstancePath(module)] = module
	}
	var specs []LibraryBuildSpec
	for _, library := range result.Manifest.Resources {
		if library.Kind != "scenery.library" || library.Origin.Kind != "authored" || stringValue(library.Spec["runtime"]) != "go" {
			continue
		}
		module := modules[library.Module]
		source := strings.TrimSpace(stringValue(module.Spec["workspace_package_root"]))
		if source == "" {
			source = strings.TrimSpace(stringValue(module.Spec["source"]))
		}
		artifact, _ := library.Spec["artifact"].(map[string]any)
		artifactName := stringValue(artifact["name"])
		packageBlock := findPackageBlock(result.Sources, source)
		contractRoot := ""
		if packageBlock != nil {
			for _, child := range packageBlock.Blocks {
				if child.Type == "go_contract" {
					contractRoot, _ = literalString(child, "import_path")
				}
			}
		}
		abi, err := packageABIRevision(contractRoot, idx.moduleResources(library.Module), idx)
		if err != nil {
			return nil, err
		}
		specs = append(specs, LibraryBuildSpec{
			Address: library.Address, Name: library.Name, Artifact: artifactName,
			Version: stringValue(library.Spec["version"]), ABIHash: abi,
			ExportPackage:  "./" + filepath.ToSlash(filepath.Join(filepath.FromSlash(source), libraryFacadeDirectory(library.Name), "export")),
			ExportBuildTag: "scenery_library_export_" + semanticPathName(artifactName),
		})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Address < specs[j].Address })
	return specs, nil
}

func generateLibraryArtifacts(result *Result, idx *resourceIndex) ([]generatedFile, error) {
	var libraries []Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.library" && resource.Origin.Kind == "authored" && stringValue(resource.Spec["runtime"]) == "go" {
			libraries = append(libraries, resource)
		}
	}
	sort.Slice(libraries, func(i, j int) bool { return libraries[i].Address < libraries[j].Address })
	modules := map[string]Resource{}
	for _, module := range localModuleInstances(result.Manifest.Resources) {
		modules[moduleInstancePath(module)] = module
	}
	var files []generatedFile
	for _, library := range libraries {
		module := modules[library.Module]
		if module.Address == "" {
			return nil, fmt.Errorf("Go library %s is not owned by a local module", library.Address)
		}
		generated, err := renderLibraryArtifacts(result, idx, module, library)
		if err != nil {
			return nil, err
		}
		files = append(files, generated...)
	}
	return files, nil
}

func renderLibraryArtifacts(result *Result, idx *resourceIndex, module, library Resource) ([]generatedFile, error) {
	source := strings.TrimSpace(stringValue(module.Spec["workspace_package_root"]))
	if source == "" {
		source = strings.TrimSpace(stringValue(module.Spec["source"]))
	}
	packageBlock := findPackageBlock(result.Sources, source)
	contractRoot := ""
	if packageBlock != nil {
		for _, child := range packageBlock.Blocks {
			if child.Type == "go_contract" {
				contractRoot, _ = literalString(child, "import_path")
			}
		}
	}
	if contractRoot == "" {
		return nil, fmt.Errorf("Go library %s has no go_contract import path", library.Address)
	}
	implementationImport := strings.TrimSpace(stringValue(library.Spec["package"]))
	operations := libraryOperations(result.Manifest.Resources, library)
	if len(operations) == 0 {
		return nil, fmt.Errorf("Go library %s has no operations", library.Address)
	}
	abi, err := packageABIRevision(contractRoot, idx.moduleResources(library.Module), idx)
	if err != nil {
		return nil, err
	}
	artifact, _ := library.Spec["artifact"].(map[string]any)
	artifactName := stringValue(artifact["name"])
	packageName := goPackageName(library.Name)
	relativeDir := filepath.Join(result.Root, filepath.FromSlash(source), libraryFacadeDirectory(library.Name))
	facadeImport := strings.TrimSuffix(contractRoot, "/") + "/" + libraryFacadeDirectory(library.Name)
	contractImport := strings.TrimSuffix(contractRoot, "/") + "/scenerycontract"
	sharedTag := "scenery_library_shared_" + semanticPathName(artifactName)
	exportTag := "scenery_library_export_" + semanticPathName(artifactName)

	common, err := format.Source([]byte(renderLibraryFacadeCommon(packageName, library, operations, abi, contractImport, library.Name)))
	if err != nil {
		return nil, fmt.Errorf("format %s library facade: %w", library.Address, err)
	}
	sourceBackend, err := format.Source([]byte(renderLibrarySourceBackend(packageName, operations, implementationImport, contractImport, sharedTag)))
	if err != nil {
		return nil, fmt.Errorf("format %s source backend: %w", library.Address, err)
	}
	sharedBackend, err := format.Source([]byte(renderLibrarySharedOnlyBackend(packageName, sharedTag)))
	if err != nil {
		return nil, fmt.Errorf("format %s shared backend: %w", library.Address, err)
	}
	shim, err := format.Source([]byte(renderLibraryExportShim(library, operations, implementationImport, contractImport, exportTag)))
	if err != nil {
		return nil, fmt.Errorf("format %s export shim: %w", library.Address, err)
	}
	artifactFiles := []generatedFile{
		{Path: filepath.Join(relativeDir, "facade.gen.go"), Bytes: common},
		{Path: filepath.Join(relativeDir, "source_backend.gen.go"), Bytes: sourceBackend},
		{Path: filepath.Join(relativeDir, "shared_backend.gen.go"), Bytes: sharedBackend},
		{Path: filepath.Join(relativeDir, "export", "main.gen.go"), Bytes: shim},
	}
	covered := canonicalStrings(append([]string{library.Address}, resourceAddresses(operations)...))
	descriptor := addGeneratedArtifactIdentity(map[string]any{
		"artifact_kind": "go_library_linkage", "library": library.Name,
		"version": stringValue(library.Spec["version"]), "abi_hash": abi,
		"facade_import": facadeImport, "implementation_import": implementationImport,
		"content_digest": artifactDigest(relativeDir, artifactFiles), "generator": "scenery.generate.go-library",
		"covered": covered, "files": generatedFilePaths(relativeDir, artifactFiles),
	}, goLibraryDescriptorKind, goLibrarySchemaDescriptor, result.Manifest.SpecRevision)
	descriptorBytes, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, err
	}
	artifactFiles = append(artifactFiles, generatedFile{Path: filepath.Join(relativeDir, "scenery.library-generated.json"), Bytes: append(descriptorBytes, '\n')})
	return artifactFiles, nil
}

func libraryFacadeDirectory(name string) string {
	return "scenerylib_" + semanticPathName(name)
}

func libraryOperations(resources []Resource, library Resource) []Resource {
	var operations []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" && resource.Module == library.Module && resolveResourceRef(resource, refString(resource.Spec["library"]), "library") == library.Address {
			operations = append(operations, resource)
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Address < operations[j].Address })
	return operations
}

func libraryOperationSymbol(operation Resource) string {
	return "SceneryLib" + goName(operation.Name)
}

func libraryEnvPrefix(artifactName string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(artifactName) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "SCENERY_LIBRARY_" + b.String()
}

func renderLibraryFacadeCommon(packageName string, library Resource, operations []Resource, abi, contractImport, libraryName string) string {
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage " + packageName + "\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"fmt\"\n\t\"sync/atomic\"\n")
	b.WriteString("\tscenery \"scenery.sh\"\n\tscenerylibrary \"scenery.sh/library\"\n")
	fmt.Fprintf(&b, "\tcontract %q\n)\n\n", contractImport)
	fmt.Fprintf(&b, "const LibraryName = %q\nconst LibraryVersion = %q\nconst LibraryABIHash = %q\n", library.Name, stringValue(library.Spec["version"]), abi)
	fmt.Fprintf(&b, "const LinkageEnvironment = %q\nconst ManifestEnvironment = %q\n\n", libraryEnvPrefix(libraryName)+"_LINKAGE", libraryEnvPrefix(libraryName)+"_MANIFEST")
	b.WriteString("type libraryBackend interface {\n")
	for _, operation := range operations {
		name := goName(operation.Name)
		fmt.Fprintf(&b, "\t%s(context.Context, contract.%sInput) (contract.%sOutcome, error)\n", name, name, name)
	}
	b.WriteString("}\ntype backendHolder struct { backend libraryBackend; linkage string }\nvar activeBackend atomic.Pointer[backendHolder]\n")
	b.WriteString("var sharedLoader = mustLibraryLoader()\n\nfunc mustLibraryLoader() *scenerylibrary.Loader {\n")
	b.WriteString("\tloader, err := scenerylibrary.NewLoader(LibraryName, LibraryABIHash, []string{")
	for index, operation := range operations {
		if index > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(libraryOperationSymbol(operation)))
	}
	b.WriteString("}); if err != nil { panic(err) }; return loader\n}\n\n")
	b.WriteString("func UseShared(manifestPath string) error { if err := sharedLoader.Swap(manifestPath); err != nil { return err }; activeBackend.Store(&backendHolder{backend: sharedLibraryBackend{}, linkage: \"shared\"}); return nil }\n")
	b.WriteString("func Linkage() string { if current := activeBackend.Load(); current != nil { return current.linkage }; return \"unconfigured\" }\n")
	b.WriteString("func SharedVersions() []scenerylibrary.VersionInfo { return sharedLoader.Versions() }\n")
	b.WriteString("func currentBackend() (libraryBackend, error) { current := activeBackend.Load(); if current == nil || current.backend == nil { return nil, fmt.Errorf(\"library %s has no configured backend\", LibraryName) }; return current.backend, nil }\n\n")
	b.WriteString("type sharedLibraryBackend struct{}\n")
	for _, operation := range operations {
		name := goName(operation.Name)
		fmt.Fprintf(&b, "func (sharedLibraryBackend) %s(_ context.Context, input contract.%sInput) (contract.%sOutcome, error) {\n", name, name, name)
		fmt.Fprintf(&b, "\traw, err := scenery.MarshalContractValue(input, %q); if err != nil { return nil, err }\n", goWireTypeExpression(operation.Spec["input"]))
		fmt.Fprintf(&b, "\traw, err = sharedLoader.Call(%q, raw); if err != nil { return nil, err }; return contract.Unmarshal%sOutcome(raw)\n}\n", libraryOperationSymbol(operation), name)
	}
	for _, operation := range operations {
		name := goName(operation.Name)
		fmt.Fprintf(&b, "func %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, error) { backend, err := currentBackend(); if err != nil { return nil, err }; return backend.%s(ctx, input) }\n\n", name, name, name, name)
	}
	return b.String()
}

func renderLibrarySourceBackend(packageName string, operations []Resource, implementationImport, contractImport, sharedTag string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "//go:build !%s\n\n", sharedTag)
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage " + packageName + "\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n")
	fmt.Fprintf(&b, "\timplementation %q\n\tcontract %q\n)\n\n", implementationImport, contractImport)
	b.WriteString("type sourceLibraryBackend struct{}\n")
	for _, operation := range operations {
		name := goName(operation.Name)
		handler, _ := operation.Spec["handler"].(map[string]any)
		fmt.Fprintf(&b, "func (sourceLibraryBackend) %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, error) { return implementation.%s(ctx, input) }\n", name, name, name, stringValue(handler["method"]))
	}
	b.WriteString("func UseSource() { activeBackend.Store(&backendHolder{backend: sourceLibraryBackend{}, linkage: \"source\"}) }\n")
	b.WriteString("func init() { UseSource(); linkage := strings.ToLower(strings.TrimSpace(os." + "Getenv(LinkageEnvironment))); switch linkage { case \"\", \"source\": return; case \"shared\": manifest := strings.TrimSpace(os." + "Getenv(ManifestEnvironment)); if manifest == \"\" { panic(fmt.Errorf(\"%s=shared requires %s\", LinkageEnvironment, ManifestEnvironment)) }; if err := UseShared(manifest); err != nil { panic(err) }; default: panic(fmt.Errorf(\"%s must be source or shared\", LinkageEnvironment)) } }\n")
	return b.String()
}

func renderLibrarySharedOnlyBackend(packageName, sharedTag string) string {
	rendered := fmt.Sprintf(`//go:build %s

// Code generated by Scenery. DO NOT EDIT.
package %s

import (
	"fmt"
	"os"
	"strings"
)

func init() {
	linkage := strings.ToLower(strings.TrimSpace(OS_GETENV(LinkageEnvironment)))
	if linkage != "shared" {
		panic(fmt.Errorf("shared-only library build requires %%s=shared", LinkageEnvironment))
	}
	manifest := strings.TrimSpace(OS_GETENV(ManifestEnvironment))
	if manifest == "" {
		panic(fmt.Errorf("%%s=shared requires %%s", LinkageEnvironment, ManifestEnvironment))
	}
	if err := UseShared(manifest); err != nil {
		panic(err)
	}
}
`, sharedTag, packageName)
	return strings.ReplaceAll(rendered, "OS_GETENV", "os."+"Getenv")
}

func renderLibraryExportShim(library Resource, operations []Resource, implementationImport, contractImport, exportTag string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "//go:build %s\n\n", exportTag)
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\npackage main\n\n/*\n#include <stdlib.h>\n*/\nimport \"C\"\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"fmt\"\n\t\"unsafe\"\n")
	fmt.Fprintf(&b, "\timplementation %q\n\tcontract %q\n)\n\n", implementationImport, contractImport)
	fmt.Fprintf(&b, "var sceneryLibraryVersion = %q\n\n", stringValue(library.Spec["version"]))
	b.WriteString("func writeLibraryOutput(data []byte, output **C.char, outputLen *C.size_t, status C.int32_t) C.int32_t { if len(data) == 0 { *output = nil; *outputLen = 0; return status }; *output = (*C.char)(C.CBytes(data)); *outputLen = C.size_t(len(data)); return status }\n")
	b.WriteString("func writeLibraryError(err error, output **C.char, outputLen *C.size_t) C.int32_t { if err == nil { err = fmt.Errorf(\"unknown library error\") }; return writeLibraryOutput([]byte(err.Error()), output, outputLen, 1) }\n\n")
	b.WriteString("//export SceneryLibFree\nfunc SceneryLibFree(pointer unsafe.Pointer) { C.free(pointer) }\n\n")
	b.WriteString("//export SceneryLibVersion\nfunc SceneryLibVersion(unusedInput unsafe.Pointer, unusedInputLen C.size_t, output **C.char, outputLen *C.size_t) C.int32_t { return writeLibraryOutput([]byte(sceneryLibraryVersion), output, outputLen, 0) }\n\n")
	b.WriteString("//export SceneryLibABIHash\nfunc SceneryLibABIHash(unusedInput unsafe.Pointer, unusedInputLen C.size_t, output **C.char, outputLen *C.size_t) C.int32_t { return writeLibraryOutput([]byte(contract.PackageContractABIRevision), output, outputLen, 0) }\n\n")
	for _, operation := range operations {
		name := goName(operation.Name)
		handler, _ := operation.Spec["handler"].(map[string]any)
		symbol := libraryOperationSymbol(operation)
		fmt.Fprintf(&b, "//export %s\nfunc %s(input unsafe.Pointer, inputLen C.size_t, output **C.char, outputLen *C.size_t) C.int32_t {\n", symbol, symbol)
		b.WriteString("\traw := C.GoBytes(input, C.int(inputLen))\n")
		fmt.Fprintf(&b, "\tvalue, err := contract.Unmarshal%sInput(raw); if err != nil { return writeLibraryError(err, output, outputLen) }\n", name)
		fmt.Fprintf(&b, "\toutcome, err := implementation.%s(context.Background(), value); if err != nil { return writeLibraryError(err, output, outputLen) }\n", stringValue(handler["method"]))
		fmt.Fprintf(&b, "\traw, err = contract.Marshal%sOutcome(outcome); if err != nil { return writeLibraryError(err, output, outputLen) }; return writeLibraryOutput(raw, output, outputLen, 0)\n}\n\n", name)
	}
	b.WriteString("func main() {}\n")
	return b.String()
}
