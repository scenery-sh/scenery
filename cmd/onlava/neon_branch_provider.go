package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"

	appcfg "github.com/pbrazdil/onlava/internal/app"
)

const neonRestorePointsSchemaVersion = "onlava.db.neon.restore_points.v1"

type neonBranchProvider interface {
	EnsureProject(ctx context.Context, spec neonBranchSpec) (string, error)
	EnsureParentBranch(ctx context.Context, spec neonBranchSpec) (neonBranchLease, error)
	EnsureBranch(ctx context.Context, spec neonBranchSpec) (neonBranchLease, error)
	CheckoutBranch(ctx context.Context, spec neonBranchSpec, ref string) (neonBranchLease, error)
	ResetBranch(ctx context.Context, lease neonBranchLease) error
	RestoreBranch(ctx context.Context, lease neonBranchLease, at string) (neonBranchRestorePoint, error)
	DeleteBranch(ctx context.Context, lease neonBranchLease) error
	ExpireBranch(ctx context.Context, lease neonBranchLease, after time.Duration) (neonBranchLease, error)
	DiffBranch(ctx context.Context, current, target neonBranchLease) (string, error)
	Connection(ctx context.Context, lease neonBranchLease) (string, error)
	Inspect(ctx context.Context) (neonCellState, error)
}

type neonSelfHostedBranchProvider struct {
	adminURL string
}

