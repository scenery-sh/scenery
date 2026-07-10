package vnext

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

type FormatResult struct {
	Changed []string `json:"changed"`
}

func Format(root string, check bool) (FormatResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return FormatResult{}, err
	}
	rootPaths, err := sourceFiles(absRoot, true)
	if err != nil {
		return FormatResult{}, err
	}
	paths := append([]string(nil), rootPaths...)
	for _, path := range rootPaths {
		source, diagnostics := parseSource(absRoot, path)
		if hasErrors(diagnostics) || source == nil {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type != "module" {
				continue
			}
			moduleSource, ok := literalString(block, "source")
			if !ok || filepath.IsAbs(moduleSource) {
				continue
			}
			packagePaths, packageErr := sourceFiles(filepath.Join(absRoot, filepath.FromSlash(moduleSource)), false)
			if packageErr == nil {
				paths = append(paths, packagePaths...)
			}
		}
	}
	seen := map[string]bool{}
	result := FormatResult{Changed: []string{}}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		before, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		after := hclwrite.Format(before)
		if string(before) == string(after) {
			continue
		}
		rel, _ := filepath.Rel(absRoot, path)
		result.Changed = append(result.Changed, filepath.ToSlash(rel))
		if check {
			continue
		}
		tmp := path + ".scenery-fmt-tmp"
		if err := os.WriteFile(tmp, after, 0o644); err != nil {
			return result, err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return result, err
		}
	}
	if check && len(result.Changed) > 0 {
		return result, fmt.Errorf("%d Scenery source files require formatting", len(result.Changed))
	}
	return result, nil
}

func CoreSchema(kind string) (map[string]any, bool) {
	kinds := map[string]map[string]any{
		"scenery.operation/v1": {"kind": "scenery.operation/v1", "required": []string{"service", "input", "handler", "result"}},
		"scenery.binding/v1":   {"kind": "scenery.binding/v1", "required": []string{"operation", "execution", "protocol", "delivery", "authentication", "authorization", "pipeline"}},
		"scenery.service/v1":   {"kind": "scenery.service/v1", "required": []string{"runtime", "implementation"}},
		"scenery.record/v1":    {"kind": "scenery.record/v1", "children": map[string]any{"field": "named"}},
		"scenery.execution/v1": {"kind": "scenery.execution/v1", "required": []string{"operation", "mode"}},
	}
	value, ok := kinds[kind]
	return value, ok
}
