package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// dotenvAllowedFiles only exclude dotenv files from builds, watches and
// deployed sources; none reads one.
var dotenvAllowedFiles = map[string]bool{
	"internal/build/source.go":       true,
	"internal/watchignore/inputs.go": true,
}

var dotenvSourcePattern = regexp.MustCompile(`"\.env(\.[A-Za-z0-9_.-]*)?"|godotenv|joho/|internal/envfile|LoadDotEnv|DotEnvFiles`)

// dotenvSourceLiterals contains a substring of every dotenvSourcePattern match;
// sources without one skip the comparatively slow regular expression.
var dotenvSourceLiterals = [][]byte{[]byte(`".env`), []byte("godotenv"), []byte("joho/"), []byte("internal/envfile"), []byte("LoadDotEnv"), []byte("DotEnvFiles")}

func dotenvSourceMatch(data []byte) bool {
	return slices.ContainsFunc(dotenvSourceLiterals, func(literal []byte) bool { return bytes.Contains(data, literal) }) &&
		dotenvSourcePattern.Match(data)
}

// appendDotenvReintroductionDiagnostics keeps application configuration out
// of dotenv files: product Go code may not name, parse or load them.
func appendDotenvReintroductionDiagnostics(repoRoot string, diagnostics []checkDiagnostic) []checkDiagnostic {
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && (architectureSkipDir(rel) || rel == "scripts" || rel == "testdata" || rel == "benchmarks") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(rel) != ".go" || strings.HasSuffix(rel, "_test.go") || dotenvAllowedFiles[rel] || isIgnoredHarnessLocalArtifact(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !dotenvSourceMatch(data) {
			return nil
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "contract drift checks",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         "production code names or loads a dotenv file: " + rel,
			SuggestedAction: "Application configuration comes from `scenery config` and the runtime snapshot; declare a deployment-phase package input instead of reading a dotenv file.",
		})
		return nil
	})
	return diagnostics
}
