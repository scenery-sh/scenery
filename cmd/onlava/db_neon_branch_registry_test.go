package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
)

func TestNeonFixtureConfigLoads(t *testing.T) {
	t.Parallel()

	basicRoot := filepath.Join(appcfg.RepoRoot(), "testdata", "apps", "neon-basic")
	_, basicCfg, err := appcfg.DiscoverRoot(basicRoot)
	if err != nil {
		t.Fatalf("DiscoverRoot(neon-basic) error = %v", err)
	}
	_, basicSvc, ok := managedPostgresDeclared(basicCfg)
	if !ok || basicSvc.Kind != "neon" {
		t.Fatalf("managed Postgres service = %+v, ok=%v", basicSvc, ok)
	}
	basicPin, err := buildWorktreeDBPin(basicRoot, basicCfg, "neonbasic/fixture")
	if err != nil {
		t.Fatalf("buildWorktreeDBPin(neon-basic) error = %v", err)
	}
	if basicPin.Project != "neonbasic" || basicPin.Database != "neonbasic" || basicPin.Role != "cloud_admin" {
		t.Fatalf("basic pin = %+v", basicPin)
	}

	electricRoot := filepath.Join(appcfg.RepoRoot(), "testdata", "apps", "neon-electric")
	_, electricCfg, err := appcfg.DiscoverRoot(electricRoot)
	if err != nil {
		t.Fatalf("DiscoverRoot(neon-electric) error = %v", err)
	}
	_, electricSvc, ok := managedPostgresDeclared(electricCfg)
	if !ok || electricSvc.Kind != "neon" {
		t.Fatalf("electric managed Postgres service = %+v, ok=%v", electricSvc, ok)
	}
	electricName, electricService, ok := managedElectricDeclared(electricCfg)
	if !ok || electricName != "electric" || electricService.Kind != "electric" || electricService.Database != "postgres" {
		t.Fatalf("managed Electric service name=%q svc=%+v ok=%v", electricName, electricService, ok)
	}
	electricPin, err := buildWorktreeDBPin(electricRoot, electricCfg, "neonelectric/fixture")
	if err != nil {
		t.Fatalf("buildWorktreeDBPin(neon-electric) error = %v", err)
	}
	if electricPin.Project != "neonelectric" || electricPin.Database != "neonelectric" || electricPin.Role != "cloud_admin" {
		t.Fatalf("electric pin = %+v", electricPin)
	}
}

func TestDBBranchExpireAndPruneLocalRegistry(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/old", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	var listOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &listOut, []string{"list", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	var listed dbBranchListResult
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list JSON: %v\n%s", err, listOut.String())
	}
	if len(listed.Branches) != 2 || len(listed.Leases) != 2 || listed.RegistryPath == "" {
		t.Fatalf("listed = %+v", listed)
	}
	for _, lease := range listed.Leases {
		if lease.Status != "missing" || lease.Pin.Branch == "" {
			t.Fatalf("lease = %+v", lease)
		}
	}

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"expire", "feature/old", "--after", "-1h", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("expire returned error: %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var oldExpired bool
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/old" && lease.ExpiresAt != "" {
			oldExpired = true
		}
	}
	if !oldExpired {
		t.Fatalf("registry after expire = %+v", registry.Leases)
	}
	foreignPin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       "branchapp",
		ParentBranch:  "main",
		Branch:        "feature/foreign",
		BranchID:      "br-foreign",
		Database:      "branchapp",
		Role:          "cloud_admin",
		CreatedBy:     "external",
	}
	registry.Leases = append(registry.Leases, neonBranchLease{
		Pin:       foreignPin,
		Status:    "expired",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	rootDir, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	if err := writeNeonBranchRegistry(rootDir, registry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	var pruneOut bytes.Buffer
	if err := runDBBranchCommand(t.Context(), &pruneOut, []string{"prune", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("prune returned error: %v", err)
	}
	var pruned dbBranchListResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruned); err != nil {
		t.Fatalf("decode prune JSON: %v\n%s", err, pruneOut.String())
	}
	if len(pruned.Branches) != 1 || pruned.Branches[0].Branch != "feature/current" || len(pruned.Leases) != 1 || pruned.Leases[0].Status != "missing" {
		t.Fatalf("pruned = %+v", pruned)
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after prune: %v", err)
	}
	var foreignKept bool
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/foreign" && lease.Pin.CreatedBy == "external" {
			foreignKept = true
		}
	}
	if !foreignKept {
		t.Fatalf("foreign lease was pruned: %+v", registry.Leases)
	}
}

