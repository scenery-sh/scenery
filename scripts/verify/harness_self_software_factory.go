package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/pythonbuild"
)

func runHarnessSoftwareFactoryStep(ctx context.Context, repo string) harnessStep {
	started := time.Now()
	summary, err := verifySoftwareFactory(ctx, repo)
	step := harnessStep{
		Name:       "software factory probe",
		DurationMS: time.Since(started).Milliseconds(),
		Summary:    summary,
		OK:         err == nil,
	}
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func verifySoftwareFactory(parent context.Context, repo string) (summary map[string]any, err error) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()

	if _, lookErr := exec.LookPath("docker"); lookErr != nil {
		return nil, fmt.Errorf("docker is required for software-factory probe: %w", lookErr)
	}
	if infoErr := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").Run(); infoErr != nil {
		return nil, fmt.Errorf("docker engine is unavailable: %w", infoErr)
	}

	root, err := os.MkdirTemp("", "scenery-software-factory-")
	if err != nil {
		return nil, err
	}
	home := filepath.Join(root, "agent-home")
	appRoot := filepath.Join(root, "factory-app")
	var extraWorktrees []string

	restoreEnv := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME": stringPtr(home),
		"SCENERY_APP_ROOT":   nil,
		"DATABASE_URL":       nil,
	})
	defer restoreEnv()

	defer func() {
		if err != nil {
			if reports, globErr := filepath.Glob(filepath.Join(home, "reports", "rpt_*.json")); globErr == nil && len(reports) > 0 {
				var causes []string
				for _, r := range reports {
					if data, readErr := os.ReadFile(r); readErr == nil {
						causes = append(causes, string(data))
					}
				}
				err = fmt.Errorf("%w\nreports:\n%s", err, strings.Join(causes, "\n"))
			}
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		_, _ = runHarnessAppCLI(cleanupCtx, repo, appRoot, home, "down", "-o", "json")
		for _, wt := range extraWorktrees {
			_, _ = runHarnessAppCLI(cleanupCtx, repo, wt, home, "down", "-o", "json")
			if pgErr := cleanupHarnessWorktreePostgres(cleanupCtx, wt, "factoryapp"); pgErr != nil {
				err = errors.Join(err, pgErr)
			}
			_, _ = runHarnessAppCLI(cleanupCtx, repo, appRoot, home, "worktree", "remove", "branch-wt", "--force", "-o", "json")
		}
		if pgErr := cleanupHarnessWorktreePostgres(cleanupCtx, appRoot, "factoryapp"); pgErr != nil {
			err = errors.Join(err, pgErr)
			return
		}
		_ = os.RemoveAll(root)
	}()

	write := func(base, rel, content string) error {
		full := filepath.Join(base, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(full), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(full, []byte(content), 0o600)
	}

	rootGoMod, err := parseArchitectureGoMod(filepath.Join(repo, "go.mod"))
	if err != nil || rootGoMod.Go == nil {
		return nil, fmt.Errorf("parse repo go.mod: %w", err)
	}
	pgxVer := "v5.8.0"
	for _, req := range rootGoMod.Require {
		if req.Mod.Path == "github.com/jackc/pgx/v5" {
			pgxVer = req.Mod.Version
			break
		}
	}

	if err := write(appRoot, ".gitignore", ".scenery/\n__pycache__/\nbilling/scenerycontract/\nscoring/scenerycontract/\n"); err != nil {
		return nil, err
	}
	if err := write(appRoot, ".scenery.json", `{
  "name": "factoryapp",
  "id": "factoryapp",
  "envs": { "local": { "default": true } },
  "storage": { "stores": { "app": { "kind": "local" } } },
  "database": {
    "migrations": [
      { "service": "billing", "directory": "billing/migrations" }
    ],
    "seed": {
      "commands": [
        {
          "name": "items",
          "service": "billing",
          "command": "go run ./cmd/seed",
          "inputs": ["cmd/seed/main.go"]
        }
      ]
    }
  }
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "go.mod", fmt.Sprintf("module example.test/factoryapp\n\ngo %s\n\nrequire (\n\tgithub.com/jackc/pgx/v5 %s\n\tscenery.sh v0.0.0\n)\n\nreplace scenery.sh => %s\n", rootGoMod.Go.Version, pgxVer, repo)); err != nil {
		return nil, err
	}
	lockBytes, err := os.ReadFile(filepath.Join(repo, "testdata/apps/worktree-postgres/app.lock.scn"))
	if err != nil {
		return nil, err
	}
	if err := write(appRoot, "app.lock.scn", string(lockBytes)); err != nil {
		return nil, err
	}
	if err := write(appRoot, "app.scn", `application "factoryapp" {}

workspace {
  managed_generated_roots = ["billing/scenerycontract"]
}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

provider "postgres" { source = "registry.scenery.dev/core/postgres" }

data_source "billing" {
  provider             = provider.postgres
  lifecycle            = "managed"
  require_capabilities = ["sql.query/v1", "sql.transaction/v1"]
  config               = { database = "billing" }
}

go_module "application" {
  root        = "."
  import_path = "example.test/factoryapp"
}

go_toolchain "application" { version = "`+rootGoMod.Go.Version+`" }

go_target "development" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "disabled"
}

go_target "production" {
  role      = "artifact"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "disabled"
}

module "billing" {
  source = "./billing"
  inputs = {
    gateway  = http_gateway.public_api
    database = data_source.billing
  }
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "billing/package.scn", `package "billing" {
  go_contract { import_path = "example.test/factoryapp/billing" }
}

input "gateway" { type = resource_ref("http_gateway") }
input "database" { type = resource_ref("data_source") }

service "billing" {
  runtime = "go"
  implementation { constructor = "NewService" }
  dependency "database" { instance = var.database }
}

operation "ping" {
  service = service.billing
  input   = std.type.unit
  handler { method = "Ping" }
  result "ok" { type = string }
}

execution "ping" {
  operation = operation.ping
  mode      = "direct"
  timeout   = "10s"
}

binding "ping" {
  gateway        = var.gateway
  operation      = operation.ping
  execution      = execution.ping
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "GET"
    path          = "/billing/ping"
    codec_profile = std.codec.http_json_v1
    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec = "json"
        from  = result.ok
      }
    }
  }
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "billing/migrations/0001_init.sql", `CREATE TABLE items (
  id TEXT PRIMARY KEY,
  note TEXT NOT NULL
);
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "cmd/seed/main.go", `package main

import (
	"context"

	scenerydb "scenery.sh/db"
)

func main() {
	pool, err := scenerydb.Get(context.Background(), "billing")
	if err != nil {
		panic(err)
	}
	defer scenerydb.Close("billing")
	if _, err := pool.ExecContext(context.Background(),
		"INSERT INTO billing.items (id, note) VALUES ('seeded-1', 'from-template') ON CONFLICT (id) DO UPDATE SET note = EXCLUDED.note",
	); err != nil {
		panic(err)
	}
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "billing/service.go", `package billing

import (
	"context"

	contract "example.test/factoryapp/billing/scenerycontract"
)

type Service struct{}

func NewService(context.Context, contract.BillingConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Ping(context.Context, contract.PingInput) (contract.PingOutcome, error) {
	return contract.PingOk{Value: "ok"}, nil
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "billing/service_test.go", `package billing

import (
	"strings"
	"testing"

	scenerydb "scenery.sh/db"
	"scenery.sh/storage"
)

func TestBranchedDatabaseAndHostHarness(t *testing.T) {
	pool, err := scenerydb.Get(t.Context(), "billing")
	if err != nil {
		t.Fatalf("open branched billing database: %v", err)
	}
	defer scenerydb.Close("billing")

	store, err := storage.Named(t.Context(), "app")
	if err != nil {
		t.Fatalf("open ephemeral test storage: %v", err)
	}
	if _, err := store.Put(t.Context(), "probe.txt", strings.NewReader("ok"), storage.PutOptions{}); err != nil {
		t.Fatalf("put object in ephemeral test storage: %v", err)
	}

	var note string
	if err := pool.QueryRowContext(t.Context(), "SELECT note FROM items WHERE id = 'seeded-1'").Scan(&note); err != nil {
		t.Fatalf("query seeded template row: %v", err)
	}
	if note != "from-template" {
		t.Fatalf("seeded note = %q, want from-template", note)
	}
	// Mutate the ephemeral cloned database; subsequent runs and the retained DB must stay clean.
	if _, err := pool.ExecContext(t.Context(), "INSERT INTO items (id, note) VALUES ('ephemeral-mutation', 'dirty')"); err != nil {
		t.Fatalf("insert ephemeral row: %v", err)
	}
}
`); err != nil {
		return nil, err
	}

	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "generate", "-o", "json"); err != nil {
		return nil, fmt.Errorf("initial generate: %w", err)
	}
	tidyCmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidyCmd.Dir = appRoot
	tidyCmd.Env = harnessAppEnv(home)
	if out, tidyErr := tidyCmd.CombinedOutput(); tidyErr != nil {
		return nil, fmt.Errorf("go mod tidy: %w: %s", tidyErr, out)
	}

	// 1. Scaffold Python module with `scenery add module scoring --runtime python`
	addOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "add", "module", "scoring", "--runtime", "python", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("add module scoring --runtime python: %w", err)
	}
	var addPayload map[string]any
	if err := decodeCLIJSON(addOut, &addPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.add.module.result.schema.json"), addPayload); len(errs) > 0 {
		return nil, fmt.Errorf("add module schema: %s", strings.Join(errs, "; "))
	}

	// Customize scoring to accept a POST JSON payload so we can trigger & fix a deterministic SCN9000 crash.
	if err := write(appRoot, "scoring/package.scn", `package "scoring" {
  python_contract { entry = "service.py" }
}

input "gateway" { type = resource_ref("http_gateway") }

service "scoring" {
  runtime = "python"
  implementation { constructor = "new_service" }
}

record "score_input" {
  field "value" { type = string }
}

operation "score" {
  service = service.scoring
  input   = record.score_input
  handler { method = "score" }
  result "ok" { type = record.score_input }
}

execution "score" {
  operation = operation.score
  mode      = "direct"
  timeout   = "10s"
}

binding "score" {
  gateway        = var.gateway
  operation      = operation.score
  execution      = execution.score
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "POST"
    path          = "/score"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "json"
      to    = operation.score.input
    }
    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec = "json"
        from  = result.ok
      }
    }
  }
}
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "scoring/service.py", `from __future__ import annotations

from scenerycontract import ConstructorInput, Context, ScoreInput, ScoreOk, ScoreOutcome


class Service:
    async def score(self, _ctx: Context, input: ScoreInput) -> ScoreOutcome:
        if input.value == "crash":
            raise RuntimeError("boom on crash input")
        return ScoreOk(value=ScoreInput(value="py:" + input.value))


def new_service(_input: ConstructorInput) -> Service:
    return Service()
`); err != nil {
		return nil, err
	}
	if err := write(appRoot, "scoring/test_service.py", `from __future__ import annotations

import unittest

from scenerycontract import ConstructorInput, Context, ScoreInput, ScoreOk, clients
from service import new_service


class ServiceTests(unittest.IsolatedAsyncioTestCase):
    async def test_score_ok(self) -> None:
        svc = new_service(ConstructorInput(clients=clients, config=None, databases={}))
        out = await svc.score(Context.standalone(), ScoreInput(value="hello"))
        self.assertEqual(out, ScoreOk(value=ScoreInput(value="py:hello")))


if __name__ == "__main__":
    unittest.main()
`); err != nil {
		return nil, err
	}
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "generate", "-o", "json"); err != nil {
		return nil, fmt.Errorf("generate scoring contracts: %w", err)
	}

	// Prove ContractRevision invariance when swapping Python <-> TypeScript runtime.
	compiledPy, err := compiler.CompileContext(ctx, appRoot)
	if err != nil || !compiledPy.Valid() {
		return nil, fmt.Errorf("compile python app: %v", err)
	}
	swapRoot := filepath.Join(root, "swap-check")
	for _, rel := range []string{".scenery.json", "app.lock.scn", "app.scn", "billing/package.scn", "scoring/package.scn"} {
		if copyErr := copyHarnessFile(filepath.Join(appRoot, rel), filepath.Join(swapRoot, rel), 0o600); copyErr != nil {
			return nil, copyErr
		}
	}
	rawSwapApp, err := os.ReadFile(filepath.Join(swapRoot, "app.scn"))
	if err != nil {
		return nil, err
	}
	swappedApp := strings.NewReplacer(
		"python_workspace", "js_workspace",
		"python_toolchain", "js_toolchain",
		"python_target", "js_target",
		`"`+firstNonEmpty(pythonbuild.DetectInstalledVersion(ctx), "3.14.6")+`"`, `"1.3.14"`,
	).Replace(string(rawSwapApp))
	if err := write(swapRoot, "app.scn", swappedApp); err != nil {
		return nil, err
	}
	if err := harnessReplaceInFile(filepath.Join(swapRoot, "scoring/package.scn"), `python_contract { entry = "service.py" }`, `js_contract { entry = "service.ts" }`); err != nil {
		return nil, err
	}
	if err := harnessReplaceInFile(filepath.Join(swapRoot, "scoring/package.scn"), `runtime = "python"`, `runtime = "ts"`); err != nil {
		return nil, err
	}
	if err := write(swapRoot, "scoring/service.ts", "export const new_service = () => ({});\n"); err != nil {
		return nil, err
	}
	compiledTS, err := compiler.CompileContext(ctx, swapRoot)
	if err != nil || !compiledTS.Valid() {
		return nil, fmt.Errorf("compile swapped TS app: %v (%+v)", err, compiledTS.Diagnostics)
	}
	if compiledPy.Manifest.ContractRevision != compiledTS.Manifest.ContractRevision {
		return nil, fmt.Errorf("contract_revision changed across python/ts swap: %s vs %s", compiledPy.Manifest.ContractRevision, compiledTS.Manifest.ContractRevision)
	}

	// 2. Provision Postgres & verify `scenery test --with-host` sub-50ms template branching!
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "db", "server", "start", "-o", "json"); err != nil {
		return nil, fmt.Errorf("db server start: %w", err)
	}
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "db", "setup", "-o", "json"); err != nil {
		return nil, fmt.Errorf("db setup: %w", err)
	}

	// First run warms the template DB; second run hits the cached template DB and proves <50ms clone.
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "test", "--with-host", "-o", "json"); err != nil {
		return nil, fmt.Errorf("first scenery test --with-host: %w", err)
	}
	testOut2, err := runHarnessAppCLI(ctx, repo, appRoot, home, "test", "--with-host", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("second scenery test --with-host: %w", err)
	}
	var testPayload map[string]any
	if err := decodeCLIJSON(testOut2, &testPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.test.result.schema.json"), testPayload); len(errs) > 0 {
		return nil, fmt.Errorf("test result schema: %s", strings.Join(errs, "; "))
	}
	var parsedTest struct {
		OK               bool `json:"ok"`
		DatabaseTemplate *struct {
			Name        string `json:"name"`
			TemplateHit bool   `json:"template_hit"`
		} `json:"database_template"`
		Modules []struct {
			Module   string `json:"module"`
			Runtime  string `json:"runtime"`
			OK       bool   `json:"ok"`
			Database *struct {
				CloneMS int64 `json:"clone_ms"`
			} `json:"database"`
		} `json:"modules"`
	}
	if err := decodeCLIJSON(testOut2, &parsedTest); err != nil {
		return nil, err
	}
	if !parsedTest.OK || parsedTest.DatabaseTemplate == nil || !parsedTest.DatabaseTemplate.TemplateHit {
		return nil, fmt.Errorf("expected cached template DB on second test --with-host run: %+v", parsedTest)
	}
	var maxCloneMS int64
	for _, m := range parsedTest.Modules {
		if !m.OK || m.Database == nil {
			return nil, fmt.Errorf("module %s missing database summary: %+v", m.Module, m)
		}
		if m.Database.CloneMS > maxCloneMS {
			maxCloneMS = m.Database.CloneMS
		}
	}
	if maxCloneMS >= 50 {
		return nil, fmt.Errorf("expected sub-50ms test database template clone, got %dms", maxCloneMS)
	}

	// Initialize git repository and test `scenery worktree create branch-wt --prepare-db`
	for _, gitArgs := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "probe@example.test"},
		{"config", "user.name", "Probe"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		gitCmd := exec.CommandContext(ctx, "git", gitArgs...)
		gitCmd.Dir = appRoot
		if out, gitErr := gitCmd.CombinedOutput(); gitErr != nil {
			return nil, fmt.Errorf("git %v: %w: %s", gitArgs, gitErr, out)
		}
	}
	wtOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "worktree", "create", "branch-wt", "--prepare-db", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("worktree create --prepare-db: %w", err)
	}
	var wtPayload map[string]any
	if err := decodeCLIJSON(wtOut, &wtPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.worktree.create.schema.json"), wtPayload); len(errs) > 0 {
		return nil, fmt.Errorf("worktree create schema: %s", strings.Join(errs, "; "))
	}
	var wtParsed struct {
		WorktreePath string `json:"worktree_path"`
		Database     *struct {
			Name     string `json:"name"`
			Template string `json:"template"`
			Cloned   bool   `json:"cloned"`
			CloneMS  int64  `json:"clone_ms"`
		} `json:"database"`
	}
	if err := decodeCLIJSON(wtOut, &wtParsed); err != nil {
		return nil, err
	}
	if wtParsed.WorktreePath != "" {
		extraWorktrees = append(extraWorktrees, wtParsed.WorktreePath)
	}
	if wtParsed.Database == nil || !wtParsed.Database.Cloned || wtParsed.Database.Template == "" || wtParsed.Database.CloneMS >= 50 {
		return nil, fmt.Errorf("worktree create --prepare-db did not clone sub-50ms from template: %+v", wtParsed)
	}

	// 3. Start `scenery up` and verify deterministic `SCN9xxx` replay + `--emit-test`!
	upOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("scenery up: %w", err)
	}
	var upParsed struct {
		Session struct {
			RouteManifest struct {
				BaseURL string `json:"base_url"`
			} `json:"route_manifest"`
		} `json:"session"`
	}
	if err := decodeCLIJSON(upOut, &upParsed); err != nil || upParsed.Session.RouteManifest.BaseURL == "" {
		return nil, fmt.Errorf("decode scenery up URL: %v (%s)", err, upOut)
	}
	baseURL := strings.TrimRight(upParsed.Session.RouteManifest.BaseURL, "/")

	// Verify `scenery test --with-host` runs cleanly alongside live `scenery up` without colliding!
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "test", "--with-host", "-o", "json"); err != nil {
		return nil, fmt.Errorf("concurrent scenery test --with-host during scenery up failed: %w", err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	defer httpClient.CloseIdleConnections()

	// Verify healthy Python worker response first
	okReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/score", strings.NewReader(`{"value":"live"}`))
	okReq.Header.Set("Content-Type", "application/json")
	okResp, err := httpClient.Do(okReq)
	if err != nil {
		return nil, fmt.Errorf("POST /api/score live: %w", err)
	}
	okBody, _ := io.ReadAll(okResp.Body)
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK || !strings.Contains(string(okBody), `"py:live"`) {
		return nil, fmt.Errorf("unexpected Python worker response (%d): %s", okResp.StatusCode, okBody)
	}

	// Trigger SCN9000 internal failure on `{"value":"crash"}`
	crashReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/score", strings.NewReader(`{"value":"crash"}`))
	crashReq.Header.Set("Content-Type", "application/json")
	crashReq.Header.Set("Authorization", "Bearer super-secret-should-be-scrubbed")
	crashResp, err := httpClient.Do(crashReq)
	if err != nil {
		return nil, fmt.Errorf("POST /api/score crash: %w", err)
	}
	crashBody, _ := io.ReadAll(crashResp.Body)
	_ = crashResp.Body.Close()
	reportToken := crashResp.Header.Get("X-Report-Token")
	if crashResp.StatusCode != http.StatusInternalServerError || !strings.HasPrefix(reportToken, "rpt_") {
		return nil, fmt.Errorf("expected HTTP 500 with X-Report-Token, got %d token=%q body=%s", crashResp.StatusCode, reportToken, crashBody)
	}
	if strings.Contains(strings.ToLower(string(crashBody)), "scenery") || strings.Contains(string(crashBody), "boom on crash") {
		return nil, fmt.Errorf("public HTTP 500 leaked internal details or scenery branding: %s", crashBody)
	}

	// Inspect failure report & verify credential scrubbing + captured invocation
	inspectOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "inspect", "report", reportToken, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("inspect report %s: %w", reportToken, err)
	}
	var reportPayload map[string]any
	if err := decodeCLIJSON(inspectOut, &reportPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.failure.report.schema.json"), reportPayload); len(errs) > 0 {
		return nil, fmt.Errorf("failure report schema: %s", strings.Join(errs, "; "))
	}
	if strings.Contains(string(inspectOut), "super-secret-should-be-scrubbed") {
		return nil, fmt.Errorf("failure report leaked Authorization header: %s", inspectOut)
	}

	// Run live `scenery replay <token>` -> expect exit 3 with status == "reproduced"
	replayCmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repo), "replay", reportToken, "--app-root", appRoot, "-o", "json")
	replayCmd.Dir, replayCmd.Env = appRoot, harnessAppEnv(home)
	var replayBuf bytes.Buffer
	replayCmd.Stdout = &replayBuf
	replayErr := replayCmd.Run()
	if replayErr == nil {
		return nil, fmt.Errorf("expected scenery replay to exit non-zero when bug is reproduced")
	}
	var replayReproPayload map[string]any
	if err := decodeCLIJSON(replayBuf.Bytes(), &replayReproPayload); err != nil {
		return nil, fmt.Errorf("decode reproduced replay JSON: %w (%s)", err, replayBuf.String())
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.replay.result.schema.json"), replayReproPayload); len(errs) > 0 {
		return nil, fmt.Errorf("replay reproduced schema: %s", strings.Join(errs, "; "))
	}
	if replayReproPayload["status"] != "reproduced" {
		return nil, fmt.Errorf("expected replay status=reproduced, got %v", replayReproPayload["status"])
	}

	// Emit offline regression test via `scenery replay <token> --emit-test`
	emitOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "replay", reportToken, "--emit-test", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("replay --emit-test: %w", err)
	}
	var emitPayload map[string]any
	if err := decodeCLIJSON(emitOut, &emitPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.replay.result.schema.json"), emitPayload); len(errs) > 0 {
		return nil, fmt.Errorf("replay emit schema: %s", strings.Join(errs, "; "))
	}
	emittedPath, _ := emitPayload["emitted_test_path"].(string)
	if emittedPath == "" {
		return nil, fmt.Errorf("missing emitted_test_path in %v", emitPayload)
	}

	// Before fixing `scoring/service.py`, `scenery test scoring` must fail on the emitted regression test!
	failTestCmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repo), "test", "scoring", "--app-root", appRoot, "-o", "json")
	failTestCmd.Dir, failTestCmd.Env = appRoot, harnessAppEnv(home)
	if failTestCmd.Run() == nil {
		return nil, fmt.Errorf("expected emitted regression test %s to fail before fixing scoring/service.py", emittedPath)
	}

	// Fix `scoring/service.py` live!
	if err := write(appRoot, "scoring/service.py", `from __future__ import annotations

from scenerycontract import ConstructorInput, Context, ScoreInput, ScoreOk, ScoreOutcome


class Service:
    async def score(self, _ctx: Context, input: ScoreInput) -> ScoreOutcome:
        return ScoreOk(value=ScoreInput(value="py:" + input.value))


def new_service(_input: ConstructorInput) -> Service:
    return Service()
`); err != nil {
		return nil, err
	}

	// Re-run `scenery replay <token> --wait -o json` -> expect exit 0 and status == "fixed"!
	fixedOut, err := runHarnessAppCLI(ctx, repo, appRoot, home, "replay", reportToken, "--wait", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("replay --wait after fix failed: %w", err)
	}
	var fixedPayload map[string]any
	if err := decodeCLIJSON(fixedOut, &fixedPayload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.replay.result.schema.json"), fixedPayload); len(errs) > 0 {
		return nil, fmt.Errorf("replay fixed schema: %s", strings.Join(errs, "; "))
	}
	if fixedPayload["status"] != "fixed" {
		return nil, fmt.Errorf("expected replay status=fixed after live edit, got %v", fixedPayload["status"])
	}

	// And `scenery test scoring` now passes with the emitted regression test in place!
	if _, err := runHarnessAppCLI(ctx, repo, appRoot, home, "test", "scoring", "-o", "json"); err != nil {
		return nil, fmt.Errorf("scenery test scoring after fix failed: %w", err)
	}

	return map[string]any{
		"template_database":      parsedTest.DatabaseTemplate.Name,
		"max_module_clone_ms":    maxCloneMS,
		"worktree_clone_ms":      wtParsed.Database.CloneMS,
		"replay_report_token":    reportToken,
		"emitted_regression_py":  emittedPath,
		"contract_revision_kept": compiledPy.Manifest.ContractRevision,
	}, nil
}
