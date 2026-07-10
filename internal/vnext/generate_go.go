package vnext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type GenerateResult struct {
	Changed []string `json:"changed"`
	Checked []string `json:"checked"`
}

type generatedFile struct {
	Path  string
	Bytes []byte
}

func GenerateGoContracts(root string, check bool) (GenerateResult, error) {
	result, err := Compile(root)
	if err != nil {
		return GenerateResult{}, err
	}
	if !result.Valid() {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid vNext contract")
	}
	modules := localModules(result.Manifest.Resources)
	generated := GenerateResult{Changed: []string{}, Checked: []string{}}
	for _, module := range modules {
		files, err := generateModuleContract(result, module)
		if err != nil {
			return generated, err
		}
		for _, file := range files {
			rel, _ := filepath.Rel(result.Root, file.Path)
			rel = filepath.ToSlash(rel)
			generated.Checked = append(generated.Checked, rel)
			current, readErr := os.ReadFile(file.Path)
			if readErr == nil && bytes.Equal(current, file.Bytes) {
				continue
			}
			generated.Changed = append(generated.Changed, rel)
			if check {
				continue
			}
			if err := atomicWrite(file.Path, file.Bytes); err != nil {
				return generated, err
			}
		}
	}
	sort.Strings(generated.Changed)
	sort.Strings(generated.Checked)
	if check && len(generated.Changed) > 0 {
		return generated, fmt.Errorf("generated vNext contracts are stale: %s", strings.Join(generated.Changed, ", "))
	}
	return generated, nil
}

