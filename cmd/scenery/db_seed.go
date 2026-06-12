package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
)

type dbSeedOptions struct {
	AppRoot string
	DryRun  bool
	JSON    bool
}

type dbSeedResult struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	DryRun        bool               `json:"dry_run"`
	Seeds         []dbSeedRecord     `json:"seeds"`
	Summary       dbSeedSummary      `json:"summary"`
}

type dbSeedRecord struct {
	Service     string                   `json:"service"`
	Path        string                   `json:"path"`
	SHA256      string                   `json:"sha256"`
	Status      string                   `json:"status"`
	Error       string                   `json:"error,omitempty"`
	Diagnostics []dbSeedSafetyDiagnostic `json:"diagnostics,omitempty"`
}

type dbSeedSummary struct {
	Planned int `json:"planned"`
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	Changed int `json:"changed"`
	Failed  int `json:"failed"`
}

type dbSeedPlan struct {
	Service string
	Path    string
	SQL     string
	SHA256  string
}

type dbSeedSafetyDiagnostic struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Context string `json:"context"`
}

type databaseSeedStore interface {
	Close(context.Context) error
	EnsureLedger(context.Context) error
	LookupSeed(context.Context, string, string) (string, bool, error)
	ApplySeed(context.Context, string, string, string, string) error
}

var openDatabaseSeedStore = func(ctx context.Context, databaseURL string) (databaseSeedStore, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &pgxDatabaseSeedStore{conn: conn}, nil
}

func dbSeedCommand(args []string) error {
	return runDBSeed(context.Background(), os.Stdout, args)
}

func runDBSeed(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDBSeedArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := buildDBSeedResult(ctx, appRoot, cfg, opts)
	if opts.JSON {
		if writeErr := writeInspectJSON(stdout, result); writeErr != nil {
			return writeErr
		}
	} else {
		renderDBSeedText(stdout, result)
	}
	if err != nil {
		return err
	}
	return nil
}

func parseDBSeedArgs(args []string) (dbSeedOptions, error) {
	var opts dbSeedOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbSeedOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--dry-run":
			opts.DryRun = true
		case "--json":
			opts.JSON = true
		default:
			return dbSeedOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func buildDBSeedResult(ctx context.Context, appRoot string, cfg appcfg.Config, opts dbSeedOptions) (dbSeedResult, error) {
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		result := emptyDBSeedResult(appRoot, cfg, opts)
		return result, err
	}
	return buildDBSeedResultWithEnv(ctx, appRoot, cfg, opts, baseEnv, true)
}

