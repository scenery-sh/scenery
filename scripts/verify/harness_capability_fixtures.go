package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/postgresname"
)

func copyHarnessWebhookFixture(repoRoot, appRoot string) error {
	for _, name := range []string{".scenery.json", ".gitignore", "app.scn", "app.lock.scn", "go.mod", "inbox/service.go", "inbox/package.scn", "cmd/schema/main.go", "client/verify.ts", "client/tsconfig.json"} {
		content, err := os.ReadFile(filepath.Join(repoRoot, "examples/webhook-inbox", name))
		if err != nil {
			return err
		}
		if name == "go.mod" {
			content = []byte(strings.Replace(string(content), "=> ../..", "=> "+filepath.ToSlash(repoRoot), 1))
		}
		if err := writeHarnessToolchainSourceFile(filepath.Join(appRoot, name), string(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func replaceHarnessSource(root, relative, before, after string) error {
	path := filepath.Join(root, relative)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(content), before) {
		return fmt.Errorf("owned fixture %s does not contain expected source %q", relative, before)
	}
	return os.WriteFile(path, []byte(strings.Replace(string(content), before, after, 1)), 0o600)
}

func verifyHarnessAuthOnlyCapability(ctx context.Context, repoRoot, root string, env []string, serviceProcesses bool) error {
	configPath := filepath.Join(root, ".scenery.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		return err
	}
	// The default user email makes bootstrap create a stored user and session,
	// so the authenticated endpoint below reads the framework SQL binding.
	config["auth"] = map[string]any{"enabled": true, "auto_bootstrap_database": true, "dev_bootstrap": map[string]any{"enabled": true, "default_user_email": "probe@example.test"}}
	if err := writeHarnessFixtureConfig(root, config); err != nil {
		return err
	}
	cli := func(appRoot string, args ...string) ([]byte, error) {
		return runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, args...)
	}
	requirements, err := readHarnessSQLRequirements(cli, root)
	if err != nil {
		return err
	}
	if len(requirements) != 1 || requirements[0].Kind != compiler.SQLStandardAuth || requirements[0].Schema != "scenery" || requirements[0].ConfigPath != ".scenery.json#auth.enabled" {
		return fmt.Errorf("auth-only compiled requirement mismatch: %+v", requirements)
	}
	output, err := cli(root, "up", "--detach", "--wait", "ready", "-o", "json")
	var runtime detachedDevResult
	if err != nil || decodeCLIJSON(output, &runtime) != nil {
		return fmt.Errorf("auth-only startup: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, worktreeProbeAPI(runtime)+"/users/dev-bootstrap", strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil || response.StatusCode != http.StatusOK || session.Token == "" {
		return fmt.Errorf("auth-only framework bootstrap failed: status %d (decode: %v)", response.StatusCode, err)
	}
	// The issued session authenticates a standard endpoint, and its absence is
	// rejected by the registered authentication handler, not an unknown route.
	for token, want := range map[string]int{session.Token: http.StatusOK, "": http.StatusUnauthorized} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, worktreeProbeAPI(runtime)+"/auth/me", nil)
		if err != nil {
			return err
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode != want {
			return fmt.Errorf("auth-only /auth/me with token=%t answered %d, want %d: %s", token != "", response.StatusCode, want, body)
		}
	}
	if !serviceProcesses {
		for key := range runtime.Session.Processes {
			if strings.HasPrefix(key, "service-") {
				return fmt.Errorf("an application without a native service started service process %s", key)
			}
		}
	}
	_, err = cli(root, "down", "-o", "json")
	return err
}

// copyHarnessServicelessFixture copies the basic application without its
// native service module, so the process host alone serves it.
func copyHarnessServicelessFixture(repoRoot, appRoot string) error {
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(appRoot, "service")); err != nil {
		return err
	}
	path := filepath.Join(appRoot, "app.scn")
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	module := bytes.Index(source, []byte(`module "service"`))
	if module < 0 {
		return fmt.Errorf("basic fixture no longer declares its service module")
	}
	return os.WriteFile(path, append(bytes.TrimRight(source[:module], "\n"), '\n'), 0o600)
}

func verifyHarnessWebhookHTTP(ctx context.Context, root, baseURL string, env []string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/events", strings.NewReader(`{"event_id":"acceptance-1","payload":"hello durable"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, 8192))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("typed durable admission status %d: %s (read=%v close=%v)", response.StatusCode, content, readErr, closeErr)
	}
	var receipt struct {
		ExecutionID string `json:"execution_id"`
	}
	if json.Unmarshal(content, &receipt) != nil || !strings.HasPrefix(receipt.ExecutionID, "job_") {
		return fmt.Errorf("admission did not return a durable job identity: %s", content)
	}
	return verifyHarnessWebhookClient(ctx, root, baseURL, env)
}