func TestDBBranchPruneDoesNotRemoveOtherProjectLeases(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/old", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"expire", "feature/old", "--after", "-1h", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("expire returned error: %v", err)
	}
	now := time.Now().UTC()
	otherPin := neonPinForTest("otherapp", "feature/old", "")
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	registry.Leases = append(registry.Leases, neonBranchLease{
		Pin:       otherPin,
		Status:    "expired",
		CreatedAt: now.Add(-3 * time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(-1 * time.Hour).Format(time.RFC3339),
	})
	rootDir, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	if err := writeNeonBranchRegistry(rootDir, registry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"prune", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("prune returned error: %v", err)
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after prune: %v", err)
	}
	var sameProjectOld, otherProjectOld bool
	for _, lease := range registry.Leases {
		sameProjectOld = sameProjectOld || (lease.Pin.Project == "branchapp" && lease.Pin.Branch == "feature/old")
		otherProjectOld = otherProjectOld || (lease.Pin.Project == "otherapp" && lease.Pin.Branch == "feature/old")
	}
	if sameProjectOld || !otherProjectOld {
		t.Fatalf("registry after project-scoped prune = %+v", registry.Leases)
	}
}

func TestDBBranchDeleteRemovesPendingLocalLease(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/kept", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/kept", "--app-root", root}); err != nil {
		t.Fatalf("delete pending non-current lease returned error: %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var kept, current bool
	for _, lease := range registry.Leases {
		kept = kept || lease.Pin.Branch == "feature/kept"
		current = current || lease.Pin.Branch == "feature/current"
	}
	if kept || !current {
		t.Fatalf("registry after non-current delete = %+v", registry.Leases)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/current", "--app-root", root}); err == nil || !strings.Contains(err.Error(), "without --force") {
		t.Fatalf("delete current without force error = %v", err)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/current", "--app-root", root, "--force"}); err != nil {
		t.Fatalf("delete current pending lease returned error: %v", err)
	}
	registry, _, err = readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry after current delete: %v", err)
	}
	for _, lease := range registry.Leases {
		if lease.Pin.Branch == "feature/current" {
			t.Fatalf("current lease still present: %+v", registry.Leases)
		}
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); !os.IsNotExist(err) {
		t.Fatalf("current delete should remove worktree pin, stat err=%v", err)
	}
}

func TestDBBranchDeleteReadyLeaseRequiresBackend(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", "feature/ready", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("checkout returned error: %v", err)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read pin ok=%v err=%v", ok, err)
	}
	markNeonLeaseReadyForTest(t, pin, neonEndpoint{
		Host:     "127.0.0.1",
		Port:     55432,
		Database: "branchapp",
		Role:     "cloud_admin",
		SSLMode:  "disable",
		Source:   "test",
	})

	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "feature/ready", "--app-root", root, "--force"})
	if err == nil || !strings.Contains(err.Error(), "no Neon branch driver is configured") {
		t.Fatalf("ready delete error = %v", err)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Leases) != 1 || registry.Leases[0].Status != "ready" {
		t.Fatalf("ready lease should remain present: %+v", registry.Leases)
	}
}

