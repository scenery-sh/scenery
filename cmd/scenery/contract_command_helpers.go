package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
)

func compileContractRoot(value string) (*compiler.Result, error) {
	root, err := findContractRoot(value)
	if err != nil {
		return nil, err
	}
	return compileSourcePlan(context.Background(), root)
}

func checkCompiledContract(root string) (*compiler.Result, error) {
	result, err := compileSourcePlan(context.Background(), root)
	if err != nil || !result.Valid() {
		return result, err
	}
	generate.ApplyImplementationCheck(result)
	return result, nil
}

func writeSourceResult(stdout io.Writer, output string, quiet bool, result *compiler.Result, data any) error {
	if generators, ok := data.(generatorGraphResponse); ok {
		if generators.sqlcPlan != nil {
			if err := generators.sqlcPlan.verifyInputs(result.Root); err != nil {
				return err
			}
		}
		for _, node := range generators.Nodes {
			for _, input := range node.Inputs {
				if input.Kind != "source" || input.Path == "" || input.Digest == "" {
					continue
				}
				contents, err := os.ReadFile(filepath.Join(result.Root, filepath.FromSlash(input.Path)))
				if os.IsNotExist(err) {
					return fmt.Errorf("revision_conflict: generator configuration disappeared before returning the source result; retry the command")
				}
				if err != nil {
					return err
				}
				if sqlcDigest(contents) != input.Digest {
					return fmt.Errorf("revision_conflict: generator configuration changed before returning the source result; retry the command")
				}
			}
		}
	}
	unchanged, err := compiler.SnapshotUnchanged(result)
	if err != nil {
		return err
	}
	if !unchanged {
		return fmt.Errorf("revision_conflict: application changed before returning the source result; retry the command")
	}
	return writeContractResult(stdout, output, quiet, result, data)
}

func writeContractResult(stdout io.Writer, output string, quiet bool, result *compiler.Result, data any) error {
	diagnostics := result.Diagnostics
	if diagnostics == nil {
		diagnostics = []graph.Diagnostic{}
	}
	var implementationRevision any
	if len(result.ImplementationRevisions) > 0 {
		implementationRevision = result.ImplementationRevisions
	}
	if output == "json" {
		env := newCLIEnvelope(result.Valid(), data, diagnostics)
		env.WorkspaceRevision = result.WorkspaceRevision
		env.ImplementationRevision = implementationRevision
		if result.Manifest != nil {
			env.ContractRevision = result.Manifest.ContractRevision
		}
		if err := json.NewEncoder(stdout).Encode(env); err != nil {
			return err
		}
		if !env.OK {
			return &silentCLIError{err: fmt.Errorf("contract compilation failed"), code: contractInvalidExitCode(result)}
		}
		return nil
	}
	if !result.Valid() {
		for _, diag := range result.Diagnostics {
			if diag.Severity == "error" {
				if location := contractDiagnosticLocation(result, diag); location != "" {
					_, _ = fmt.Fprintf(stdout, "%s: %s: %s\n", location, diag.Code, diag.Message)
					continue
				}
				_, _ = fmt.Fprintf(stdout, "%s: %s\n", diag.Code, diag.Message)
			}
		}
		return &codedCLIError{err: fmt.Errorf("contract compilation failed"), code: contractInvalidExitCode(result)}
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintln(stdout, "scenery: contract ok", result.Manifest.ContractRevision)
	return err
}

// contractDiagnosticLocation renders "file:line:column" (one-based, editor
// clickable) for a diagnostic range by resolving its opaque source ID
// through the compiler result's source map — the manifest's when compilation
// reached one, the partial graph's on failure, and the loaded sources as the
// last resort for pure parse failures. Machine JSON keeps the zero-based
// range untouched; this is human output only.
func contractDiagnosticLocation(result *compiler.Result, diag graph.Diagnostic) string {
	if result == nil || diag.Range == nil || strings.TrimSpace(diag.Range.SourceID) == "" {
		return ""
	}
	uri := ""
	if result.Manifest != nil {
		uri = result.Manifest.SourceMap[diag.Range.SourceID].URI
	}
	if uri == "" && result.PartialGraph != nil {
		uri = result.PartialGraph.SourceMap[diag.Range.SourceID].URI
	}
	if uri == "" {
		for _, source := range result.Sources {
			if source != nil && source.ID == diag.Range.SourceID {
				uri = source.Relative
				break
			}
		}
	}
	if strings.TrimSpace(uri) == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", uri, diag.Range.Start.Line+1, diag.Range.Start.Column+1)
}

func contractInvalidExitCode(result *compiler.Result) int {
	if result != nil {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" && strings.HasPrefix(diagnostic.Code, "SCN70") {
				return 4
			}
		}
	}
	return 2
}

func resourceKindMatches(resource graph.Resource, value string) bool {
	return resource.Kind == value || strings.TrimPrefix(resource.Kind, "scenery.") == strings.ReplaceAll(value, "_", "-")
}

func pathExistsLocal(path string) bool { _, err := os.Stat(path); return err == nil }
