package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/victoria"
)

func writeTestAppFileIfChanged(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if current, err := os.ReadFile(path); err == nil && string(current) == contents {
		return
	}
	writeTestAppFile(t, root, rel, contents)
}

func persistentTestAppRoot(t *testing.T, name string) string {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cacheDir, "scenery", "cmd-scenery-tests", name)
}

func preparePersistentTestApp(t *testing.T, root string, files map[string]string) {
	t.Helper()
	fingerprint := testAppFingerprint(files)
	marker := filepath.Join(root, ".scenery-test-fingerprint")
	data, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != fingerprint {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		writeTestAppFileIfChanged(t, root, rel, files[rel])
	}
	writeTestAppFileIfChanged(t, root, ".scenery-test-fingerprint", fingerprint+"\n")
}

func testAppFingerprint(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(files[rel]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeTestAppFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if rel == ".scenery.json" {
		contractPath := filepath.Join(root, "scenery.scn")
		if _, err := os.Stat(contractPath); os.IsNotExist(err) {
			contract := "application \"test\" {}\n"
			if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", contractPath, err)
			}
		}
	}
}

func writeWatchFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	callErr := fn()
	_ = w.Close()
	os.Stdout = old
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if callErr != nil {
		t.Fatalf("command returned error: %v", callErr)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	return string(data)
}

func writeHarnessTestApp(t *testing.T, root, name, body string) {
	t.Helper()
	for rel, contents := range nativeHarnessTestFiles(t, name, body) {
		writeTestAppFile(t, root, rel, contents)
	}
}

func nativeHarnessTestFiles(t *testing.T, name, body string) map[string]string {
	t.Helper()
	extra := ""
	if strings.Contains(body, "MissingSymbol") {
		extra = "\nvar _ = MissingSymbol\n"
	}
	module := "example.com/" + name
	return map[string]string{
		".scenery.json": `{"name":"` + name + `","id":"` + name + `-id"}`,
		"go.mod":        "module " + module + "\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + filepath.ToSlash(repoRootForTest(t)) + "\n",
		"scenery.scn": `workspace {
  implementation_root "application" {
    path = "."
    revision_include = ["**/*.go", "go.mod"]
  }
  managed_generated_roots = ["internal/scenerygen"]
}
go_module "application" {
  root = "."
  import_path = "` + module + `"
}
go_toolchain "application" {
	version = "1.26.3"
  experiments = []
}
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
  cgo = "disabled"
}
application "` + name + `" {
}
`,
		"svc/api.go": `package svc

import "context"

func Ping(context.Context) error {
  return nil
}
` + extra,
	}
}

func writeHarnessSelfRepo(t *testing.T, schema string, requestedSchemas ...string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.26.3\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [harness](docs/harness-engineering.md).\n")
	writeTestAppFile(t, root, "SKILL.md", strings.Join(requiredSkillMentions, "\n")+"\n")
	writeTestAppFile(t, root, "PLAN.md", "See [docs](docs/index.md).\n")
	writeTestAppFile(t, root, "PLANS.md", validExecPlanStandardForTest())
	writeTestAppFile(t, root, "docs/index.md", "See [local](local-contract.md), [plans](plans/active.md), and [debt](tech-debt.md).\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "Contract.\n")
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{"kind":"`+envpolicy.Kind+`","schema_revision":"`+envpolicy.SchemaRevision+`","variables":[{"name":"SCENERY_TEST_","match":"prefix","scope":"test_only","direction":"test_input","category":"tests","stability":"test_only","secret":false,"allowed_in":["docs","tests"],"owner":"scenery runtime","rationale":"Test-only controls.","preferred_surface":"tests","docs":["docs/environment.md"]}]}`)
	writeTestAppFile(t, root, "docs/app-development-cookbook.md", "Cookbook.\n")
	writeTestAppFile(t, root, "docs/ui-agent-contract.md", "UI contract.\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "Harness.\n")
	writeTestAppFile(t, root, "docs/plans/active.md", "Active.\n")
	writeTestAppFile(t, root, "docs/plans/completed.md", "Completed.\n")
	writeTestAppFile(t, root, "docs/tech-debt.md", "Debt.\n")
	schemaNames := append([]string{"scenery.docs.index.schema.json"}, requestedSchemas...)
	sort.Strings(schemaNames)
	for i, name := range schemaNames {
		name = strings.TrimSpace(name)
		if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".schema.json") {
			t.Fatalf("invalid harness fixture schema name %q", name)
		}
		if i > 0 && name == strings.TrimSpace(schemaNames[i-1]) {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRootForTest(t), "docs", "schemas", name)); err != nil {
			t.Fatalf("unknown harness fixture schema %q: %v", name, err)
		}
		writeTestAppFile(t, root, filepath.Join("docs", "schemas", name), schema)
	}
	writeTestAppFile(t, root, "docs/knowledge.json", `{
  "kind": "`+docsIndexKind+`",
  "schema_revision": "`+docsIndexSchemaRevision+`",
  "generated_at": "2026-04-27T00:00:00Z",
  "owner_default": "scenery maintainers",
  "freshness_policy": {
    "default_review_days": 30,
    "quality_grades": ["A", "B", "C", "D"],
    "freshness_states": ["current", "review_due", "stale"]
  },
  "documents": [
    {
      "path": "SKILL.md",
      "title": "Skill",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Skill.",
      "tags": ["skill"]
    },
    {
      "path": "docs/index.md",
      "title": "Index",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Index.",
      "tags": ["docs"]
    },
    {
      "path": "docs/app-development-cookbook.md",
      "title": "Cookbook",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Cookbook.",
      "tags": ["cookbook"]
    },
    {
      "path": "docs/ui-agent-contract.md",
      "title": "UI contract",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "UI.",
      "tags": ["ui"]
    },
    {
      "path": "docs/local-contract.md",
      "title": "Contract",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Contract.",
      "tags": ["contract"],
      "schema_refs": ["docs/schemas/scenery.docs.index.schema.json"]
    }
  ],
  "plans": {
    "active": "docs/plans/active.md",
    "completed": "docs/plans/completed.md"
  },
  "tech_debt": "docs/tech-debt.md"
}`)
	return root
}

func validExecPlanStandardForTest() string {
	var b strings.Builder
	b.WriteString("# scenery Execution Plans\n\n")
	b.WriteString("## Required Sections\n\n")
	for _, section := range requiredExecPlanSections {
		b.WriteString("- `")
		b.WriteString(section)
		b.WriteString("`\n")
	}
	return b.String()
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore Chdir(%q): %v", prev, err)
		}
	}
}