func buildDBSeedResultWithEnv(ctx context.Context, appRoot string, cfg appcfg.Config, opts dbSeedOptions, baseEnv []string, useManaged bool) (dbSeedResult, error) {
	result := dbSeedResult{
		SchemaVersion: "scenery.db.seed.result.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".scenery.json"),
		},
		DryRun: opts.DryRun,
		Seeds:  []dbSeedRecord{},
	}
	plans, err := discoverDBSeedPlans(appRoot, cfg)
	if err != nil {
		return result, err
	}
	if len(plans) == 0 {
		return result, nil
	}
	safetyDiagnostics := validateDBSeedSafety(plans)
	if len(safetyDiagnostics) > 0 {
		var errs []error
		unsafePaths := make([]string, 0, len(safetyDiagnostics))
		for path := range safetyDiagnostics {
			unsafePaths = append(unsafePaths, path)
		}
		sort.Strings(unsafePaths)
		for _, path := range unsafePaths {
			diagnostics := safetyDiagnostics[path]
			record := dbSeedRecord{
				Path:        path,
				SHA256:      seedPlanSHAForPath(plans, path),
				Status:      "failed",
				Error:       diagnostics[0].Message,
				Diagnostics: diagnostics,
			}
			if plan, ok := seedPlanForPath(plans, path); ok {
				record.Service = plan.Service
			}
			result.addSeedRecord(record)
			for _, diag := range diagnostics {
				errs = append(errs, fmt.Errorf("%s:%d: %s", diag.Path, diag.Line, diag.Message))
			}
		}
		return result, errors.Join(errs...)
	}
	dsn, err := resolveDatabaseURLForConfig(ctx, appRoot, cfg, baseEnv, useManaged)
	if err != nil {
		return result, err
	}
	store, err := openDatabaseSeedStore(ctx, dsn)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = store.Close(context.Background())
	}()
	if err := store.EnsureLedger(ctx); err != nil {
		return result, err
	}
	var errs []error
	appID := cfg.AppID()
	for _, plan := range plans {
		record := dbSeedRecord{
			Service: plan.Service,
			Path:    plan.Path,
			SHA256:  plan.SHA256,
		}
		prior, ok, err := store.LookupSeed(ctx, appID, plan.Path)
		if err != nil {
			record.Status = "failed"
			record.Error = err.Error()
			result.addSeedRecord(record)
			errs = append(errs, fmt.Errorf("lookup seed %s: %w", plan.Path, err))
			continue
		}
		if ok && prior == plan.SHA256 {
			record.Status = "skipped"
			result.addSeedRecord(record)
			continue
		}
		if ok {
			record.Status = "changed"
			record.Error = "seed was previously applied with a different sha256"
			result.addSeedRecord(record)
			errs = append(errs, fmt.Errorf("seed %s changed after it was applied", plan.Path))
			continue
		}
		if opts.DryRun {
			record.Status = "planned"
			result.addSeedRecord(record)
			continue
		}
		if err := store.ApplySeed(ctx, appID, plan.Path, plan.SHA256, plan.SQL); err != nil {
			record.Status = "failed"
			record.Error = err.Error()
			result.addSeedRecord(record)
			errs = append(errs, fmt.Errorf("apply seed %s: %w", plan.Path, err))
			continue
		}
		record.Status = "applied"
		result.addSeedRecord(record)
	}
	return result, errors.Join(errs...)
}

func emptyDBSeedResult(appRoot string, cfg appcfg.Config, opts dbSeedOptions) dbSeedResult {
	return dbSeedResult{
		SchemaVersion: "scenery.db.seed.result.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".scenery.json"),
		},
		DryRun: opts.DryRun,
		Seeds:  []dbSeedRecord{},
	}
}