func TestNeonDownCleanupRemovesCurrentLeaseAndPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)

	for _, branch := range []string{"feature/kept", "feature/current"} {
		if err := runDBBranchCommand(t.Context(), io.Discard, []string{"checkout", branch, "--app-root", root, "--json"}); err != nil {
			t.Fatalf("checkout %s returned error: %v", branch, err)
		}
	}
	message, err := dropSessionManagedDatabase(t.Context(), root, localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("dropSessionManagedDatabase returned error: %v", err)
	}
	if !strings.Contains(message, "removed local Neon branch lease feature/current") {
		t.Fatalf("message = %q", message)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var kept, current bool
	for _, lease := range registry.Leases {
		kept = kept || lease.Pin.Branch == "feature/kept"
		current = current || lease.Pin.Branch == "feature/current"
	}
	if !kept || current {
		t.Fatalf("registry after down db cleanup = %+v", registry.Leases)
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); err != nil {
		t.Fatalf("db cleanup removed worktree pin: %v", err)
	}

	removed, err := removeNeonWorktreeDBPinIfConfigured(root)
	if err != nil {
		t.Fatalf("removeNeonWorktreeDBPinIfConfigured returned error: %v", err)
	}
	if !removed {
		t.Fatal("expected state cleanup to remove worktree pin")
	}
	if _, err := os.Stat(worktreeDBPinPath(root)); !os.IsNotExist(err) {
		t.Fatalf("worktree pin still exists or stat failed: %v", err)
	}
}

func TestDownDBRemovesSelectedSessionLeaseOnly(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp","branch_policy":"session"}}}}`)

	pinA := neonPinForTest("branchapp", "branchapp/session-a", "session-a")
	pinB := neonPinForTest("branchapp", "branchapp/session-b", "session-b")
	if err := upsertNeonBranchLease(pinA); err != nil {
		t.Fatalf("upsert pin A: %v", err)
	}
	if err := writeWorktreeDBPin(root, pinB); err != nil {
		t.Fatalf("write current pin B: %v", err)
	}

	message, err := dropSessionManagedDatabase(t.Context(), root, localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("dropSessionManagedDatabase returned error: %v", err)
	}
	if !strings.Contains(message, "branchapp/session-a") {
		t.Fatalf("message = %q", message)
	}
	registry, _, err := readNeonBranchRegistryForDefaultRoot()
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var foundA, foundB bool
	for _, lease := range registry.Leases {
		foundA = foundA || lease.Pin.SessionID == "session-a"
		foundB = foundB || lease.Pin.SessionID == "session-b"
	}
	if foundA || !foundB {
		t.Fatalf("registry after selected-session cleanup = %+v", registry.Leases)
	}
	current, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok || current.SessionID != "session-b" {
		t.Fatalf("current pin after selected-session cleanup ok=%v err=%v pin=%+v", ok, err, current)
	}

	message, err = dropSessionManagedDatabase(t.Context(), root, localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("second dropSessionManagedDatabase returned error: %v", err)
	}
	if !strings.Contains(message, "no local Neon branch lease") {
		t.Fatalf("second message = %q", message)
	}
	current, ok, err = readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok || current.SessionID != "session-b" {
		t.Fatalf("current pin after no-op cleanup ok=%v err=%v pin=%+v", ok, err, current)
	}
}