func verifyHarnessWebhookClient(ctx context.Context, root, baseURL string, env []string) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		cmd := commandTreeContext(ctx, "bun", "client/verify.ts", baseURL)
		cmd.Dir, cmd.Env = root, env
		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), "PASS typed client") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("typed authenticated webhook client: %w: %s", err, tailString(string(output), 8192))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func verifyHarnessCapabilityRecovery(ctx context.Context, repoRoot, appRoot, root string, env []string) error {
	cli := func(args ...string) ([]byte, error) {
		return runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, args...)
	}
	sourcePath := filepath.Join(appRoot, "app.scn")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(sourcePath, []byte("invalid source {\n"), 0o600); err != nil {
		return err
	}
	if output, err := cli("inspect", "app", "-o", "json"); err == nil {
		return fmt.Errorf("invalid source was reported as a current successful compile: %s", output)
	}
	if _, err := cli("down", "-o", "json"); err != nil {
		return err
	}
	if _, err := cli("db", "server", "start", "-o", "json"); err != nil {
		return err
	}
	archive := filepath.Join(root, "owned-webhook.zip")
	for _, args := range [][]string{
		{"snapshot", "save", "--db", "--output", archive, "-o", "json"},
		{"snapshot", "verify", "--input", archive, "-o", "json"},
		{"db", "reset", "--yes"},
		{"snapshot", "load", "--db", "--input", archive, "--mode", "overwrite", "--yes", "-o", "json"},
		{"db", "server", "stop", "-o", "json"},
	} {
		if args[1] == "verify" {
			cmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), args...)
			cmd.Dir, cmd.Env = appRoot, env
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("target-independent snapshot verification: %w: %s", err, tailString(string(output), 8192))
			}
			continue
		}
		if _, err := cli(args...); err != nil {
			return err
		}
	}
	if err := os.Remove(sourcePath); err != nil {
		return err
	}
	if _, err := cli("db", "server", "start", "-o", "json"); err != nil {
		return err
	}
	if _, err := cli("down", "-o", "json"); err != nil {
		return err
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return err
	}
	output, err := cli("up", "--detach", "--wait", "ready", "-o", "json")
	var runtime detachedDevResult
	if err != nil || decodeCLIJSON(output, &runtime) != nil {
		return fmt.Errorf("recovered webhook failed to start: %w", err)
	}
	// The client reads the existing processed row before its duplicate enqueue.
	// Do not submit a replacement event that could conceal lost restored data.
	if err := verifyHarnessWebhookClient(ctx, appRoot, worktreeProbeAPI(runtime), env); err != nil {
		return err
	}
	_, err = cli("down", "-o", "json")
	return err
}

