package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

// verificationReuseTool is a directory holding the go command the test
// results select.
var verificationReuseTool string

func verificationReuseResult() *Result {
	return &Result{
		Dir: "/workspace", BuildFingerprint: "build", DependencyFingerprint: "dependencies", FrameworkFingerprint: "framework",
		GeneratorFingerprint: "generator", GraphFingerprint: "graph",
		GoEnvironment: []string{"GOFLAGS=-mod=mod", "SESSION_TOKEN=one", "CGO_ENABLED=0", "PATH=" + verificationReuseTool},
		Contract:      &compiler.Result{ContractStatus: "valid", Manifest: &compiler.Manifest{ContractRevision: "sha256:contract"}},
		Target:        &compiler.GoBuildTarget{Name: "development"},
		verification:  &preparedVerification{},
	}
}

func TestImplementationCheckIsReusedOnlyForTheInputsThatPassed(t *testing.T) {
	previous := generateHooks
	t.Cleanup(func() { generateHooks = previous })
	verificationReuseTool = t.TempDir()
	if err := os.WriteFile(filepath.Join(verificationReuseTool, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	analyses, reuses := 0, 0
	var diagnostic *compiler.Diagnostic
	generateHooks.ApplyPreparedImplementationCheck = func(_ context.Context, checked *compiler.Result, _ string, _ compiler.GoBuildTarget) error {
		analyses++
		if diagnostic != nil {
			checked.Diagnostics = append(checked.Diagnostics, *diagnostic)
		}
		return nil
	}
	generateHooks.ApplyVerifiedImplementationCheck = func(context.Context, *compiler.Result) error {
		reuses++
		return nil
	}
	run := func(result *Result) error {
		t.Helper()
		result.verification = &preparedVerification{}
		return compileWithPreparedVerification(t.Context(), result, func(context.Context) error { return nil })
	}
	result := verificationReuseResult()
	if err := run(result); err != nil || analyses != 1 || reuses != 0 || result.verifiedImplementation == "" {
		t.Fatalf("first check: %v, analyses %d, reuses %d, proof %q", err, analyses, reuses, result.verifiedImplementation)
	}
	proof := result.verifiedImplementation
	// A variable the Go tool does not read belongs to the session, not the inputs.
	result.GoEnvironment = []string{"CGO_ENABLED=0", "SESSION_TOKEN=two", "GOFLAGS=-mod=mod", "PATH=" + verificationReuseTool}
	if err := run(result); err != nil || analyses != 1 || reuses != 1 || result.verifiedImplementation != proof {
		t.Fatalf("unchanged inputs: %v, analyses %d, reuses %d", err, analyses, reuses)
	}
	changes := map[string]func(*Result){
		"workspace":    func(r *Result) { r.BuildFingerprint = "other" },
		"dependencies": func(r *Result) { r.DependencyFingerprint = "other" },
		"framework":    func(r *Result) { r.FrameworkFingerprint = "other" },
		"generator":    func(r *Result) { r.GeneratorFingerprint = "other" },
		"graph":        func(r *Result) { r.GraphFingerprint = "other" },
		"contract": func(r *Result) {
			r.Contract = &compiler.Result{ContractStatus: "valid", Manifest: &compiler.Manifest{ContractRevision: "sha256:other"}}
		},
		"target": func(r *Result) { r.Target = &compiler.GoBuildTarget{Name: "release"} },
		"environment": func(r *Result) {
			r.GoEnvironment = []string{"GOFLAGS=-mod=vendor", "CGO_ENABLED=0", "PATH=" + verificationReuseTool}
		},
		"owned": func(r *Result) { r.OwnedGoModuleSources = []OwnedGoModuleSource{{}} },
	}
	for name, change := range changes {
		changed := verificationReuseResult()
		changed.verifiedImplementation = proof
		change(changed)
		before := analyses
		if err := run(changed); err != nil || analyses != before+1 {
			t.Fatalf("changed %s reused the earlier check: %v", name, err)
		}
	}
	// Unknown inputs never have a proof, and a pending tidy changes them.
	for name, change := range map[string]func(*Result){
		"unknown workspace": func(r *Result) { r.BuildFingerprint = "" },
		"no go command":     func(r *Result) { r.GoEnvironment = []string{"PATH=" + t.TempDir()} },
		"pending tidy":      func(r *Result) { r.NeedsTidy = true },
	} {
		unknown := verificationReuseResult()
		change(unknown)
		before := analyses
		if err := run(unknown); err != nil || analyses != before+1 || unknown.verifiedImplementation != "" {
			t.Fatalf("%s: %v, proof %q", name, err, unknown.verifiedImplementation)
		}
	}
	// A check that reported anything is repeated, so its report is too.
	diagnostic = &compiler.Diagnostic{Code: "SCN6204", Severity: "warning", Message: "stale"}
	warned := verificationReuseResult()
	if err := run(warned); err != nil || warned.verifiedImplementation != "" {
		t.Fatalf("a check with a diagnostic left a proof %q: %v", warned.verifiedImplementation, err)
	}
	diagnostic = nil
	// Inputs that changed while the check ran are not the inputs it proved.
	generateHooks.ApplyPreparedImplementationCheck = func(context.Context, *compiler.Result, string, compiler.GoBuildTarget) error {
		analyses++
		return nil
	}
	moved := verificationReuseResult()
	moved.verification = &preparedVerification{}
	check, err := preparedChecker(moved)
	if err != nil {
		t.Fatal(err)
	}
	verification := moved.verification
	if _, err := check(t.Context()); err != nil {
		t.Fatal(err)
	}
	moved.BuildFingerprint = "tidied"
	recordVerifiedImplementation(moved, verification)
	if moved.verifiedImplementation != "" {
		t.Fatal("a proof was recorded for inputs that changed during the check")
	}
	// A failed reuse is a failed check.
	generateHooks.ApplyVerifiedImplementationCheck = func(context.Context, *compiler.Result) error { return errors.New("artifact check failed") }
	failing := verificationReuseResult()
	failing.verifiedImplementation = implementationVerificationKey(failing)
	if err := run(failing); err == nil {
		t.Fatal("a failed reuse passed")
	}
}