func TestDBBranchResetAndDeleteGuards(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"branchapp","dev":{"services":{"postgres":{"kind":"neon","mode":"self-hosted","isolation":"branch","project":"branchapp"}}}}`)
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-selfhost",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "main",
		"branch_id": "br-local-parent",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)

	err := runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `refusing to reset protected parent branch "main"`) {
		t.Fatalf("reset parent error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "main", "--app-root", root, "--force"})
	if err == nil || !strings.Contains(err.Error(), `refusing to delete protected parent branch "main"`) {
		t.Fatalf("delete parent error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `refusing to restore protected parent branch "main"`) {
		t.Fatalf("restore parent error = %v", err)
	}

	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-selfhost",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "branchapp/feature",
		"branch_id": "br-local-feature",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "branchapp/feature", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `without --force`) {
		t.Fatalf("delete current error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `requires --yes`) {
		t.Fatalf("reset yes error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires --at`) {
		t.Fatalf("restore at error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `requires --yes`) {
		t.Fatalf("restore yes error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("reset preflight error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("restore preflight error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "--app-root", root})
	if err == nil || !strings.Contains(err.Error(), `usage: onlava db branch diff <branch>`) {
		t.Fatalf("diff usage error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "main", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), `requires generated Neon dev-cell readiness`) {
		t.Fatalf("diff preflight error = %v", err)
	}

	useMissingNeonDocker(t)
	if err := runDBNeonCommand(t.Context(), io.Discard, []string{"install", "--json"}); err != nil {
		t.Fatalf("install dev-cell returned error: %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"reset", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `no Neon branch driver is configured`) {
		t.Fatalf("reset backend error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"restore", "--at", "2026-06-08T00:00:00Z", "--app-root", root, "--yes"})
	if err == nil || !strings.Contains(err.Error(), `no Neon branch driver is configured`) {
		t.Fatalf("restore backend error = %v", err)
	}
	err = runDBBranchCommand(t.Context(), io.Discard, []string{"diff", "main", "--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), `no Neon branch driver is configured`) {
		t.Fatalf("diff backend error = %v", err)
	}
}

func TestEnsureNeonBranchPinForSessionDerivesWorktreeBranch(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := appcfg.Config{
		Name: "Branch App",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {
				Kind:               "neon",
				Mode:               "self-hosted",
				Isolation:          "branch",
				Project:            "Branch App",
				BranchNameTemplate: "{app}/{git_branch}",
			},
		}},
	}
	resolution, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{
		SessionID: "session-a",
		BaseAppID: "branch-app",
		Branch:    "Feature/API",
	})
	if err != nil {
		t.Fatalf("ensureNeonBranchPinForSession returned error: %v", err)
	}
	if !resolution.Created || resolution.Source != "worktree" {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.Pin.Branch != "branch-app/feature/api" || resolution.Pin.SessionID != "session-a" {
		t.Fatalf("pin = %+v", resolution.Pin)
	}
	if resolution.BackendStatus.Status != "missing" || !strings.Contains(resolution.BackendStatus.Message, "dev-cell is not installed") {
		t.Fatalf("backend status = %+v, want missing dev-cell", resolution.BackendStatus)
	}
	if _, err := os.Stat(filepath.Join(root, ".onlava", "worktree-db.json")); err != nil {
		t.Fatalf("pin not written: %v", err)
	}
}

func TestEnsureNeonBranchPinForSessionRewritesStaleSessionPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	cfg := appcfg.Config{
		Name: "demo",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {
				Kind:               "neon",
				Mode:               "self-hosted",
				Isolation:          "branch",
				Project:            "demo",
				BranchPolicy:       "session",
				BranchNameTemplate: "{app}/{session}",
			},
		}},
	}
	first, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-a", BaseAppID: "demo"})
	if err != nil {
		t.Fatalf("first ensure returned error: %v", err)
	}
	second, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-b", BaseAppID: "demo"})
	if err != nil {
		t.Fatalf("second ensure returned error: %v", err)
	}
	if !second.Created || second.Pin.Branch == first.Pin.Branch || second.Pin.SessionID != "session-b" {
		t.Fatalf("second resolution = %+v, first = %+v", second, first)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(root))
	if err != nil || !ok {
		t.Fatalf("read rewritten pin ok=%v err=%v", ok, err)
	}
	if pin.Branch != "demo/session-b" || pin.SessionID != "session-b" {
		t.Fatalf("rewritten pin = %+v", pin)
	}
}

func TestDBBranchDeleteAndExpireScopeUnpinnedTargetToAppProject(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
		"name": "app-a",
		"dev": {
			"services": {
				"postgres": {
					"kind": "neon",
					"mode": "self-hosted",
					"isolation": "branch",
					"project": "app-a"
				}
			}
		}
	}`)
	pinA := worktreeDBPin{SchemaVersion: dbBranchPinSchemaVersion, Provider: neonSelfhostProvider, Project: "app-a", ParentBranch: "main", Branch: "shared", BranchID: neonLocalBranchID("app-a", "shared"), Database: "app_a", Role: "cloud_admin", CreatedBy: "onlava"}
	pinB := worktreeDBPin{SchemaVersion: dbBranchPinSchemaVersion, Provider: neonSelfhostProvider, Project: "app-b", ParentBranch: "main", Branch: "shared", BranchID: neonLocalBranchID("app-b", "shared"), Database: "app_b", Role: "cloud_admin", CreatedBy: "onlava"}
	home, err := neonSubstrateRoot()
	if err != nil {
		t.Fatalf("neonSubstrateRoot: %v", err)
	}
	if err := writeNeonBranchRegistry(home, neonBranchRegistry{
		SchemaVersion: dbBranchRegistrySchemaVersion,
		Provider:      neonSelfhostProvider,
		Leases: []neonBranchLease{
			{Pin: pinA, Status: "pending"},
			{Pin: pinB, Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"expire", "shared", "--after", "1h", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("expire returned error: %v", err)
	}
	registry, err := readNeonBranchRegistry(home)
	if err != nil {
		t.Fatalf("read registry after expire: %v", err)
	}
	if registry.Leases[0].ExpiresAt == "" || registry.Leases[1].ExpiresAt != "" {
		t.Fatalf("expire scope leaked across projects: %+v", registry.Leases)
	}
	if err := runDBBranchCommand(t.Context(), io.Discard, []string{"delete", "shared", "--app-root", root, "--force"}); err != nil {
		t.Fatalf("delete returned error: %v", err)
	}
	registry, err = readNeonBranchRegistry(home)
	if err != nil {
		t.Fatalf("read registry after delete: %v", err)
	}
	if len(registry.Leases) != 1 || registry.Leases[0].Pin.Project != "app-b" {
		t.Fatalf("delete scope leaked across projects: %+v", registry.Leases)
	}
}

func TestEnsureNeonBranchPinForSessionReusesExistingPin(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava/worktree-db.json", `{
		"schema_version": "onlava.db.branch.v1",
		"provider": "neon-selfhost",
		"project": "branchapp",
		"parent_branch": "main",
		"branch": "branchapp/manual",
		"branch_id": "br-local-manual",
		"database": "branchapp",
		"role": "cloud_admin",
		"created_by": "onlava"
	}`)
	cfg := appcfg.Config{
		Name: "branchapp",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "manual"},
		}},
	}
	resolution, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("ensureNeonBranchPinForSession returned error: %v", err)
	}
	if resolution.Created || resolution.Source != "pin" || resolution.Pin.Branch != "branchapp/manual" {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.BackendStatus.Status != "missing" || !strings.Contains(resolution.BackendStatus.Message, "dev-cell is not installed") {
		t.Fatalf("backend status = %+v, want missing dev-cell", resolution.BackendStatus)
	}
}

func TestEnsureNeonBranchPinForSessionManualRequiresPin(t *testing.T) {
	root := t.TempDir()
	cfg := appcfg.Config{
		Name: "branchapp",
		Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "manual"},
		}},
	}
	_, err := ensureNeonBranchPinForSession(t.Context(), root, cfg, &localagent.Session{SessionID: "session-a"})
	if err == nil || !strings.Contains(err.Error(), "requires `onlava db branch checkout <name>`") {
		t.Fatalf("manual policy error = %v", err)
	}
}

func TestParseDBBranchArgsRequiresKnownCommand(t *testing.T) {
	t.Parallel()

	if _, err := parseDBBranchArgs([]string{"status", "--json"}); err != nil {
		t.Fatalf("parseDBBranchArgs status returned error: %v", err)
	}
	if _, err := parseDBBranchArgs([]string{"unknown"}); err == nil || err.Error() != `unknown db branch command "unknown"` {
		t.Fatalf("unknown command error = %v", err)
	}
}
