package main

import (
	os "os"
	filepath "path/filepath"
	strings "strings"
)

func harnessKnowledgeFiles(root string, relPaths []string) []harnessKnowledgeFile {
	files := make([]harnessKnowledgeFile, 0, len(relPaths))
	for _, rel := range relPaths {
		_, err := os.Stat(filepath.Join(root, rel))
		files = append(files, harnessKnowledgeFile{
			Path:   filepath.ToSlash(rel),
			Exists: err == nil,
		})
	}
	return files
}

func buildHarnessNextActions(steps []harnessStep) []string {
	seen := make(map[string]struct{})
	var actions []string
	for _, step := range steps {
		if step.OK {
			continue
		}
		for _, diag := range step.Diagnostics {
			action := strings.TrimSpace(diag.SuggestedAction)
			if action == "" {
				continue
			}
			if _, ok := seen[action]; ok {
				continue
			}
			seen[action] = struct{}{}
			actions = append(actions, action)
		}
		if step.Error != "" {
			action := "Fix `" + step.Name + "`: " + step.Error
			if _, ok := seen[action]; !ok {
				seen[action] = struct{}{}
				actions = append(actions, action)
			}
		}
	}
	return actions
}
