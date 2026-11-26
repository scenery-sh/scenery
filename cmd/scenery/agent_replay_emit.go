package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/harnessevidence"
	"scenery.sh/internal/postgresname"
)

func emitReplayRegressionTest(ctx context.Context, out io.Writer, root string, report failureReport, opts agentReplayOptions) error {
	started := time.Now()
	inv := report.Invocation
	g, err := loadAgentGraph(ctx, root)
	if err != nil {
		return err
	}
	modules, err := g.modules()
	if err != nil {
		return err
	}
	var targetMod *agentModule
	for i := range modules {
		if modules[i].Name == inv.Module {
			targetMod = &modules[i]
			break
		}
	}
	if targetMod == nil {
		return preconditionErrorf("module %q from report %s is not declared in %s", inv.Module, report.ReportToken, root)
	}

	serviceRes, ok := g.resources[inv.Service]
	if !ok || serviceRes.Kind != "scenery.service" {
		return preconditionErrorf("service %q from report %s is not declared", inv.Service, report.ReportToken)
	}
	opRes, ok := g.resources[inv.Operation]
	if !ok || opRes.Kind != "scenery.operation" {
		return preconditionErrorf("operation %q from report %s is not declared", inv.Operation, report.ReportToken)
	}

	shortToken := strings.TrimPrefix(report.ReportToken, "rpt_")
	if len(shortToken) > 12 {
		shortToken = shortToken[:12]
	}

	defaultRel, content, err := renderReplayTestFile(g, *targetMod, serviceRes, opRes, report, shortToken)
	if err != nil {
		return err
	}

	destPath := filepath.Join(root, filepath.FromSlash(defaultRel))
	if opts.outputPath != "" {
		if filepath.IsAbs(opts.outputPath) {
			destPath = opts.outputPath
		} else {
			destPath = filepath.Join(root, opts.outputPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(destPath, []byte(content), 0o644); err != nil {
		return err
	}

	displayPath := destPath
	if rel, relErr := filepath.Rel(root, destPath); relErr == nil && filepath.IsLocal(rel) {
		displayPath = filepath.ToSlash(rel)
	}

	res := agentReplayResult{
		cliPayloadIdentity: newCLIPayloadIdentity(replayResultKind),
		OK:                 true,
		Mode:               "emit_test",
		ReportToken:        report.ReportToken,
		Status:             "emitted",
		OriginalCause:      report.Cause,
		Invocation:         inv,
		DurationMS:         time.Since(started).Milliseconds(),
		EmittedTestPath:    displayPath,
		NextCommand:        harnessevidence.ReproCommand([]string{"scenery", "test", targetMod.Name}, root),
	}
	return printAgentReplay(out, opts.jsonOutput, res)
}

func renderReplayTestFile(
	g agentGraph,
	mod agentModule,
	service graph.Resource,
	operation graph.Resource,
	report failureReport,
	shortToken string,
) (string, string, error) {
	inv := report.Invocation
	rawInput := strings.TrimSpace(string(inv.Input))
	if rawInput == "" || !json.Valid([]byte(rawInput)) {
		rawInput = "{}"
	}
	impl, _ := service.Spec["implementation"].(map[string]any)
	constructor := cp.StringValue(impl["constructor"])
	handler, _ := operation.Spec["handler"].(map[string]any)
	method := cp.StringValue(handler["method"])
	opGoName := cp.GoName(operation.Name)
	svcGoName := cp.GoName(service.Name)

	var pkgSpec map[string]any
	for _, r := range g.Manifest.Resources {
		if r.Kind == "scenery.module" && cp.ModuleInstancePath(r) == mod.Name {
			pkgSpec, _ = r.Spec["package"].(map[string]any)
			break
		}
	}

	sqlDeps := serviceSQLDependencies(g, service)
	hasPublishers := len(cp.NamedChildren(service.Spec, "publisher")) > 0

	switch mod.Runtime {
	case "python":
		pyContract, _ := pkgSpec["python_contract"].(map[string]any)
		entry := firstNonEmpty(cp.StringValue(pyContract["entry"]), "service.py")
		entryMod := strings.TrimSuffix(filepath.ToSlash(entry), ".py")
		entryMod = strings.ReplaceAll(entryMod, "/", ".")
		principalPy := "None"
		if inv.Principal != "" {
			principalPy = strconv.Quote(inv.Principal)
		}
		tenantPy := "None"
		if inv.TenantID != "" {
			tenantPy = strconv.Quote(inv.TenantID)
		}
		modePy := "direct"
		if inv.Mode == "durable" {
			modePy = "durable"
		}
		bindingPy := "None"
		if inv.Binding != "" {
			bindingPy = strconv.Quote(inv.Binding)
		}
		osImport := ""
		if len(sqlDeps) > 0 {
			osImport = "import os\n"
		}
		rel := filepath.ToSlash(filepath.Join(mod.Directory, "test_replay_"+shortToken+".py"))
		src := fmt.Sprintf(`# Regression test generated from %s (%s).
from __future__ import annotations

%simport unittest

from scenerycontract import ConstructorInput, Context, clients, dispatch
from scenerycontract.sdk.codec import parse_exact_json
from scenerycontract.sdk.worker import maybe_await
from %s import %s


class Replay%sTests(unittest.IsolatedAsyncioTestCase):
    async def test_replay_%s(self) -> None:
        raw_input = parse_exact_json(%s)
        service = await maybe_await(%s(ConstructorInput(clients=clients, config=None, databases=%s)))
        ctx = Context.standalone(principal=%s, tenant_id=%s, invocation_id=%q, mode=%q, binding=%s)
        outcome_json, _ = await dispatch(service, %q, raw_input, ctx)
        self.assertTrue(outcome_json)


if __name__ == "__main__":
    unittest.main()
`, report.ReportToken, inv.Operation, osImport, entryMod, constructor, strings.ToUpper(shortToken), shortToken,
			strconv.Quote(rawInput), constructor, renderPythonTestDatabases(sqlDeps), principalPy, tenantPy, report.ReportToken, modePy, bindingPy, operation.Address)
		return rel, src, nil

	case "ts", "js":
		jsContract, _ := pkgSpec["js_contract"].(map[string]any)
		entry := firstNonEmpty(cp.StringValue(jsContract["entry"]), "service."+mod.Runtime)
		ext := filepath.Ext(entry)
		entryImport := "./" + strings.TrimSuffix(filepath.ToSlash(entry), ext) + ".js"
		contractImports := "clients, dispatch, standaloneContext"
		ctorFields := "clients, config: undefined, databases: " + renderJSTestDatabases(sqlDeps)
		if hasPublishers {
			contractImports = "clients, dispatch, publishers, standaloneContext"
			ctorFields = "clients, publishers, config: undefined, databases: " + renderJSTestDatabases(sqlDeps)
		}
		modeJS := "direct"
		if inv.Mode == "durable" {
			modeJS = "durable"
		}
		rel := filepath.ToSlash(filepath.Join(mod.Directory, "replay_"+shortToken+".test."+mod.Runtime))
		src := fmt.Sprintf(`// Regression test generated from %s (%s).
import { expect, test } from "bun:test";
import { %s } from "./scenerycontract/index.js";
import { parseExactJSON } from "./scenerycontract/codec.js";
import { %s } from %q;

test("replay %s (%s)", async () => {
  const rawInput = parseExactJSON(%s);
  const service = await %s({ %s });
  const ctx = standaloneContext({
    invocationId: %q,
    principal: %s,
    tenantId: %s,
    mode: %q,
    binding: %s,
  });
  const result = await dispatch(service, %q, rawInput, ctx);
  expect(result.outcome).toBeDefined();
});
`, report.ReportToken, inv.Operation, contractImports, constructor, entryImport, report.ReportToken, inv.Operation,
			strconv.Quote(rawInput), constructor, ctorFields, report.ReportToken,
			jsOptionalLiteral(inv.Principal), jsOptionalLiteral(inv.TenantID), modeJS, jsOptionalLiteral(inv.Binding), operation.Address)
		return rel, src, nil

	case "rust":
		rustContract, _ := pkgSpec["rust_contract"].(map[string]any)
		crateName := strings.ReplaceAll(firstNonEmpty(cp.StringValue(rustContract["crate"]), mod.Name), "-", "_")
		ctorRust := rustSnakeName(constructor)
		methodRust := rustSnakeName(method)
		principalRust := "None"
		if inv.Principal != "" {
			principalRust = fmt.Sprintf("Some(%q)", inv.Principal)
		}
		tenantRust := "None"
		if inv.TenantID != "" {
			tenantRust = fmt.Sprintf("Some(%q)", inv.TenantID)
		}
		rel := filepath.ToSlash(filepath.Join(mod.Directory, "tests", "replay_"+shortToken+".rs"))
		src := fmt.Sprintf(`// Regression test generated from %s (%s).
use scenerycontract::{%sConstructorInput, %sService, %sInput, sdk};

#[test]
fn replay_%s() -> sdk::Result<()> {
    sdk::run(async {
        let service = %s::%s(%sConstructorInput::default())?;
        let input: %sInput = sdk::serde_json::from_str(%s)?;
        let ctx = sdk::Context::with_principal(%s, %s);
        let _outcome = %sService::%s(&service, ctx, input).await?;
        Ok(())
    })
}
`, report.ReportToken, inv.Operation, svcGoName, svcGoName, opGoName,
			shortToken, crateName, ctorRust, svcGoName, opGoName, strconv.Quote(rawInput),
			principalRust, tenantRust, svcGoName, methodRust)
		return rel, src, nil

	case "go":
		importPath, pkgName := resolveGoModuleImportAndPackage(g, mod, pkgSpec)
		streaming := operationHasStreamingBinding(g.Manifest.Resources, operation.Address)
		callAssign := "outcome, err :="
		checkOutcome := "if outcome == nil {\n\t\tt.Fatal(\"expected non-nil outcome\")\n\t}"
		if streaming {
			callAssign = "outcome, _, err :="
		}
		execID := ""
		if inv.Mode == "durable" {
			execID = report.ReportToken
		}
		rel := filepath.ToSlash(filepath.Join(mod.Directory, "replay_"+shortToken+"_test.go"))
		src := fmt.Sprintf(`// Regression test generated from %s (%s).
package %s

import (
	"encoding/json"
	"testing"

	"scenery.sh"
	contract %q
)

func TestReplay_%s(t *testing.T) {
	var input contract.%sInput
	if err := json.Unmarshal([]byte(%s), &input); err != nil {
		t.Fatalf("decode captured replay input: %%v", err)
	}
	ctx := scenery.WithStandaloneInvocation(t.Context(), scenery.StandaloneInvocation{
		ID:          %q,
		Principal:   %q,
		TenantID:    %q,
		ExecutionID: %q,
		Binding:     %q,
	})
	service, err := %s(ctx, contract.%sConstructorInput{})
	if err != nil {
		t.Fatalf("construct service: %%v", err)
	}
	%s service.%s(ctx, input)
	if err != nil {
		t.Fatalf("replay failed: %%v", err)
	}
	%s
}
`, report.ReportToken, inv.Operation, pkgName, importPath+"/scenerycontract",
			strings.ToUpper(shortToken), opGoName, strconv.Quote(rawInput),
			report.ReportToken, inv.Principal, inv.TenantID, execID, inv.Binding,
			constructor, svcGoName, callAssign, method, checkOutcome)
		return rel, src, nil

	default:
		return "", "", fmt.Errorf("unsupported runtime %q for --emit-test", mod.Runtime)
	}
}

func resolveGoModuleImportAndPackage(g agentGraph, mod agentModule, pkgSpec map[string]any) (string, string) {
	goContract, _ := pkgSpec["go_contract"].(map[string]any)
	importPath := cp.StringValue(goContract["import_path"])
	if importPath == "" {
		for _, r := range g.Manifest.Resources {
			if r.Kind == "scenery.go-module" && filepath.Clean(cp.StringValue(r.Spec["root"])) == "." {
				base := strings.TrimSuffix(cp.StringValue(r.Spec["import_path"]), "/")
				if mod.Directory == "." {
					importPath = base
				} else {
					importPath = base + "/" + filepath.ToSlash(mod.Directory)
				}
				break
			}
		}
	}
	pkgName := filepath.Base(mod.Directory)
	if pkgName == "." || pkgName == "/" {
		pkgName = mod.Name
	}
	entries, err := os.ReadDir(filepath.Join(g.Root, mod.Directory))
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				if data, readErr := os.ReadFile(filepath.Join(g.Root, mod.Directory, name)); readErr == nil {
					for line := range strings.SplitSeq(string(data), "\n") {
						line = strings.TrimSpace(line)
						if rest, ok := strings.CutPrefix(line, "package "); ok {
							fields := strings.Fields(rest)
							if len(fields) > 0 {
								return importPath, fields[0]
							}
						}
					}
				}
			}
		}
	}
	return importPath, pkgName
}

