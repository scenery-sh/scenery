package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type javascriptTestOwner struct {
	Path         string
	Probe        string
	Stage        string
	Prerequisite string
}

// Every conventional JS test has one execution owner; prepared application
// proofs stay separate from service-free conformance and generated overlays.
func javascriptTestOwners() []javascriptTestOwner {
	return []javascriptTestOwner{
		{"internal/generate/testdata/typescript_client_conformance.test.ts", "ui", "client", "tools/typescript dependencies"},
		{"internal/generate/testdata/dev_runtime_client.test.ts", "ui", "client", "tools/typescript dependencies"},
		{"internal/generate/testdata/query_table_perf.test.tsx", "ui", "table", "tools/typescript dependencies"},
		{"internal/generate/testdata/query_table_regressions.test.tsx", "ui", "table", "tools/typescript dependencies"},
		{"internal/build/runtime_identity.test.ts", "ui", "identity", "Bun"},
		{"internal/assistantadapter/eve/testdata/helper-protocol.test.mjs", "assistant-helper", "helper", "generated Eve overlay and Bun"},
		{"internal/compiler/testdata/native/typescript_reference_server.test.ts", "native-contract", "reference-server", "prepared live native application"},
	}
}

func javascriptStageFiles(stage string) []string {
	files := []string{}
	for _, owner := range javascriptTestOwners() {
		if owner.Probe == "ui" && owner.Stage == stage {
			files = append(files, owner.Path)
		}
	}
	return files
}

func conventionalJavascriptTest(path string) bool {
	ext := filepath.Ext(path)
	if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" && ext != ".mjs" && ext != ".cjs" {
		return false
	}
	name := strings.TrimSuffix(filepath.Base(path), ext)
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".spec")
}

func validateJavascriptTestOwners(paths []string) []string {
	owners := map[string]int{}
	for _, owner := range javascriptTestOwners() {
		owners[owner.Path]++
	}
	discovered := map[string]bool{}
	problems := []string{}
	for _, path := range paths {
		if !conventionalJavascriptTest(path) || discovered[path] {
			continue
		}
		discovered[path] = true
		if owners[path] != 1 {
			problems = append(problems, fmt.Sprintf("%s has %d owning lanes; expected exactly one", path, owners[path]))
		}
	}
	for path, count := range owners {
		if !discovered[path] || count != 1 {
			problems = append(problems, "stale or duplicate JS test owner: "+path)
		}
	}
	sort.Strings(problems)
	return problems
}

func runJavascriptTestInventoryStep(ctx context.Context, root string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: "JavaScript test ownership", Command: []string{"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"}}
	output, err := runHarnessGit(ctx, root, step.Command[1:]...)
	paths := []string{}
	if err == nil {
		for _, path := range strings.Split(string(output), "\x00") {
			if path == "" {
				continue
			}
			if _, statErr := os.Lstat(filepath.Join(root, path)); os.IsNotExist(statErr) {
				continue
			} else if statErr != nil {
				err = statErr
				break
			}
			paths = append(paths, path)
		}
	}
	problems := validateJavascriptTestOwners(paths)
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, message := range problems {
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: step.Name, Severity: "error", Message: message, SuggestedAction: "Assign the exact test file to its service-free or prepared-application probe and execute that lane."})
	}
	step.Summary = map[string]any{"owned_files": len(javascriptTestOwners()), "owners": javascriptTestOwners(), "inventory_complete": len(problems) == 0}
	step.OK = len(problems) == 0
	step.DurationMS = time.Since(started).Milliseconds()
	return step
}