func discoverDBSeedPlans(appRoot string, cfg appcfg.Config) ([]dbSeedPlan, error) {
	graph, err := buildInspectGeneratorsResponse(appRoot, cfg)
	if err != nil {
		return nil, err
	}
	var plans []dbSeedPlan
	for _, artifact := range graph.DBArtifacts {
		if artifact.Kind != "seed" || artifact.Role != "initial-data" {
			continue
		}
		path := filepath.Join(appRoot, filepath.FromSlash(artifact.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		plans = append(plans, dbSeedPlan{
			Service: artifact.Service,
			Path:    artifact.Path,
			SQL:     string(data),
			SHA256:  hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].Service != plans[j].Service {
			return plans[i].Service < plans[j].Service
		}
		return plans[i].Path < plans[j].Path
	})
	return plans, nil
}

var (
	seedDropPattern         = regexp.MustCompile(`(?is)(^|;)\s*drop\s+`)
	seedTruncatePattern     = regexp.MustCompile(`(?is)(^|;)\s*truncate\s+`)
	seedDeletePattern       = regexp.MustCompile(`(?is)(^|;)\s*delete\s+from\s+`)
	seedDeleteWherePattern  = regexp.MustCompile(`(?is)\bwhere\s+`)
	seedDeleteBroadPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)\bwhere\s+true\b`),
		regexp.MustCompile(`(?is)\bwhere\s+1\s*=\s*1\b`),
	}
)

func validateDBSeedSafety(plans []dbSeedPlan) map[string][]dbSeedSafetyDiagnostic {
	diagnostics := map[string][]dbSeedSafetyDiagnostic{}
	for _, plan := range plans {
		diags := dbSeedSafetyDiagnostics(plan)
		if len(diags) > 0 {
			diagnostics[plan.Path] = append(diagnostics[plan.Path], diags...)
		}
	}
	if len(diagnostics) == 0 {
		return nil
	}
	return diagnostics
}

func dbSeedSafetyDiagnostics(plan dbSeedPlan) []dbSeedSafetyDiagnostic {
	sanitized := sanitizeSQLForSeedSafety(plan.SQL)
	statements := splitSQLStatements(sanitized)
	var diagnostics []dbSeedSafetyDiagnostic
	for _, stmt := range statements {
		text := strings.TrimSpace(stmt.Text)
		if text == "" {
			continue
		}
		switch {
		case seedDropPattern.MatchString(text):
			diagnostics = append(diagnostics, seedSafetyDiagnostic(plan.Path, stmt.Line, "seed contains destructive DROP statement", text))
		case seedTruncatePattern.MatchString(text):
			diagnostics = append(diagnostics, seedSafetyDiagnostic(plan.Path, stmt.Line, "seed contains destructive TRUNCATE statement", text))
		case seedDeletePattern.MatchString(text) && broadSeedDelete(text):
			diagnostics = append(diagnostics, seedSafetyDiagnostic(plan.Path, stmt.Line, "seed contains broad DELETE statement", text))
		}
	}
	return diagnostics
}

func broadSeedDelete(statement string) bool {
	if !seedDeleteWherePattern.MatchString(statement) {
		return true
	}
	for _, pattern := range seedDeleteBroadPatterns {
		if pattern.MatchString(statement) {
			return true
		}
	}
	return false
}

func seedSafetyDiagnostic(path string, line int, message, context string) dbSeedSafetyDiagnostic {
	context = strings.Join(strings.Fields(context), " ")
	if len(context) > 160 {
		context = context[:157] + "..."
	}
	return dbSeedSafetyDiagnostic{
		Path:    path,
		Line:    line,
		Message: message,
		Context: context,
	}
}

type dbSeedSQLStatement struct {
	Text string
	Line int
}

func splitSQLStatements(sql string) []dbSeedSQLStatement {
	var statements []dbSeedSQLStatement
	startLine := 1
	line := 1
	start := 0
	for i, r := range sql {
		if r == ';' {
			statements = append(statements, dbSeedSQLStatement{Text: sql[start : i+1], Line: startLine})
			start = i + 1
			startLine = line
			continue
		}
		if r == '\n' {
			line++
			if strings.TrimSpace(sql[start:i+1]) == "" {
				startLine = line
			}
		}
	}
	if start < len(sql) {
		statements = append(statements, dbSeedSQLStatement{Text: sql[start:], Line: startLine})
	}
	return statements
}

func sanitizeSQLForSeedSafety(sql string) string {
	var out strings.Builder
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			out.WriteString("  ")
			i += 2
			for i < len(sql) && sql[i] != '\n' {
				out.WriteByte(' ')
				i++
			}
		case strings.HasPrefix(sql[i:], "/*"):
			out.WriteString("  ")
			i += 2
			for i < len(sql) {
				if strings.HasPrefix(sql[i:], "*/") {
					out.WriteString("  ")
					i += 2
					break
				}
				writeSeedSafetyBlank(&out, sql[i])
				i++
			}
		case sql[i] == '\'':
			out.WriteByte(' ')
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					out.WriteByte(' ')
					i++
					if i < len(sql) && sql[i] == '\'' {
						out.WriteByte(' ')
						i++
						continue
					}
					break
				}
				writeSeedSafetyBlank(&out, sql[i])
				i++
			}
		case sql[i] == '"':
			out.WriteByte(' ')
			i++
			for i < len(sql) {
				if sql[i] == '"' {
					out.WriteByte(' ')
					i++
					if i < len(sql) && sql[i] == '"' {
						out.WriteByte(' ')
						i++
						continue
					}
					break
				}
				writeSeedSafetyBlank(&out, sql[i])
				i++
			}
		case sql[i] == '$':
			tagEnd := seedDollarTagEnd(sql, i)
			if tagEnd == -1 {
				out.WriteByte(sql[i])
				i++
				continue
			}
			tag := sql[i : tagEnd+1]
			out.WriteString(strings.Repeat(" ", len(tag)))
			i = tagEnd + 1
			closeAt := strings.Index(sql[i:], tag)
			if closeAt == -1 {
				for i < len(sql) {
					writeSeedSafetyBlank(&out, sql[i])
					i++
				}
				continue
			}
			bodyEnd := i + closeAt
			for i < bodyEnd {
				writeSeedSafetyBlank(&out, sql[i])
				i++
			}
			out.WriteString(strings.Repeat(" ", len(tag)))
			i += len(tag)
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	return out.String()
}

func writeSeedSafetyBlank(out *strings.Builder, b byte) {
	if b == '\n' {
		out.WriteByte('\n')
		return
	}
	out.WriteByte(' ')
}

func seedDollarTagEnd(sql string, start int) int {
	if start >= len(sql) || sql[start] != '$' {
		return -1
	}
	for i := start + 1; i < len(sql); i++ {
		switch c := sql[i]; {
		case c == '$':
			return i
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			continue
		default:
			return -1
		}
	}
	return -1
}

func seedPlanForPath(plans []dbSeedPlan, path string) (dbSeedPlan, bool) {
	for _, plan := range plans {
		if plan.Path == path {
			return plan, true
		}
	}
	return dbSeedPlan{}, false
}

func seedPlanSHAForPath(plans []dbSeedPlan, path string) string {
	if plan, ok := seedPlanForPath(plans, path); ok {
		return plan.SHA256
	}
	return ""
}

func (r *dbSeedResult) addSeedRecord(record dbSeedRecord) {
	r.Seeds = append(r.Seeds, record)
	switch record.Status {
	case "planned":
		r.Summary.Planned++
	case "applied":
		r.Summary.Applied++
	case "skipped":
		r.Summary.Skipped++
	case "changed":
		r.Summary.Changed++
	case "failed":
		r.Summary.Failed++
	}
}

func renderDBSeedText(stdout io.Writer, result dbSeedResult) {
	if len(result.Seeds) == 0 {
		fmt.Fprintln(stdout, "scenery: no seed files discovered")
		return
	}
	for _, seed := range result.Seeds {
		line := fmt.Sprintf("%s %s", seed.Status, seed.Path)
		if seed.Error != "" {
			line += ": " + seed.Error
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "scenery: database seed complete; planned=%d applied=%d skipped=%d changed=%d failed=%d\n",
		result.Summary.Planned,
		result.Summary.Applied,
		result.Summary.Skipped,
		result.Summary.Changed,
		result.Summary.Failed,
	)
}

type pgxDatabaseSeedStore struct {
	conn *pgx.Conn
}

func (s *pgxDatabaseSeedStore) Close(ctx context.Context) error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close(ctx)
}

func (s *pgxDatabaseSeedStore) EnsureLedger(ctx context.Context) error {
	_, err := s.conn.Exec(ctx, `
create schema if not exists scenery_internal;
create table if not exists scenery_internal.seed_runs (
  app_id text not null,
  path text not null,
  sha256 text not null,
  applied_at timestamptz not null default now(),
  primary key (app_id, path)
)`)
	return err
}

func (s *pgxDatabaseSeedStore) LookupSeed(ctx context.Context, appID, path string) (string, bool, error) {
	var hash string
	err := s.conn.QueryRow(ctx, `select sha256 from scenery_internal.seed_runs where app_id = $1 and path = $2`, appID, path).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

func (s *pgxDatabaseSeedStore) ApplySeed(ctx context.Context, appID, path, hash, sql string) error {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if strings.TrimSpace(sql) != "" {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into scenery_internal.seed_runs (app_id, path, sha256) values ($1, $2, $3)`, appID, path, hash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
