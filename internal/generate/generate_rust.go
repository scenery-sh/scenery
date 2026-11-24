package generate

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
	sdksource "scenery.sh/sdk/rust"
)

const rustSDKRelativeDirectory = ".scenery/gen/rust-sdk"

type rustSourceModule struct {
	resource Resource
	crate    string
	root     string
	services []Resource
}

func rustSourceModules(result *compiler.Result) []rustSourceModule {
	idx := newResourceIndex(result.Manifest.Resources)
	var modules []rustSourceModule
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.module" {
			continue
		}
		metadata, _ := resource.Spec["package"].(map[string]any)
		contract, _ := metadata["rust_contract"].(map[string]any)
		crate := stringValue(contract["crate"])
		if crate == "" {
			continue
		}
		module := rustSourceModule{resource: resource, crate: crate, root: stringValue(resource.Spec["workspace_package_root"])}
		for _, candidate := range idx.moduleResources(moduleInstancePath(resource)) {
			if candidate.Kind == "scenery.service" && stringValue(candidate.Spec["runtime"]) == "rust" {
				module.services = append(module.services, candidate)
			}
		}
		if len(module.services) > 0 {
			modules = append(modules, module)
		}
	}
	return modules
}

func renderRustContractFiles(result *compiler.Result, modules []rustSourceModule) ([]generatedFile, error) {
	if len(modules) == 0 {
		return nil, nil
	}
	idx := newResourceIndex(result.Manifest.Resources)
	var generated []generatedFile
	sdk := filepath.Join(result.Root, rustSDKRelativeDirectory)
	if err := fs.WalkDir(sdksource.SDK, "scenery-sdk", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := sdksource.SDK.ReadFile(path)
		if err == nil {
			generated = append(generated, generatedFile{Path: filepath.Join(sdk, filepath.FromSlash(strings.TrimPrefix(path, "scenery-sdk/"))), Bytes: data})
		}
		return err
	}); err != nil {
		return nil, err
	}
	for _, module := range modules {
		dir := filepath.Join(result.Root, filepath.FromSlash(module.root), "scenerycontract")
		if !rustContractManaged(result, dir) {
			return nil, fmt.Errorf("Rust contract output %s is outside managed_generated_roots", dir)
		}
		sdkPath, err := filepath.Rel(dir, sdk)
		if err != nil {
			return nil, err
		}
		manifest := fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\n\n[dependencies]\nscenery-sdk = { path = %q }\n", module.crate+"-contract", filepath.ToSlash(sdkPath))
		source, err := renderRustContract(module, idx)
		if err != nil {
			return nil, err
		}
		generated = append(generated, generatedFile{Path: filepath.Join(dir, "Cargo.toml"), Bytes: []byte(manifest)}, generatedFile{Path: filepath.Join(dir, "src", "lib.rs"), Bytes: []byte(source)})
		for _, service := range module.services {
			worker := rustWorkerSource(module, service)
			generated = append(generated, generatedFile{Path: filepath.Join(dir, service.Name+"_worker.rs"), Bytes: []byte(worker)})
		}
	}
	return generated, nil
}

func rustContractManaged(result *compiler.Result, dir string) bool {
	for _, source := range result.Sources {
		if source.Relative != "app.scn" {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type != "workspace" {
				continue
			}
			for _, path := range literalStringList(block, "managed_generated_roots") {
				root := filepath.Join(result.Root, filepath.FromSlash(path))
				if relative, err := filepath.Rel(root, dir); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return true
				}
			}
		}
	}
	return false
}

type rustContractProjection struct {
	module string
	types  map[string]Resource
}

