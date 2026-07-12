package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/vnext"
)

func readExactVNextPlanFile(path, description string, target any) error {
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

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func readApprovalTokens(paths []string) ([]vnext.ApprovalToken, error) {
	var tokens []vnext.ApprovalToken
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read approval token %s: %w", path, err)
		}
		var token vnext.ApprovalToken
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
		if err := vnext.ValidateApprovalToken(token); err != nil {
			return nil, fmt.Errorf("decode approval token %s: %w", path, err)
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func approvalVerifierForTokens(root string, tokens []vnext.ApprovalToken) (vnext.ApprovalVerifier, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	return vnext.LoadApprovalVerifier(root)
}

func compileVNextRoot(value string) (*vnext.Result, error) {
	root, err := vnextRoot(value)
	if err != nil {
		return nil, err
	}
	return vnext.Compile(root)
}

func writeVNextResult(stdout io.Writer, output string, quiet bool, result *vnext.Result, data any) error {
	diagnostics := result.Diagnostics
	if diagnostics == nil {
		diagnostics = []vnext.Diagnostic{}
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
		env := vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: result.Valid(), WorkspaceRevision: result.WorkspaceRevision, ImplementationRevision: implementationRevision, DeploymentRevision: deploymentRevision, Data: data, Diagnostics: diagnostics}
		if result.Manifest != nil {
			env.ContractRevision = result.Manifest.ContractRevision
		}
		if err := json.NewEncoder(stdout).Encode(env); err != nil {
			return err
		}
		if !env.OK {
			return &silentCLIError{err: fmt.Errorf("vNext compilation failed"), code: vnextInvalidExitCode(result)}
		}
		return nil
	}
	if !result.Valid() {
		for _, diag := range result.Diagnostics {
			if diag.Severity == "error" {
				_, _ = fmt.Fprintf(stdout, "%s: %s\n", diag.Code, diag.Message)
			}
		}
		return &codedCLIError{err: fmt.Errorf("vNext compilation failed"), code: vnextInvalidExitCode(result)}
	}
	if quiet {
		return nil
	}
	_, err := fmt.Fprintln(stdout, "scenery: vNext contract ok", result.Manifest.ContractRevision)
	return err
}

func vnextInvalidExitCode(result *vnext.Result) int {
	if result != nil {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" && (strings.Contains(diagnostic.Message, "unsupported_profile") || strings.HasPrefix(diagnostic.Code, "SCN70")) {
				return 4
			}
		}
	}
	return 2
}

func resourceKindMatches(resource vnext.Resource, value string) bool {
	return resource.Kind == value || strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery.") == strings.ReplaceAll(value, "_", "-")
}

func pathExistsLocal(path string) bool { _, err := os.Stat(path); return err == nil }

func hasCLIArg(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func isVNextGenerate(args []string) bool {
	root := "."
	for i, arg := range args {
		if arg == "--app-root" && i+1 < len(args) {
			root = args[i+1]
		}
		if strings.HasPrefix(arg, "--app-root=") {
			root = strings.TrimPrefix(arg, "--app-root=")
		}
	}
	if positional := firstGeneratePositional(args); positional == "sqlc" {
		return false
	}
	_, err := vnextRoot(root)
	return err == nil
}

func firstGeneratePositional(args []string) string {
	expectsValue := false
	for _, arg := range args {
		if expectsValue {
			expectsValue = false
			continue
		}
		if arg == "--app-root" || arg == "--target" || arg == "--lang" || arg == "--output" || arg == "-o" {
			expectsValue = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}
