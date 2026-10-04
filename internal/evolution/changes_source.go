package evolution

import (
	stdjson "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

func applySemanticOperation(root string, base *Result, operation SemanticOperation) error {
	if operation.View != "" && operation.View != "source" {
		return fmt.Errorf("expanded resources are read-only")
	}
	if operation.Op == "resource.create" {
		return createResourceBlock(root, base, operation)
	}
	var resource *Resource
	for index := range base.Manifest.Resources {
		if base.Manifest.Resources[index].Address == operation.Address {
			resource = &base.Manifest.Resources[index]
			break
		}
	}
	if resource == nil || resource.Origin.Kind != "authored" {
		return fmt.Errorf("resource %q is not an authored writable resource", operation.Address)
	}
	if err := checkChangePrecondition(*resource, operation); err != nil {
		return err
	}
	switch operation.Op {
	case "resource.rename":
		name, ok := operation.Value.(string)
		if !ok || !validSemanticName(name) {
			return fmt.Errorf("rename requires a valid lower-snake name")
		}
		return renameResource(root, base, *resource, name)
	case "resource.delete":
		for _, edge := range graph.ResourceEdges(base.Manifest.Resources) {
			if edge.To == resource.Address {
				return fmt.Errorf("failed_precondition: %s depends on %s", edge.From, resource.Address)
			}
		}
		return removeResourceBlock(root, base, *resource)
	case "module.configure":
		operation.Op, operation.Path = "value.set", "/spec/inputs"
	case "value.set", "value.unset":
	default:
		return fmt.Errorf("unsupported semantic operation %q", operation.Op)
	}
	return mutateResourceValue(root, base, *resource, operation)
}

func renameResource(root string, base *Result, resource Resource, newName string) error {
	blockType := blockTypeForKind(resource.Kind)
	newAddress := graph.ResourceAddress(resource.Module, blockType, newName)
	for _, existing := range base.Manifest.Resources {
		if existing.Address == newAddress {
			return fmt.Errorf("failed_precondition: resource %s already exists", newAddress)
		}
		if existing.Address != resource.Address && existing.Kind == resource.Kind && existing.Name == resource.Name && existing.Origin.SourceID != "" && existing.Origin.SourceID == resource.Origin.SourceID {
			return fmt.Errorf("failed_precondition: resource source is shared by module instances %s and %s; rename the package declaration explicitly", resource.Address, existing.Address)
		}
	}
	oldTraversal := blockType + "." + resource.Name
	newTraversal := blockType + "." + newName
	targetSource := sourceByID(base.Sources, resource.Origin.SourceID)
	if targetSource == nil {
		return fmt.Errorf("source for %s not found", resource.Address)
	}
	targetDirectory := filepath.ToSlash(filepath.Dir(targetSource.Relative))
	sources := append([]*Source(nil), base.Sources...)
	for _, source := range sources {
		if source.ID == "" || filepath.ToSlash(filepath.Dir(source.Relative)) != targetDirectory {
			continue
		}
		replacements := collectSourceTraversalReplacements(source, oldTraversal, newTraversal)
		if len(replacements) == 0 {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(source.Relative))
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sort.Slice(replacements, func(i, j int) bool {
			return replacements[i].Start.ByteOffset > replacements[j].Start.ByteOffset
		})
		for _, replacement := range replacements {
			rng := replacement.Range
			if rng.Start.ByteOffset < 0 || rng.End.ByteOffset > len(b) {
				return fmt.Errorf("reference range outside source")
			}
			b = append(append(append([]byte(nil), b[:rng.Start.ByteOffset]...), []byte(replacement.Value)...), b[rng.End.ByteOffset:]...)
		}
		if err := atomicWrite(path, b); err != nil {
			return err
		}
	}
	return mutateResourceBlock(root, base, resource, func(body *hclwrite.Body, block *hclwrite.Block) { block.SetLabels([]string{newName}) })
}

func removeResourceBlock(root string, base *Result, resource Resource) error {
	return mutateResourceBlock(root, base, resource, func(body *hclwrite.Body, block *hclwrite.Block) { body.RemoveBlock(block) })
}

func mutateResourceBlock(root string, base *Result, resource Resource, mutation func(*hclwrite.Body, *hclwrite.Block)) error {
	source := sourceByID(base.Sources, resource.Origin.SourceID)
	if source == nil {
		return fmt.Errorf("source for %s not found", resource.Address)
	}
	path := filepath.Join(root, filepath.FromSlash(source.Relative))
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, diagnostics := hclwrite.ParseConfig(b, source.Relative, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return fmt.Errorf("parse writable source: %s", diagnostics.Error())
	}
	block := writableBlock(file.Body(), &resource)
	if block == nil {
		return fmt.Errorf("source block for %s not found", resource.Address)
	}
	mutation(file.Body(), block)
	return atomicWrite(path, hclwrite.Format(file.Bytes()))
}

type traversalReplacement struct {
	Range
	Value string
}

func collectSourceTraversalReplacements(source *Source, oldTraversal, newTraversal string) []traversalReplacement {
	if source == nil || source.File == nil {
		return nil
	}
	body, ok := source.File.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var replacements []traversalReplacement
	var visitBody func(*hclsyntax.Body)
	visitBody = func(current *hclsyntax.Body) {
		for _, attribute := range current.Attributes {
			for _, traversal := range attribute.Expr.Variables() {
				text := scn.TraversalString(traversal)
				if text != oldTraversal && !strings.HasPrefix(text, oldTraversal+".") {
					continue
				}
				positions := source.PositionIndex()
				if positions == nil {
					positions = scn.NewPositionIndex(source.Bytes)
				}
				rng := scn.ConvertRange(source.ID, positions, traversal.SourceRange())
				key := fmt.Sprintf("%d:%d", rng.Start.ByteOffset, rng.End.ByteOffset)
				if seen[key] {
					continue
				}
				seen[key] = true
				replacements = append(replacements, traversalReplacement{Range: rng, Value: newTraversal + strings.TrimPrefix(text, oldTraversal)})
			}
		}
		for _, block := range current.Blocks {
			visitBody(block.Body)
		}
	}
	visitBody(body)
	return replacements
}

func validSemanticName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func checkChangePrecondition(resource Resource, operation SemanticOperation) error {
	if operation.Precondition == nil {
		return nil
	}
	current, exists := compiler.ResourcePointerValue(resource, operation.Path)
	pre := operation.Precondition
	if pre.Exists != nil && *pre.Exists != exists {
		return fmt.Errorf("failed_precondition: exists mismatch")
	}
	if pre.Absent && exists {
		return fmt.Errorf("failed_precondition: value exists")
	}
	if pre.Equals != nil && (!exists || !semanticEqual(current, pre.Equals)) {
		return fmt.Errorf("failed_precondition: value mismatch")
	}
	return nil
}

func changeValue(value any) (cty.Value, error) {
	b, err := stdjson.Marshal(value)
	if err != nil {
		return cty.NilVal, err
	}
	t, err := ctyjson.ImpliedType(b)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(b, t)
}

func writableBlock(body *hclwrite.Body, resource *Resource) *hclwrite.Block {
	blockType := strings.ReplaceAll(strings.TrimPrefix(resource.Kind, "scenery."), "-", "_")
	for _, block := range body.Blocks() {
		labels := block.Labels()
		if block.Type() == blockType && len(labels) == 1 && labels[0] == resource.Name {
			return block
		}
	}
	return nil
}
func sourceByID(sources []*Source, id string) *Source {
	for _, source := range sources {
		if source.ID == id {
			return source
		}
	}
	return nil
}
