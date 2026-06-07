package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

func TestBuildNeonBranchSpecUsesWorktreeTemplate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "Pulse App",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {
				Kind:               "neon",
				Mode:               "self-hosted",
				Isolation:          "branch",
				Project:            "onlv",
				ParentBranch:       "main",
				BranchPolicy:       "worktree",
				BranchNameTemplate: "{app}/{git_branch}/{worktree}",
				TTL:                "24h",
				Database:           "onlv",
				Role:               "cloud_admin",
			},
		}},
	}
	svc := cfg.Dev.Services["postgres"]
	session := &localagent.Session{SessionID: "Pricing Agent", BaseAppID: cfg.AppID(), AppRoot: root, Branch: "Feature/Pricing Agent"}

	spec, err := buildNeonBranchSpec(cfg, svc, session, "postgres://onlava@127.0.0.1/postgres")
	if err != nil {
		t.Fatalf("buildNeonBranchSpec() error = %v", err)
	}
	if spec.Branch != "pulse-app/feature/pricing-agent/"+filepath.Base(root) {
		t.Fatalf("branch = %q", spec.Branch)
	}
	if spec.Project != "onlv" || spec.ParentBranch != "main" || spec.Role != "cloud_admin" || spec.Database != "onlv" {
		t.Fatalf("spec = %+v", spec)
	}
	lease := leaseFromNeonSpec(spec)
	if !strings.HasPrefix(lease.BranchID, "br-local-") {
		t.Fatalf("branch id = %q", lease.BranchID)
	}
	if lease.DatabaseName == "" || len(lease.DatabaseName) > 63 {
		t.Fatalf("database name = %q", lease.DatabaseName)
	}
}

func TestValidateNeonPostgresConfigFailsClosed(t *testing.T) {
	t.Parallel()

	if err := validateNeonPostgresConfig("postgres", app.DevServiceConfig{Kind: "neon", Mode: "hosted", Isolation: "branch"}); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("mode error = %v", err)
	}
	if err := validateNeonPostgresConfig("postgres", app.DevServiceConfig{Kind: "neon", Mode: "self-hosted", Isolation: "database"}); err == nil || !strings.Contains(err.Error(), "isolation") {
		t.Fatalf("isolation error = %v", err)
	}
	if err := validateNeonPostgresConfig("postgres", app.DevServiceConfig{Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "shared"}); err == nil || !strings.Contains(err.Error(), "branch_policy") {
		t.Fatalf("branch policy error = %v", err)
	}
	if err := validateNeonPostgresConfig("postgres", app.DevServiceConfig{Kind: "neon", Mode: "self-hosted", Isolation: "branch", BranchPolicy: "session"}); err != nil {
		t.Fatalf("valid config error = %v", err)
	}
}

func TestNeonWorktreeLeaseRoundTrip(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lease := neonBranchLease{
		SchemaVersion: neonWorktreeBranchSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Project:       "onlv",
		ParentBranch:  "main",
		Branch:        "onlv/pricing-agent",
		BranchID:      "br-local-abc123",
		Database:      "onlv",
		Role:          "cloud_admin",
		CreatedBy:     "onlava",
		DatabaseName:  "onlv_pricing_agent",
	}
	if err := writeNeonWorktreeLease(root, lease); err != nil {
		t.Fatalf("writeNeonWorktreeLease() error = %v", err)
	}
	got, err := readNeonWorktreeLease(root)
	if err != nil {
		t.Fatalf("readNeonWorktreeLease() error = %v", err)
	}
	if got.Branch != lease.Branch || got.BranchID != lease.BranchID || got.DatabaseName != lease.DatabaseName {
		t.Fatalf("lease = %+v", got)
	}
}

func TestNeonFixtureConfigLoads(t *testing.T) {
	t.Parallel()

	root := filepath.Join(app.RepoRoot(), "testdata", "apps", "neon-basic")
	_, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		t.Fatalf("DiscoverRoot(neon-basic) error = %v", err)
	}
	_, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		t.Fatalf("managedPostgresUsesNeon() = false")
	}
	if err := validateNeonPostgresConfig("postgres", svc); err != nil {
		t.Fatalf("validateNeonPostgresConfig() error = %v", err)
	}
}