func waitForAgentCommandPing(ctx context.Context, client *localagent.Client) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return lastErr
}

func startTestAgentServer(t *testing.T, ctx context.Context) <-chan error {
	return startTestAgentServerWithPathSetup(t, ctx, nil)
}

func startTestAgentServerWithPathSetup(t *testing.T, ctx context.Context, setup func(localagent.Paths)) <-chan error {
	t.Helper()
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(paths)
	}
	server, err := localagent.NewServer(localagent.RunOptions{
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return done
}

func waitForTestAgentServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func harnessArtifactExists(items []harnessArtifact, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Exists
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func installLogsVictoriaStack(t *testing.T, events ...devdash.DevEvent) *victoria.Stack {
	t.Helper()
	for i := range events {
		if events[i].ID == 0 {
			events[i].ID = int64(i + 1)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		for _, event := range events {
			data, err := json.Marshal(victoria.DevEventRecord(event))
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			_, _ = w.Write(append(data, '\n'))
		}
	}))
	t.Cleanup(server.Close)
	stack := victoria.NewStack(victoria.ExternalComponent{Name: "logs", BaseURL: server.URL})
	prev := resolveLogsVictoriaStackFunc
	resolveLogsVictoriaStackFunc = func(ctx context.Context, allowDefault bool) *victoria.Stack {
		return stack
	}
	t.Cleanup(func() {
		resolveLogsVictoriaStackFunc = prev
	})
	return stack
}

func stopAgentServerForTest(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("agent shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func waitForSubstrateStatus(t *testing.T, ctx context.Context, client *localagent.Client, kind, status string) localagent.Substrate {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last localagent.Substrate
	var lastErr error
	for time.Now().Before(deadline) {
		got, err := client.GetSubstrate(ctx, kind)
		if err == nil {
			last = got
			if got.Status == status {
				return got
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("substrate %s status = %+v err=%v, want %s", kind, last, lastErr, status)
	return localagent.Substrate{}
}

func startSubstrateTestAgent(t *testing.T) (context.Context, *localagent.Client) {
	t.Helper()
	ctx := context.Background()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(runCtx) }()
	t.Cleanup(func() {
		stopAgentServerForTest(t, cancel, done)
	})
	client := localagent.NewClient(server.Paths().SocketPath)
	t.Cleanup(client.CloseIdleConnections)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return ctx, client
}

func startFakeSubstrateOwner(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake substrate owner: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func waitForMonitorDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for substrate monitor")
	}
}

func currentPlatformDirForTest() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func processAliveForTest(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func diagnosticMessages(diagnostics []checkDiagnostic) string {
	messages := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		messages = append(messages, diag.Message)
	}
	return strings.Join(messages, "\n")
}
