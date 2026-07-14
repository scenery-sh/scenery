package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
)

func readExactPlanFile(path, description string, target any) error {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid_request: decode %s: %w", description, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid_request: decode %s: trailing JSON value", description)
		}
		return fmt.Errorf("invalid_request: decode %s trailing JSON: %w", description, err)
	}
	return nil
}

func readApprovalTokens(paths []string) ([]evolution.ApprovalToken, error) {
	var tokens []evolution.ApprovalToken
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read approval token %s: %w", path, err)
		}
		var token evolution.ApprovalToken
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&token); err != nil {
			return nil, fmt.Errorf("decode approval token %s: %w", path, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("multiple JSON values")
			}
			return nil, fmt.Errorf("decode approval token %s: %w", path, err)
		}
		if err := evolution.ValidateApprovalToken(token); err != nil {
			return nil, fmt.Errorf("decode approval token %s: %w", path, err)
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func approvalVerifierForTokens(root string, tokens []evolution.ApprovalToken) (evolution.ApprovalVerifier, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	verifier, err := evolution.LoadApprovalVerifier(root)
	return evolution.ApprovalVerifier(verifier), err
}

func compileContractRoot(value string) (*compiler.Result, error) {
	root, err := findContractRoot(value)
	if err != nil {
		return nil, err
	}
	return compiler.Compile(root)
}

func checkCompiledContract(root string) (*compiler.Result, error) {
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		return result, err
	}
	generate.ApplyCheck(result, generate.Check(result))
	if result.Valid() {
		if err := generate.SyncEditorWorkspace(result); err != nil {
			return result, err
		}
	}
	return result, nil
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
	var deploymentRevision any
	if len(result.DeploymentRevisions) > 0 {
		deploymentRevision = result.DeploymentRevisions
	}
	if output == "json" {
		env := newCLIEnvelope(result.Valid(), data, diagnostics)
		env.WorkspaceRevision = result.WorkspaceRevision
		env.ImplementationRevision = implementationRevision
		env.DeploymentRevision = deploymentRevision
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