func TestParseDBBranchArgsRequiresDestructiveFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseDBBranchArgs([]string{"feature/demo", "--app-root", "/tmp/app", "--force", "--yes", "--json"}, true)
	if err != nil {
		t.Fatalf("parseDBBranchArgs() error = %v", err)
	}
	if opts.Name != "feature/demo" || opts.AppRoot != "/tmp/app" || !opts.Force || !opts.Yes || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseDBBranchArgsParsesRestoreAndExpireFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseDBBranchArgs([]string{"feature/demo", "--app-root", "/tmp/app", "--at", "2026-06-08T00:00:00Z", "--after", "24h", "--older-than", "7d", "--json"}, true)
	if err != nil {
		t.Fatalf("parseDBBranchArgs() error = %v", err)
	}
	if opts.Name != "feature/demo" || opts.AppRoot != "/tmp/app" || opts.At != "2026-06-08T00:00:00Z" || opts.After != 24*time.Hour || opts.OlderThan != 7*24*time.Hour || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestResolveNeonTargetLeaseSupportsBranchIDAndParent(t *testing.T) {

	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	target := neonBranchLease{
		SchemaVersion: neonWorktreeBranchSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Project:       "onlv",
		ParentBranch:  "main",
		Branch:        "onlv/pricing-agent",
		BranchID:      neonBranchID("onlv", "onlv/pricing-agent"),
		Database:      "onlv",
		Role:          "cloud_admin",
		CreatedBy:     "onlava",
		DatabaseName:  neonBranchDatabaseName("onlv", "onlv/pricing-agent"),
	}
	if err := upsertNeonGlobalBranch(target); err != nil {
		t.Fatalf("upsertNeonGlobalBranch() error = %v", err)
	}
	current := neonBranchLease{
		SchemaVersion: neonWorktreeBranchSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Project:       "onlv",
		ParentBranch:  "main",
		Branch:        "onlv/current",
		BranchID:      neonBranchID("onlv", "onlv/current"),
		Database:      "onlv",
		Role:          "cloud_admin",
		CreatedBy:     "onlava",
		DatabaseName:  neonBranchDatabaseName("onlv", "onlv/current"),
	}

	got, err := resolveNeonTargetLease(current, target.BranchID)
	if err != nil {
		t.Fatalf("resolveNeonTargetLease(branchID) error = %v", err)
	}
	if got.Branch != target.Branch || got.DatabaseName != target.DatabaseName {
		t.Fatalf("resolved target = %+v", got)
	}
	parent, err := resolveNeonTargetLease(current, current.ParentBranch)
	if err != nil {
		t.Fatalf("resolveNeonTargetLease(parent) error = %v", err)
	}
	if parent.Branch != "main" || parent.DatabaseName != neonBranchDatabaseName("onlv", "main") {
		t.Fatalf("parent = %+v", parent)
	}
}

func TestNeonBranchStatusResultUsesConfiguredEnvAndRedactsConnection(t *testing.T) {

	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	cell := neonCellState{
		SchemaVersion: neonCellSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Mode:          neonDefaultMode,
		Status:        "ready",
		Backend:       "postgres-compatible-local-cell",
		Version:       "18",
		Root:          filepath.Join(paths.AgentDir, "substrates", "neon"),
		ComposePath:   filepath.Join(paths.AgentDir, "substrates", "neon", "compose.generated.yml"),
		AdminURL:      "postgres://cloud_admin:secret@127.0.0.1:55432/postgres?sslmode=disable",
		Components:    map[string]neonComponent{},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := writeJSONFile(filepath.Join(paths.AgentDir, "substrates", "neon", "cell.json"), cell, 0o644); err != nil {
		t.Fatalf("writeJSONFile(cell) error = %v", err)
	}
	cfg := app.Config{
		Name: "Demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"postgres": {Kind: "neon", Mode: "self-hosted", Isolation: "branch", DatabaseURLEnv: "APP_DATABASE_URL"},
		}},
	}
	lease := neonBranchLease{
		SchemaVersion: neonWorktreeBranchSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Project:       "onlv",
		ParentBranch:  "main",
		Branch:        "onlv/pricing-agent",
		BranchID:      neonBranchID("onlv", "onlv/pricing-agent"),
		Database:      "onlv",
		Role:          "cloud_admin",
		CreatedBy:     "onlava",
		TTL:           "24h",
		DatabaseName:  neonBranchDatabaseName("onlv", "onlv/pricing-agent"),
		ExpiresAt:     time.Now().UTC().Add(24 * time.Hour).Round(time.Second),
	}

	result := neonBranchStatusResultFor("/tmp/onlv", cfg, lease, "ready")
	if result.Database.DatabaseURLEnv != "APP_DATABASE_URL" {
		t.Fatalf("database_url_env = %q", result.Database.DatabaseURLEnv)
	}
	if !strings.Contains(result.Database.Connection, "xxxxx") || strings.Contains(result.Database.Connection, "secret") {
		t.Fatalf("connection = %q", result.Database.Connection)
	}
	for _, command := range []string{result.Database.ResetCommand, result.Database.RestoreCommand, result.Database.DiffCommand, result.Database.ExpireCommand} {
		if command == "" {
			t.Fatalf("missing command in status: %+v", result.Database)
		}
	}
}

func TestResolveNeonRestorePointUsesLatestAtOrBeforeTimestamp(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	first := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Hour)
	state := neonRestorePointsState{
		SchemaVersion: neonRestorePointsSchemaVersion,
		Points: map[string][]neonBranchRestorePoint{
			"br-local-demo": {
				{Ref: "20260608T100000Z", BranchID: "br-local-demo", Branch: "onlv/demo", DatabaseName: "restore_a", Source: "branch-created", CreatedAt: first},
				{Ref: "20260608T120000Z", BranchID: "br-local-demo", Branch: "onlv/demo", DatabaseName: "restore_b", Source: "database-setup", CreatedAt: second},
			},
		},
		UpdatedAt: second,
	}
	if err := writeJSONFile(filepath.Join(paths.AgentDir, "substrates", "neon", "restore-points.json"), state, 0o644); err != nil {
		t.Fatalf("writeJSONFile(restore-points) error = %v", err)
	}

	got, err := resolveNeonRestorePoint("br-local-demo", "2026-06-08T11:00:00Z")
	if err != nil {
		t.Fatalf("resolveNeonRestorePoint(timestamp) error = %v", err)
	}
	if got.Ref != "20260608T100000Z" {
		t.Fatalf("restore point = %+v", got)
	}
	got, err = resolveNeonRestorePoint("br-local-demo", "20260608T120000Z")
	if err != nil {
		t.Fatalf("resolveNeonRestorePoint(ref) error = %v", err)
	}
	if got.DatabaseName != "restore_b" {
		t.Fatalf("restore point = %+v", got)
	}
}