func localModules(resources []Resource) []Resource {
	var modules []Resource
	for _, r := range resources {
		if r.Kind == "scenery.module/v1" {
			if source, ok := r.Spec["source"].(string); ok && !strings.Contains(source, "://") {
				modules = append(modules, r)
			}
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	return modules
}

func generateModuleContract(result *Result, module Resource) ([]generatedFile, error) {
	source, _ := module.Spec["source"].(string)
	dir := filepath.Join(result.Root, filepath.FromSlash(source))
	contractDir := filepath.Join(dir, "scenerycontract")
	resources := moduleResources(result.Manifest.Resources, module.Name)
	packageBlock := findPackageBlock(result.Sources, source)
	importPath := ""
	if packageBlock != nil {
		for _, child := range packageBlock.Blocks {
			if child.Type == "go_contract" {
				importPath, _ = literalString(child, "import_path")
			}
		}
	}
	if importPath == "" {
		return nil, fmt.Errorf("module %s package has no go_contract.import_path", module.Name)
	}
	abiRevision, err := packageABIRevision(importPath, resources)
	if err != nil {
		return nil, err
	}
	typesSource := renderContractTypes(resources)
	contractSource := renderContractAPI(module.Name, importPath, abiRevision, resources)
	formattedTypes, err := format.Source([]byte(typesSource))
	if err != nil {
		return nil, fmt.Errorf("format %s types: %w\n%s", module.Name, err, typesSource)
	}
	formattedContract, err := format.Source([]byte(contractSource))
	if err != nil {
		return nil, fmt.Errorf("format %s contract: %w\n%s", module.Name, err, contractSource)
	}
	artifactFiles := []generatedFile{{filepath.Join(contractDir, "types.gen.go"), formattedTypes}, {filepath.Join(contractDir, "contract.gen.go"), formattedContract}}
	descriptor := map[string]any{"api_version": "scenery.package-generated.v1", "package": module.Name, "import_path": importPath + "/scenerycontract", "package_contract_abi_revision": abiRevision, "content_digest": artifactDigest(contractDir, artifactFiles), "generator": "scenery.vnext.go/v1", "covered": resourceAddresses(resources)}
	descriptorBytes, _ := json.MarshalIndent(descriptor, "", "  ")
	descriptorBytes = append(descriptorBytes, '\n')
	return append(artifactFiles, generatedFile{filepath.Join(contractDir, "scenery.package-generated.v1.json"), descriptorBytes}), nil
}

func moduleResources(resources []Resource, module string) []Resource {
	var out []Resource
	for _, r := range resources {
		if r.Module == module {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}
func resourceAddresses(resources []Resource) []string {
	out := make([]string, len(resources))
	for i, r := range resources {
		out[i] = r.Address
	}
	return out
}

func findPackageBlock(sources []*Source, moduleSource string) *Block {
	prefix := strings.TrimPrefix(filepath.ToSlash(moduleSource), "./") + "/"
	for _, source := range sources {
		if !strings.HasPrefix(source.Relative, prefix) {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "package" {
				return block
			}
		}
	}
	return nil
}

func packageABIRevision(importPath string, resources []Resource) (string, error) {
	projection := map[string]any{"import_path": importPath + "/scenerycontract", "profile": "scenery.go-implementation/v1", "resources": resources}
	b, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("scenery.package-contract-abi-revision.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func renderContractTypes(resources []Resource) string {
	var b strings.Builder
	b.WriteString("// Code generated by Scenery vNext. DO NOT EDIT.\npackage scenerycontract\n\nimport scenery \"scenery.sh\"\n\nvar _ = scenery.Problem{}\n\n")
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record/v1":
			renderRecord(&b, resource)
		case "scenery.enum/v1":
			renderEnum(&b, resource)
		}
	}
	return b.String()
}

func renderRecord(b *strings.Builder, resource Resource) {
	fmt.Fprintf(b, "type %s struct {\n", goName(resource.Name))
	for _, field := range namedChildren(resource.Spec, "field") {
		name, _ := field["name"].(string)
		typeValue := field["type"]
		fmt.Fprintf(b, "\t%s %s `json:\"%s%s\"`\n", goName(name), goType(typeValue), wireName(field, name), optionalJSONSuffix(typeValue))
	}
	b.WriteString("}\n\n")
}
func renderEnum(b *strings.Builder, resource Resource) {
	name := goName(resource.Name)
	fmt.Fprintf(b, "type %s string\n\nconst (\n", name)
	for _, value := range namedChildren(resource.Spec, "value") {
		label, _ := value["name"].(string)
		wire := label
		if explicit, ok := value["wire_value"].(string); ok {
			wire = explicit
		}
		fmt.Fprintf(b, "\t%s%s %s = %q\n", name, goName(label), name, wire)
	}
	b.WriteString(")\n\n")
}

func renderContractAPI(moduleName, importPath, abi string, resources []Resource) string {
	var b strings.Builder
	b.WriteString("// Code generated by Scenery vNext. DO NOT EDIT.\npackage scenerycontract\n\nimport (\n\t\"context\"\n\tscenery \"scenery.sh\"\n)\n\n")
	fmt.Fprintf(&b, "const PackageContractABIRevision = %q\nconst PackageImportPath = %q\n\n", abi, importPath)
	for _, service := range resources {
		if service.Kind != "scenery.service/v1" {
			continue
		}
		serviceName := goName(service.Name)
		fmt.Fprintf(&b, "type %sDependencies struct {\n", serviceName)
		for _, dep := range namedChildren(service.Spec, "dependency") {
			name, _ := dep["name"].(string)
			fmt.Fprintf(&b, "\t%s any\n", goName(name))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "type %sConfig struct{}\ntype %sClients struct{}\ntype %sConstructorInput struct {\n\tDependencies %sDependencies\n\tConfig %sConfig\n\tClients %sClients\n}\n\n", serviceName, serviceName, serviceName, serviceName, serviceName, serviceName)
	}
	for _, op := range resources {
		if op.Kind != "scenery.operation/v1" {
			continue
		}
		renderOperationAPI(&b, op)
	}
	b.WriteString("type InternalClient interface {\n\tInvoke(context.Context, scenery.Invocation, any) (any, error)\n}\n")
	return b.String()
}

func renderOperationAPI(b *strings.Builder, op Resource) {
	name := goName(op.Name)
	inputType := goType(op.Spec["input"])
	if inputType != name+"Input" {
		fmt.Fprintf(b, "type %sInput = %s\n\n", name, inputType)
	}
	fmt.Fprintf(b, "type %sOutcome interface { is%sOutcome() }\n\n", name, name)
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(op.Spec, kind) {
			variantName, _ := variant["name"].(string)
			wrapper := name + goName(variantName)
			valueType := goType(variant["type"])
			field := "Value"
			if kind == "error" {
				field = "Problem"
			}
			fmt.Fprintf(b, "type %s struct { %s %s }\nfunc (%s) is%sOutcome() {}\n\n", wrapper, field, valueType, wrapper, name)
		}
	}
}

func namedChildren(spec map[string]any, name string) []map[string]any {
	value, ok := spec[name]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if child, ok := item.(map[string]any); ok {
				out = append(out, child)
			}
		}
		sort.Slice(out, func(i, j int) bool { a, _ := out[i]["name"].(string); b, _ := out[j]["name"].(string); return a < b })
		return out
	}
	return nil
}