type neonBranchRestorePoint struct {
	Ref          string    `json:"ref"`
	BranchID     string    `json:"branch_id"`
	Branch       string    `json:"branch"`
	Project      string    `json:"project"`
	DatabaseName string    `json:"database_name"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

type neonRestorePointsState struct {
	SchemaVersion string                              `json:"schema_version"`
	Points        map[string][]neonBranchRestorePoint `json:"points"`
	UpdatedAt     time.Time                           `json:"updated_at"`
}

func newNeonSelfHostedBranchProvider(adminURL string) neonBranchProvider {
	return neonSelfHostedBranchProvider{adminURL: adminURL}
}

func (p neonSelfHostedBranchProvider) EnsureProject(_ context.Context, spec neonBranchSpec) (string, error) {
	if strings.TrimSpace(spec.Project) == "" {
		return "", fmt.Errorf("Neon project is required")
	}
	return spec.Project, nil
}

func (p neonSelfHostedBranchProvider) EnsureParentBranch(ctx context.Context, spec neonBranchSpec) (neonBranchLease, error) {
	if _, err := p.EnsureProject(ctx, spec); err != nil {
		return neonBranchLease{}, err
	}
	parentSpec := spec
	parentSpec.Branch = spec.ParentBranch
	parentSpec.BranchID = neonBranchID(spec.Project, spec.ParentBranch)
	lease := leaseFromNeonSpec(parentSpec)
	if err := ensureManagedPostgresDatabase(ctx, p.adminURL, lease.DatabaseName); err != nil {
		return neonBranchLease{}, err
	}
	lease.UpdatedAt = time.Now().UTC()
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return neonBranchLease{}, err
	}
	return lease, nil
}

func (p neonSelfHostedBranchProvider) EnsureBranch(ctx context.Context, spec neonBranchSpec) (neonBranchLease, error) {
	if _, err := p.EnsureProject(ctx, spec); err != nil {
		return neonBranchLease{}, err
	}
	if _, err := p.EnsureParentBranch(ctx, spec); err != nil {
		return neonBranchLease{}, err
	}
	lease := leaseFromNeonSpec(spec)
	created := false
	exists, err := postgresDatabaseExists(ctx, p.adminURL, lease.DatabaseName)
	if err != nil {
		return neonBranchLease{}, err
	}
	if lease.Branch == lease.ParentBranch {
		if err := ensureManagedPostgresDatabase(ctx, p.adminURL, lease.DatabaseName); err != nil {
			return neonBranchLease{}, err
		}
		created = !exists
	} else if !exists {
		if err := ensurePostgresDatabaseCloned(ctx, p.adminURL, neonBranchDatabaseName(spec.Project, spec.ParentBranch), lease.DatabaseName); err != nil {
			return neonBranchLease{}, err
		}
		created = true
	}
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return neonBranchLease{}, err
	}
	if created {
		if _, err := ensureNeonRestorePoint(ctx, p.adminURL, lease, "branch-created"); err != nil {
			return neonBranchLease{}, err
		}
	}
	return lease, nil
}

func (p neonSelfHostedBranchProvider) CheckoutBranch(ctx context.Context, spec neonBranchSpec, ref string) (neonBranchLease, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return neonBranchLease{}, fmt.Errorf("Neon branch ref is required")
	}
	if existing, ok := lookupNeonBranchLease(spec.Project, ref); ok {
		if err := p.ensureResolvedBranchDatabase(ctx, existing); err != nil {
			return neonBranchLease{}, err
		}
		existing.UpdatedAt = time.Now().UTC()
		if err := upsertNeonGlobalBranch(existing); err != nil {
			return neonBranchLease{}, err
		}
		return existing, nil
	}
	spec.Branch = sanitizeNeonBranch(ref)
	spec.BranchID = neonBranchID(spec.Project, spec.Branch)
	return p.EnsureBranch(ctx, spec)
}

func (p neonSelfHostedBranchProvider) ResetBranch(ctx context.Context, lease neonBranchLease) error {
	if lease.Branch == lease.ParentBranch {
		return fmt.Errorf("refusing to reset protected parent branch %s", lease.ParentBranch)
	}
	if err := ensurePostgresDatabaseCloned(ctx, p.adminURL, neonBranchDatabaseName(lease.Project, lease.ParentBranch), lease.DatabaseName, true); err != nil {
		return err
	}
	lease.UpdatedAt = time.Now().UTC()
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return err
	}
	_, err := ensureNeonRestorePoint(ctx, p.adminURL, lease, "branch-reset")
	return err
}

func (p neonSelfHostedBranchProvider) RestoreBranch(ctx context.Context, lease neonBranchLease, at string) (neonBranchRestorePoint, error) {
	point, err := resolveNeonRestorePoint(lease.BranchID, at)
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	if err := ensurePostgresDatabaseCloned(ctx, p.adminURL, point.DatabaseName, lease.DatabaseName, true); err != nil {
		return neonBranchRestorePoint{}, err
	}
	lease.UpdatedAt = time.Now().UTC()
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return neonBranchRestorePoint{}, err
	}
	if _, err := ensureNeonRestorePoint(ctx, p.adminURL, lease, "branch-restore"); err != nil {
		return neonBranchRestorePoint{}, err
	}
	return point, nil
}

func (p neonSelfHostedBranchProvider) DeleteBranch(ctx context.Context, lease neonBranchLease) error {
	if lease.Branch == lease.ParentBranch {
		return fmt.Errorf("refusing to delete protected parent branch %s", lease.ParentBranch)
	}
	if err := dropManagedPostgresDatabase(ctx, p.adminURL, lease.DatabaseName); err != nil {
		return err
	}
	if err := deleteNeonRestorePoints(ctx, p.adminURL, lease.BranchID); err != nil {
		return err
	}
	return removeNeonGlobalBranch(lease.BranchID)
}

func (p neonSelfHostedBranchProvider) ExpireBranch(_ context.Context, lease neonBranchLease, after time.Duration) (neonBranchLease, error) {
	if after <= 0 {
		return neonBranchLease{}, fmt.Errorf("Neon branch expiry must be greater than zero")
	}
	lease.TTL = after.String()
	lease.UpdatedAt = time.Now().UTC()
	lease.ExpiresAt = lease.UpdatedAt.Add(after)
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return neonBranchLease{}, err
	}
	return lease, nil
}

func (p neonSelfHostedBranchProvider) DiffBranch(ctx context.Context, current, target neonBranchLease) (string, error) {
	currentURL, err := postgresDatabaseURL(p.adminURL, current.DatabaseName)
	if err != nil {
		return "", err
	}
	targetURL, err := postgresDatabaseURL(p.adminURL, target.DatabaseName)
	if err != nil {
		return "", err
	}
	left, err := neonSchemaSnapshot(ctx, currentURL)
	if err != nil {
		return "", err
	}
	right, err := neonSchemaSnapshot(ctx, targetURL)
	if err != nil {
		return "", err
	}
	return unifiedLineDiff(current.Branch, target.Branch, left, right), nil
}

func (p neonSelfHostedBranchProvider) Connection(_ context.Context, lease neonBranchLease) (string, error) {
	return postgresDatabaseURL(p.adminURL, lease.DatabaseName)
}

func (p neonSelfHostedBranchProvider) Inspect(_ context.Context) (neonCellState, error) {
	return readNeonCellState()
}

func (p neonSelfHostedBranchProvider) ensureResolvedBranchDatabase(ctx context.Context, lease neonBranchLease) error {
	if lease.Branch == lease.ParentBranch {
		return ensureManagedPostgresDatabase(ctx, p.adminURL, lease.DatabaseName)
	}
	return ensurePostgresDatabaseCloned(ctx, p.adminURL, neonBranchDatabaseName(lease.Project, lease.ParentBranch), lease.DatabaseName)
}

func ensurePostgresDatabaseCloned(ctx context.Context, adminURL, sourceDB, targetDB string, force ...bool) error {
	if sourceDB == targetDB {
		return ensureManagedPostgresDatabase(ctx, adminURL, targetDB)
	}
	reset := len(force) > 0 && force[0]
	if err := ensureManagedPostgresDatabase(ctx, adminURL, sourceDB); err != nil {
		return err
	}
	exists, err := postgresDatabaseExists(ctx, adminURL, targetDB)
	if err != nil {
		return err
	}
	if exists && !reset {
		return nil
	}
	return copyPostgresDatabase(ctx, adminURL, sourceDB, targetDB)
}

func postgresDatabaseExists(ctx context.Context, adminURL, dbName string) (bool, error) {
	db, err := openManagedPostgresAdmin(ctx, adminURL, 10*time.Second)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func openManagedPostgresAdmin(ctx context.Context, adminURL string, timeout time.Duration) (*sql.DB, error) {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("managed Postgres admin connection failed: %w", err)
	}
	return db, nil
}

func lookupNeonBranchLease(project, ref string) (neonBranchLease, bool) {
	state, _, err := readNeonBranchesState()
	if err != nil {
		return neonBranchLease{}, false
	}
	project = sanitizeNeonBranchSegment(project)
	ref = strings.TrimSpace(ref)
	for _, lease := range state.Branches {
		if project != "" && lease.Project != project {
			continue
		}
		if lease.BranchID == ref || lease.Branch == sanitizeNeonBranch(ref) {
			return lease, true
		}
	}
	return neonBranchLease{}, false
}

func readNeonRestorePointsState() (neonRestorePointsState, string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return neonRestorePointsState{}, "", err
	}
	path := filepath.Join(paths.AgentDir, "substrates", "neon", "restore-points.json")
	state := neonRestorePointsState{SchemaVersion: neonRestorePointsSchemaVersion, Points: map[string][]neonBranchRestorePoint{}}
	if err := readJSONFile(path, &state); err != nil {
		if os.IsNotExist(err) {
			return state, path, nil
		}
		return neonRestorePointsState{}, "", err
	}
	if state.Points == nil {
		state.Points = map[string][]neonBranchRestorePoint{}
	}
	return state, path, nil
}

func ensureNeonRestorePoint(ctx context.Context, adminURL string, lease neonBranchLease, source string) (neonBranchRestorePoint, error) {
	state, path, err := readNeonRestorePointsState()
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	now := time.Now().UTC()
	ref := now.Format("20060102T150405.000000000Z07:00")
	point := neonBranchRestorePoint{
		Ref:          ref,
		BranchID:     lease.BranchID,
		Branch:       lease.Branch,
		Project:      lease.Project,
		DatabaseName: neonRestorePointDatabaseName(lease.BranchID, ref),
		Source:       source,
		CreatedAt:    now,
	}
	if err := copyPostgresDatabase(ctx, adminURL, lease.DatabaseName, point.DatabaseName); err != nil {
		return neonBranchRestorePoint{}, err
	}
	state.SchemaVersion = neonRestorePointsSchemaVersion
	state.Points[lease.BranchID] = append(state.Points[lease.BranchID], point)
	sort.Slice(state.Points[lease.BranchID], func(i, j int) bool {
		return state.Points[lease.BranchID][i].CreatedAt.Before(state.Points[lease.BranchID][j].CreatedAt)
	})
	state.UpdatedAt = now
	if err := writeJSONFile(path, state, 0o644); err != nil {
		return neonBranchRestorePoint{}, err
	}
	return point, nil
}

func snapshotNeonBranchAfterSetup(ctx context.Context, cfg appcfg.Config, appRoot string) error {
	_, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		return nil
	}
	lease, err := readNeonWorktreeLease(appRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cell, err := ensureNeonDevCell(ctx, cfg, svc)
	if err != nil {
		return err
	}
	_, err = ensureNeonRestorePoint(ctx, cell.AdminURL, lease, "database-setup")
	return err
}

func resolveNeonRestorePoint(branchID, at string) (neonBranchRestorePoint, error) {
	state, _, err := readNeonRestorePointsState()
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	points := append([]neonBranchRestorePoint(nil), state.Points[branchID]...)
	if len(points) == 0 {
		return neonBranchRestorePoint{}, fmt.Errorf("no restore points recorded for Neon branch %s", branchID)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].CreatedAt.Before(points[j].CreatedAt) })
	at = strings.TrimSpace(at)
	if at == "" {
		return points[len(points)-1], nil
	}
	for _, point := range points {
		if point.Ref == at {
			return point, nil
		}
	}
	when, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		when, err = time.Parse(time.RFC3339, at)
	}
	if err != nil {
		return neonBranchRestorePoint{}, fmt.Errorf("unknown Neon restore point %q; use an exact restore point ref or RFC3339 timestamp", at)
	}
	var chosen neonBranchRestorePoint
	for _, point := range points {
		if point.CreatedAt.After(when) {
			break
		}
		chosen = point
	}
	if chosen.Ref == "" {
		return neonBranchRestorePoint{}, fmt.Errorf("no Neon restore point exists at or before %s", at)
	}
	return chosen, nil
}

func deleteNeonRestorePoints(ctx context.Context, adminURL, branchID string) error {
	state, path, err := readNeonRestorePointsState()
	if err != nil {
		return err
	}
	for _, point := range state.Points[branchID] {
		if err := dropManagedPostgresDatabase(ctx, adminURL, point.DatabaseName); err != nil {
			return err
		}
	}
	delete(state.Points, branchID)
	state.UpdatedAt = time.Now().UTC()
	return writeJSONFile(path, state, 0o644)
}

func neonRestorePointDatabaseName(branchID, ref string) string {
	label := postgresIdentifierPart(branchID) + "_" + postgresIdentifierPart(ref)
	label = strings.Trim(label, "_")
	if label == "" {
		label = "onlava_neon_restore"
	}
	if len(label) <= 55 {
		return label
	}
	return label[:55]
}

func neonSchemaSnapshot(ctx context.Context, dsn string) ([]string, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var lines []string
	queries := []struct {
		label string
		sql   string
	}{
		{
			label: "table",
			sql: `SELECT table_schema, table_name
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND table_type = 'BASE TABLE'
ORDER BY table_schema, table_name`,
		},
		{
			label: "column",
			sql: `SELECT table_schema, table_name, column_name, data_type, is_nullable, COALESCE(column_default, '')
FROM information_schema.columns
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY table_schema, table_name, ordinal_position`,
		},
		{
			label: "index",
			sql: `SELECT schemaname, tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
ORDER BY schemaname, tablename, indexname`,
		},
	}
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query.sql)
		if err != nil {
			return nil, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			values := make([]string, len(cols))
			scan := make([]any, len(cols))
			for i := range values {
				scan[i] = &values[i]
			}
			if err := rows.Scan(scan...); err != nil {
				rows.Close()
				return nil, err
			}
			lines = append(lines, query.label+" "+strings.Join(values, " | "))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(lines) == 0 {
		lines = []string{"schema <empty>"}
	}
	return lines, nil
}

func unifiedLineDiff(leftName, rightName string, left, right []string) string {
	if strings.Join(left, "\n") == strings.Join(right, "\n") {
		return fmt.Sprintf("branches %s and %s have identical schema dumps\n", leftName, rightName)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", leftName)
	fmt.Fprintf(&b, "+++ %s\n", rightName)
	max := len(left)
	if len(right) > max {
		max = len(right)
	}
	for i := 0; i < max; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l == r {
			continue
		}
		if l != "" {
			fmt.Fprintf(&b, "-%s\n", l)
		}
		if r != "" {
			fmt.Fprintf(&b, "+%s\n", r)
		}
	}
	return b.String()
}
