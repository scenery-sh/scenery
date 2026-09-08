package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

const harnessStandardAuthName = "standard auth lifecycle"

var harnessStandardAuthCases = []struct{ ID, Root string }{
	{"schema", "TestStandardAuthBootstrapPostgresSchema"},
	{"dev-new", "TestDevBootstrapDefaultEmailCreatesUserTenantAndMembership"},
	{"dev-existing", "TestDevBootstrapAttachesExistingUserToConfiguredTenant"},
	{"oauth-browser", "TestGoogleOAuthBrowserFlowWithFakeGoogle"},
	{"connection-redirect", "TestGoogleConnectionStartFallsBackToConfiguredAPIBaseURL"},
	{"connection-store", "TestGoogleConnectionFlowStoresEncryptedTokenAndDisconnects"},
	{"connection-error", "TestGoogleConnectionCallbackOAuthErrorUsesStateRedirect"},
	{"token-rotation", "TestGoogleAccessTokenRefreshesRotatesAndSingleFlights"},
	{"token-retry", "TestGoogleAccessTokenRetriesTransientAndMarksPermanentRefreshFailures"},
	{"token-missing-scope", "TestGoogleAccessTokenReportsMissingScopes"},
	{"token-disallowed-scope", "TestGoogleAccessTokenEnforcesAllowedScopes"},
	{"impersonation", "TestPrepareImpersonationTargetAndStartUnverifiedSession"},
	{"impersonation-privilege", "TestPrepareImpersonationTargetRequiresPrivilege"},
	{"refresh-replay", "TestRefreshReplayRevokesSessionAcrossTransaction"},
	{"user-lifecycle", "TestUserLifecyclePostgres"},
}

type harnessAuthCaseReport struct {
	Case       string   `json:"case"`
	OK         bool     `json:"ok"`
	Assertions []string `json:"assertions"`
	Error      string   `json:"error,omitempty"`
}

func runHarnessStandardAuthStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessStandardAuthStepWithCheck(ctx, repoRoot, runHarnessStandardAuth)
}

func runHarnessStandardAuthStepWithCheck(ctx context.Context, repoRoot string, check func(context.Context, string) (map[string]any, error)) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessStandardAuthName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"}}
	var err error
	step.Summary, err = check(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Restore the complete public auth/SQL release journeys and owned-resource cleanup, then rerun the release verifier."}}
	}
	return step
}