func renderRustContract(module rustSourceModule, idx *resourceIndex) (string, error) {
	modulePath := moduleInstancePath(module.resource)
	projection := rustContractProjection{module: modulePath, types: make(map[string]Resource)}
	services := make(map[string]bool)
	for _, service := range module.services {
		services[service.Address] = true
	}
	operations := make(map[string][]Resource)
	types := make(map[string]bool)
	collect := func(resource Resource) {
		for _, value := range contractTypeValues([]Resource{resource}) {
			collectABITypeReferences(value, resource.Module, idx.byAddress, types)
		}
	}
	for _, operation := range idx.moduleResources(modulePath) {
		if operation.Kind != "scenery.operation" {
			continue
		}
		service := resolveResourceRef(operation, refString(operation.Spec["service"]), "service")
		if services[service] {
			operations[service] = append(operations[service], operation)
			collect(operation)
		}
	}
	clients := make(map[string][]goClientBinding)
	for _, service := range module.services {
		var err error
		clients[service.Address], err = serviceGoClients(idx, service)
		if err != nil {
			return "", err
		}
		for _, client := range clients[service.Address] {
			collect(client.Operation)
		}
	}
	for changed := true; changed; {
		before := len(types)
		for address := range types {
			collect(idx.byAddress[address])
		}
		changed = len(types) != before
	}
	var declarations []string
	for address := range types {
		projection.types[address] = idx.byAddress[address]
		declarations = append(declarations, address)
	}
	slices.Sort(declarations)
	var source strings.Builder
	source.WriteString("// Generated by Scenery. DO NOT EDIT.\npub use scenery_sdk as sdk;\nuse sdk::serde;\nuse sdk::serde::{Deserialize, Serialize};\nuse sdk::serde_json::{self, value::RawValue};\n\n")
	for _, address := range declarations {
		if err := projection.renderType(&source, projection.types[address]); err != nil {
			return "", err
		}
	}
	for _, service := range module.services {
		if err := projection.renderService(&source, service, operations[service.Address], clients[service.Address], resourceValues(idx.byAddress)); err != nil {
			return "", err
		}
	}
	return source.String(), nil
}

func (p rustContractProjection) typeName(resource Resource) string {
	name := goName(resource.Name)
	if resource.Module != p.module {
		return goName(strings.ReplaceAll(resource.Module, "/", "_")) + name
	}
	return name
}

func (p rustContractProjection) rustType(value any, module string) string {
	expression := qualifyProjectedGoExpression(typeExpression(value), module)
	return p.rustExpression(expression)
}

func (p rustContractProjection) rustExpression(expression string) string {
	if resource, ok := p.types[expression]; ok {
		return p.typeName(resource)
	}
	if open := strings.IndexByte(expression, '('); open >= 0 && strings.HasSuffix(expression, ")") {
		arguments := splitTypeArguments(expression[open+1 : len(expression)-1])
		for i, argument := range arguments {
			arguments[i] = p.rustExpression(argument)
		}
		switch expression[:open] {
		case "optional", "nullable":
			return "Option<" + arguments[0] + ">"
		case "list", "set":
			return "Vec<" + arguments[0] + ">"
		case "map":
			return "std::collections::BTreeMap<String, " + arguments[0] + ">"
		case "tuple":
			return "(" + strings.Join(arguments, ", ") + ",)"
		}
	}
	switch expression {
	case "bool":
		return "bool"
	case "string":
		return "String"
	case "int32":
		return "i32"
	case "uint32":
		return "u32"
	case "int64":
		return "sdk::Int64"
	case "uint64":
		return "sdk::UInt64"
	case "float32":
		return "f32"
	case "float64":
		return "f64"
	case "decimal":
		return "sdk::Decimal"
	case "timestamp":
		return "sdk::Timestamp"
	case "duration":
		return "sdk::Duration"
	case "bytes":
		return "sdk::ContractBytes"
	case "std.type.unit":
		return "sdk::Unit"
	default:
		return "Box<RawValue>"
	}
}

func rustIdent(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	switch name {
	case "as", "break", "const", "continue", "else", "enum", "extern", "false", "fn", "for", "gen", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while", "async", "await", "dyn", "try":
		return "r#" + name
	case "self", "Self", "crate":
		return name + "_"
	default:
		return name
	}
}

func rustMethodName(name string) string {
	var result strings.Builder
	for index, character := range name {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				result.WriteByte('_')
			}
			character += 'a' - 'A'
		}
		result.WriteRune(character)
	}
	return rustIdent(result.String())
}