func operationHasStreamingBinding(resources []graph.Resource, operationAddress string) bool {
	for _, r := range resources {
		if r.Kind != "scenery.binding" {
			continue
		}
		if ref, _ := r.Spec["operation"].(map[string]any); cp.StringValue(ref["$ref"]) != operationAddress && cp.StringValue(r.Spec["operation"]) != operationAddress {
			continue
		}
		if cp.StringValue(r.Spec["delivery"]) == "stream_out" {
			return true
		}
		httpSpec, _ := r.Spec["http"].(map[string]any)
		for _, resp := range cp.NamedChildren(httpSpec, "response") {
			body, _ := resp["body"].(map[string]any)
			if cp.StringValue(body["codec"]) == "binary_stream" || cp.StringValue(body["delivery"]) == "stream" {
				return true
			}
		}
	}
	return false
}

type sqlDependencyEntry struct {
	name   string
	schema string
}

func serviceSQLDependencies(g agentGraph, service graph.Resource) []sqlDependencyEntry {
	requires, err := compiler.ServiceGoDependencies(g.resources, service)
	if err != nil {
		return nil
	}
	var out []sqlDependencyEntry
	for _, dep := range requires {
		if dep.Resolver == "sql" {
			out = append(out, sqlDependencyEntry{name: dep.Name, schema: dep.RuntimeName})
		}
	}
	return out
}

func renderPythonTestDatabases(deps []sqlDependencyEntry) string {
	if len(deps) == 0 {
		return "{}"
	}
	var parts []string
	for _, dep := range deps {
		envKey := postgresname.ServiceDatabaseURLEnv(dep.schema)
		fallback := "postgres://localhost/" + dep.schema
		parts = append(parts, fmt.Sprintf("%s: os.environ.get(%s, %s)",
			strconv.Quote(dep.name), strconv.Quote(envKey), strconv.Quote(fallback)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func renderJSTestDatabases(deps []sqlDependencyEntry) string {
	if len(deps) == 0 {
		return "{}"
	}
	var parts []string
	for _, dep := range deps {
		envKey := postgresname.ServiceDatabaseURLEnv(dep.schema)
		fallback := "postgres://localhost/" + dep.schema
		parts = append(parts, fmt.Sprintf("%s: process.env[%s] ?? %s",
			strconv.Quote(dep.name), strconv.Quote(envKey), strconv.Quote(fallback)))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func jsOptionalLiteral(value string) string {
	if value == "" {
		return "undefined"
	}
	return strconv.Quote(value)
}

func rustSnakeName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return strings.ReplaceAll(b.String(), "-", "_")
}
