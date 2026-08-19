package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func copyAssistantFixture(t *testing.T) string {
	t.Helper()
	source := filepath.Join(repoRootForTest(t), "testdata", "assistant")
	root := t.TempDir()
	if err := copyAssistantFixtureTree(root, source); err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.ReplaceAll(string(goMod), "=> ../..", "=> "+filepath.ToSlash(repoRootForTest(t))))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyAssistantFixtureTree(destination, source string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		first := strings.Split(filepath.ToSlash(relative), "/")[0]
		if first == ".scenery" || first == "node_modules" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func TestAssistantInitDryRunApplyIdempotentAndPreservesAuthoredFiles(t *testing.T) {
	root := copyAssistantFixture(t)
	compiledRoot, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	appBefore, err := os.ReadFile(filepath.Join(root, "app.scn"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := initializeAssistant(context.Background(), compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !response.DryRun || response.Applied || len(response.Created) != 4 {
		t.Fatalf("dry-run response = %#v", response)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "app.scn")); string(got) != string(appBefore) {
		t.Fatal("dry-run changed app.scn")
	}
	if _, err := os.Stat(filepath.Join(root, "assistants", "extra")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created assistant files: %v", err)
	}
	response, err = initializeAssistant(context.Background(), compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Applied || response.Idempotent || len(response.Created) != 4 {
		t.Fatalf("apply response = %#v", response)
	}
	if _, err := os.Stat(filepath.Join(root, "assistants", "extra", "eval")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "assistants", "extra", "eval"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("eval directory not empty: %#v", entries)
	}
	lock, err := os.ReadFile(filepath.Join(root, "assistants", "extra", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(lock)
	if got, want := "sha256:"+hex.EncodeToString(sum[:]), "sha256:50688be5a4ea2b73acffd21b724caa699ea81e8343befd22b1212e89e845938a"; got != want {
		t.Fatalf("lock digest=%s want=%s", got, want)
	}
	instructionsPath := filepath.Join(root, "assistants", "extra", "agent", "instructions.md")
	if err := os.WriteFile(instructionsPath, []byte("edited by developer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err = initializeAssistant(context.Background(), compiledRoot, cfg, mustCompileAssistant(t, root), assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Idempotent || response.Applied || len(response.Created) != 0 {
		t.Fatalf("second init response = %#v", response)
	}
	if got, _ := os.ReadFile(instructionsPath); string(got) != "edited by developer\n" {
		t.Fatal("second init overwrote authored instructions")
	}
	if !strings.Contains(string(mustReadAssistant(t, filepath.Join(root, "app.scn"))), `assistant "extra"`) {
		t.Fatal("canonical assistant block missing")
	}
}

func TestAssistantInitRejectsExistingNonRegularScaffoldFile(t *testing.T) {
	root := copyAssistantFixture(t)
	path := filepath.Join(root, "assistants", "extra", "agent")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "instructions.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initializeAssistant(context.Background(), root, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api", DryRun: true}); err == nil {
		t.Fatal("init accepted directory at authored file path")
	}
}

func TestAssistantScaffoldResponsesMatchSchemas(t *testing.T) {
	root := repoRootForTest(t)
	initResponse := assistantInitResponse{
		cliPayloadIdentity:         newCLIPayloadIdentity(assistantInitKind),
		Assistant:                  "support",
		Address:                    "app/assistant/support",
		MCPServer:                  "support",
		Client:                     "public_api",
		Source:                     "./assistants/support",
		Package:                    "./assistants/support/package.json",
		PackageLock:                "./assistants/support/package-lock.json",
		EvalDirectory:              "./assistants/support/eval",
		DryRun:                     true,
		Applied:                    false,
		Idempotent:                 false,
		Created:                    []string{"./assistants/support/agent/agent.ts"},
		Preserved:                  []string{},
		PlanID:                     digestBytes([]byte("plan")),
		BaseWorkspaceRevision:      digestBytes([]byte("base")),
		PredictedWorkspaceRevision: digestBytes([]byte("predicted")),
		ContractRevision:           digestBytes([]byte("contract")),
		Files:                      []assistantInitFile{{Path: "./assistants/support/agent/agent.ts", Action: "create"}},
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.init.schema.json"), initResponse); len(diagnostics) != 0 {
		t.Fatalf("init response schema diagnostics = %v", diagnostics)
	}
	syncResponse := assistantSyncResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(assistantSyncKind), Assistant: "support", Address: "app/assistant/support",
		Source: "./assistants/support", Package: "./assistants/support/package.json", PackageLock: "./assistants/support/package-lock.json",
		LockDigest: digestBytes([]byte("lock")), PackageDigest: digestBytes([]byte("package")), CachePath: "/tmp/cache", Status: "reused", Reused: true,
		NodePath: "/tmp/node", NPMPath: "/tmp/npm",
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.sync.schema.json"), syncResponse); len(diagnostics) != 0 {
		t.Fatalf("sync response schema diagnostics = %v", diagnostics)
	}
}

func mustCompileAssistant(t *testing.T, root string) *compiler.Result {
	t.Helper()
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustReadAssistant(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