func goType(value any) string {
	if ref := refString(value); ref != "" {
		parts := strings.Split(ref, ".")
		if len(parts) >= 2 {
			switch parts[0] {
			case "record", "enum", "union":
				return goName(parts[1])
			case "std":
				if strings.HasSuffix(ref, ".problem") {
					return "scenery.Problem"
				}
			}
		}
		switch ref {
		case "string":
			return "string"
		case "bool":
			return "bool"
		case "int":
			return "scenery.Int"
		case "int32":
			return "int32"
		case "int64":
			return "int64"
		case "uint32":
			return "uint32"
		case "uint64":
			return "uint64"
		case "decimal":
			return "scenery.Decimal"
		case "float32":
			return "float32"
		case "float64":
			return "float64"
		case "bytes":
			return "[]byte"
		case "uuid":
			return "scenery.UUID"
		case "date":
			return "scenery.Date"
		case "datetime":
			return "scenery.DateTime"
		case "duration":
			return "scenery.Duration"
		case "size":
			return "scenery.Size"
		case "url":
			return "scenery.URL"
		case "relative_path":
			return "scenery.RelativePath"
		case "json":
			return "scenery.JSON"
		}
	}
	if expr, ok := value.(map[string]any); ok {
		if raw, ok := expr["$expression"].(string); ok {
			return goTypeExpression(raw)
		}
	}
	return "any"
}
func goTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, wrapper := range []struct{ prefix, goPrefix string }{{"optional(", "scenery.Optional["}, {"nullable(", "scenery.Nullable["}, {"list(", "[]"}, {"set(", "scenery.Set["}, {"map(", "map[string]"}} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			inner := goTypeExpression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")"))
			if strings.HasSuffix(wrapper.goPrefix, "[") {
				return wrapper.goPrefix + inner + "]"
			}
			return wrapper.goPrefix + inner
		}
	}
	return goType(map[string]any{"$ref": raw})
}
func optionalJSONSuffix(value any) string {
	if expr, ok := value.(map[string]any); ok {
		if raw, ok := expr["$expression"].(string); ok && strings.HasPrefix(strings.TrimSpace(raw), "optional(") {
			return ",omitempty"
		}
	}
	return ""
}
func wireName(field map[string]any, fallback string) string {
	if value, ok := field["wire_name"].(string); ok {
		return value
	}
	return fallback
}
func goName(value string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' }) {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func artifactDigest(root string, files []generatedFile) string {
	sorted := append([]generatedFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		a, _ := filepath.Rel(root, sorted[i].Path)
		b, _ := filepath.Rel(root, sorted[j].Path)
		return filepath.ToSlash(a) < filepath.ToSlash(b)
	})
	h := sha256.New()
	for _, file := range sorted {
		rel, _ := filepath.Rel(root, file.Path)
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(file.Bytes)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
