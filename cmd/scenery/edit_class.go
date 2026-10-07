package main

import (
	"path/filepath"
	"strings"
)

type devEditClassKey struct{}

func classifyEditPaths(paths []string) string {
	classes := map[string]bool{}
	for _, path := range paths {
		class := "other_source"
		switch filepath.Ext(path) {
		case ".go":
			class = "go_source"
		case ".scn":
			class = "schema_codegen"
		case ".tsx", ".jsx":
			class = "frontend_component"
		case ".css", ".scss":
			class = "styles"
		case ".ts", ".js":
			class = "frontend_module"
		}
		if strings.Contains(filepath.ToSlash(path), "/generated/") {
			class = "generated_client"
		}
		classes[class] = true
	}
	if len(classes) == 0 {
		return "forced"
	}
	if len(classes) > 1 {
		return "mixed"
	}
	for class := range classes {
		return class
	}
	return "unknown"
}