func verifyHarnessCapabilityInstances(ctx context.Context, repoRoot, root, home string, env []string) error {
	if err := copyHarnessBasicFixture(repoRoot, root); err != nil {
		return err
	}
	if err := addHarnessSQLDeclarations(repoRoot, root, "reports", "cache"); err != nil {
		return err
	}
	packagePath := filepath.Join(root, "service/package.scn")
	content, err := os.ReadFile(packagePath)
	if err != nil {
		return err
	}
	// Two instances intentionally share the same authored package. No HTTP
	// binding is needed to register its native constructor and dependencies.
	start := strings.Index(string(content), "binding \"echo_http\"")
	if start < 0 {
		return fmt.Errorf("basic fixture no longer has the expected HTTP binding")
	}
	end := strings.Index(string(content)[start:], "\ninput ")
	if end < 0 {
		return fmt.Errorf("basic fixture no longer has the expected binding/input layout")
	}
	content = append(content[:start:start], content[start+end:]...)
	if err := os.WriteFile(packagePath, content, 0o600); err != nil {
		return err
	}
	appPath := filepath.Join(root, "app.scn")
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	appSource = append(appSource, []byte(`
data_source "second_cache" {
  provider = provider.postgres
  lifecycle = "managed"
  require_capabilities = ["sql.query/v1", "sql.transaction/v1"]
  config = { database = "second-cache" }
}
module "second" {
  source = "./service"
  inputs = {
    gateway = http_gateway.public_api
    reports = data_source.reports
    cache = data_source.second_cache
  }
}
`)...)
	if err := os.WriteFile(appPath, appSource, 0o600); err != nil {
		return err
	}
	cli := func(appRoot string, args ...string) ([]byte, error) {
		return runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, args...)
	}
	// A distinct logical name that normalizes onto another binding must fail
	// before the allocation owner is entered, not create a shared empty schema.
	invalid := strings.Replace(string(appSource), `database = "second-cache"`, `database = "reports."`, 1)
	invalid = strings.Replace(invalid, `database = "reports"`, `database = "reports-"`, 1)
	if err := os.WriteFile(appPath, []byte(invalid), 0o600); err != nil {
		return err
	}
	output, conflictErr := cli(root, "db", "server", "start", "-o", "json")
	if conflictErr == nil || !strings.Contains(string(output), "both map") {
		return fmt.Errorf("conflicting schemas were not rejected: %s: %w", output, conflictErr)
	}
	paths, err := localagent.PathsForWorktree(home, root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.Record); !os.IsNotExist(err) {
		return fmt.Errorf("conflicting schemas created allocation state: %v", err)
	}
	if err := os.WriteFile(appPath, appSource, 0o600); err != nil {
		return err
	}
	requirements, err := readHarnessSQLRequirements(cli, root)
	if err != nil {
		return err
	}
	if len(requirements) != 3 || len(requirements.Bindings(false)) != 3 {
		return fmt.Errorf("module instance SQL requirements collapsed: %+v", requirements)
	}
	for _, requirement := range requirements {
		if requirement.Kind != compiler.SQLDataSource {
			return fmt.Errorf("unexpected framework requirement: %+v", requirement)
		}
		if requirement.Name == "reports" && len(requirement.Consumers) != 2 {
			return fmt.Errorf("shared source lost one instance consumer: %+v", requirement)
		}
		if requirement.Name == "second-cache" && requirement.Schema != "second_cache" {
			return fmt.Errorf("logical schema normalization changed: %+v", requirement)
		}
	}
	if _, err := cli(root, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
		return err
	}
	record, err := paths.LoadRecord("")
	if err != nil || record.Postgres == nil {
		return fmt.Errorf("instance fixture has no owned allocation: %w", err)
	}
	dsn := worktreePostgresURL(record.Postgres, postgresname.DatabaseNameFor(record.AppID, paths.AppRoot))
	db, err := openPostgresDatabase(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname IN ('reports','cache','second_cache')`).Scan(&count); err != nil || count != 3 {
		return fmt.Errorf("actual instance schemas count=%d: %w", count, err)
	}
	_, err = cli(root, "down", "-o", "json")
	return err
}