// Every journey gets a fresh native process and database. Only the retained
// public worktree lifecycle helpers provision and retire its disposable cluster.
func runHarnessStandardAuth(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if !harnessDockerAvailable(ctx) {
		return nil, errors.New("docker is required; standard auth release proof cannot skip")
	}
	root, err := os.MkdirTemp("", "scenery-standard-auth-*")
	if err != nil {
		return nil, err
	}
	fixture, home := filepath.Join(root, "worktree"), filepath.Join(root, "state")
	restore := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(home), "SCENERY_APP_ROOT": nil, "DATABASE_URL": nil, "SCENERY_DATABASE_JSON": nil, "REPORTS_DATABASE_URL": nil, "CACHE_DATABASE_URL": nil})
	defer restore()
	summary = map[string]any{"fixture_root": root, "cases": []map[string]any{}, "expected_cases": len(harnessStandardAuthCases)}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := cleanupHarnessWorktreePostgres(cleanup, fixture, "postgres-harness")
		summary["cleanup_ok"] = err == nil
		returnErr = errors.Join(returnErr, err)
		if returnErr == nil {
			returnErr = os.RemoveAll(root)
		}
	}()
	if err := writePostgresHarnessConfig(repoRoot, fixture); err != nil {
		return summary, err
	}
	_, database, err := provisionHarnessDatabase(ctx, repoRoot, fixture, app.Config{Name: "postgres-harness", ID: "postgres-harness"}, "cache", "reports")
	if err != nil {
		return summary, err
	}
	admin, err := postgresdb.Open(ctx, database.URL)
	if err != nil {
		return summary, err
	}
	defer func() { returnErr = errors.Join(returnErr, admin.Close()) }()
	binary := filepath.Join(root, "authprobe")
	if output, err := runHarnessTimingConfirmationCommand(ctx, repoRoot, []string{"go", "build", "-o", binary, "./scripts/verify/testdata/authprobe"}); err != nil {
		return summary, fmt.Errorf("build auth fixture: %w: %s", err, tailString(string(output), 8192))
	}
	summary["fixture_sha256"], err = worktreeProbeFileSHA(binary)
	if err != nil {
		return summary, err
	}
	list := commandTreeContext(ctx, binary, "--list")
	output, err := list.CombinedOutput()
	if err != nil {
		return summary, fmt.Errorf("list auth fixture cases: %w", err)
	}
	var actual []string
	if err := json.Unmarshal(output, &actual); err != nil {
		return summary, err
	}
	want := make([]string, 0, len(harnessStandardAuthCases))
	for _, scenario := range harnessStandardAuthCases {
		want = append(want, scenario.ID)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		return summary, fmt.Errorf("auth fixture case inventory differs: got %v, want %v", actual, want)
	}
	var failures []error
	for index, scenario := range harnessStandardAuthCases {
		if ctx.Err() != nil {
			return summary, errors.Join(ctx.Err(), errors.Join(failures...))
		}
		started := time.Now()
		report, output, caseErr := runHarnessAuthCase(ctx, admin, database.URL, root, binary, index, scenario.ID, home)
		row := map[string]any{"id": scenario.ID, "root": scenario.Root, "ok": caseErr == nil, "duration_ms": time.Since(started).Milliseconds(), "assertions": report.Assertions}
		if caseErr != nil {
			row["error"] = caseErr.Error()
			row["output_tail"] = tailString(string(output), 8192)
			failures = append(failures, fmt.Errorf("%s: %w", scenario.ID, caseErr))
		}
		summary["cases"] = append(summary["cases"].([]map[string]any), row)
	}
	return summary, errors.Join(failures...)
}

func runHarnessAuthCase(ctx context.Context, admin *sql.DB, base, root, binary string, index int, name, home string) (report harnessAuthCaseReport, output []byte, returnErr error) {
	databaseName := fmt.Sprintf("scenery_auth_probe_%d", index)
	if err := postgresdb.EnsureDatabase(ctx, admin, databaseName); err != nil {
		return report, nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, postgresdb.DropDatabase(cleanup, admin, databaseName))
	}()
	u, err := url.Parse(base)
	if err != nil {
		return report, nil, err
	}
	u.Path = "/" + databaseName
	path := filepath.Join(root, name+".json")
	command := commandTreeContext(ctx, binary, "--case", name, "--report", path)
	command.Dir = root
	command.Env = envWithOverrides(harnessAppEnv(home), "DATABASE_URL="+u.String(), "SCENERY_DATABASE_JSON=", "SCENERY_ROLE=", "SCENERY_LISTEN_NETWORK=", "SCENERY_DURABLE_ENDPOINT=")
	output, commandErr := command.CombinedOutput()
	encoded, readErr := os.ReadFile(path)
	if readErr != nil {
		return report, output, errors.Join(commandErr, readErr)
	}
	if err := json.Unmarshal(encoded, &report); err != nil {
		return report, output, errors.Join(commandErr, err)
	}
	if err := validateHarnessAuthCase(name, report); err != nil {
		return report, output, errors.Join(commandErr, err)
	}
	return report, output, commandErr
}

func validateHarnessAuthCase(expected string, report harnessAuthCaseReport) error {
	if report.Case != expected || !report.OK || len(report.Assertions) < 2 || report.Error != "" {
		return fmt.Errorf("auth case %s is incomplete or failed: case=%s ok=%t assertions=%d error=%s", expected, report.Case, report.OK, len(report.Assertions), report.Error)
	}
	return nil
}
