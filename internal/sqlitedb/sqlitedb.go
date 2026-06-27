package sqlitedb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"scenery.sh/internal/app"
)

const (
	DriverName = "sqlite"

	ModeLocal   Mode = "local"
	ModeSession Mode = "session"
	ModeBranch  Mode = "branch"
)

type Mode string

type Service struct {
	Name            string `json:"service"`
	FileLabel       string `json:"file_label"`
	Path            string `json:"path"`
	URL             string `json:"url"`
	DatabaseURLEnv  string `json:"database_url_env"`
	DatabasePathEnv string `json:"database_path_env"`
}

type ResolveRequest struct {
	AppRoot   string
	Config    app.Config
	SessionID string
	BranchID  string
	Mode      Mode
}

func ResolveServices(req ResolveRequest) ([]Service, error) {
	root, err := filepath.Abs(req.AppRoot)
	if err != nil {
		return nil, err
	}
	base, err := baseDir(root, req)
	if err != nil {
		return nil, err
	}
	cfgs := req.Config.SQLiteServices()
	out := make([]Service, 0, len(cfgs))
	seenEnv := map[string]string{}
	for _, cfg := range cfgs {
		if cfg.FileLabel == "" {
			return nil, fmt.Errorf("sqlite service %q has an empty file label", cfg.Name)
		}
		if prev := seenEnv[cfg.DatabaseURLEnv]; prev != "" {
			return nil, fmt.Errorf("sqlite services %q and %q use the same database_url_env %q", prev, cfg.Name, cfg.DatabaseURLEnv)
		}
		seenEnv[cfg.DatabaseURLEnv] = cfg.Name
		fileName := cfg.FileLabel
		if !strings.HasSuffix(strings.ToLower(fileName), ".sqlite") && !strings.HasSuffix(strings.ToLower(fileName), ".db") {
			fileName += ".sqlite"
		}
		path := filepath.Join(base, fileName)
		out = append(out, Service{
			Name:            cfg.Name,
			FileLabel:       cfg.FileLabel,
			Path:            path,
			URL:             URLForPath(path),
			DatabaseURLEnv:  cfg.DatabaseURLEnv,
			DatabasePathEnv: cfg.DatabasePathEnv,
		})
	}
	return out, nil
}

func ResolveService(req ResolveRequest, name string) (Service, error) {
	services, err := ResolveServices(req)
	if err != nil {
		return Service{}, err
	}
	if name == "" && len(services) == 1 {
		return services[0], nil
	}
	for _, svc := range services {
		if svc.Name == name {
			return svc, nil
		}
	}
	if name == "" {
		return Service{}, fmt.Errorf("sqlite service name is required")
	}
	return Service{}, fmt.Errorf("sqlite service %q is not configured", name)
}

func EnsureFiles(ctx context.Context, services []Service) error {
	for _, svc := range services {
		db, err := Open(ctx, svc.Path)
		if err != nil {
			return err
		}
		if err := db.Close(); err != nil {
			return err
		}
	}
	return nil
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open(DriverName, path)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"CREATE TABLE IF NOT EXISTS scenery_sqlite_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

func Env(services []Service, includeDatabaseURLAlias bool) []string {
	values := map[string]string{}
	for _, svc := range services {
		values[svc.DatabaseURLEnv] = svc.URL
		values[svc.DatabasePathEnv] = svc.Path
	}
	if includeDatabaseURLAlias && len(services) == 1 {
		if _, ok := values["DatabaseURL"]; !ok {
			values["DatabaseURL"] = services[0].URL
		}
	}
	if data, err := json.Marshal(services); err == nil {
		values["SCENERY_SQLITE_DATABASES_JSON"] = string(data)
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func ParseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme != "sqlite" {
		return "", fmt.Errorf("sqlite URL must use sqlite scheme")
	}
	if u.Host != "" {
		return "", fmt.Errorf("sqlite URL must be absolute path form sqlite:///path")
	}
	if u.Path == "" || !filepath.IsAbs(u.Path) {
		return "", fmt.Errorf("sqlite URL must contain an absolute path")
	}
	return u.Path, nil
}

func URLForPath(path string) string {
	return (&url.URL{Scheme: "sqlite", Path: path}).String()
}

func Backup(ctx context.Context, sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	db, err := sql.Open(DriverName, sourcePath)
	if err != nil {
		return err
	}
	defer db.Close()
	tmp := targetPath + ".tmp"
	_ = os.Remove(tmp)
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoteSQLiteString(tmp)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, targetPath)
}

func Snapshot(ctx context.Context, services []Service, dir string) error {
	for _, svc := range services {
		if err := Backup(ctx, svc.Path, filepath.Join(dir, filepath.Base(svc.Path))); err != nil {
			return err
		}
	}
	return nil
}

func DumpSchema(ctx context.Context, path string) (string, error) {
	db, err := sql.Open(DriverName, path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return "", err
		}
		parts = append(parts, stmt)
	}
	return strings.Join(parts, ";\n"), rows.Err()
}

func baseDir(appRoot string, req ResolveRequest) (string, error) {
	switch req.Mode {
	case "", ModeLocal:
		return filepath.Join(appRoot, ".scenery", "sqlite", "local"), nil
	case ModeSession:
		if strings.TrimSpace(req.SessionID) == "" {
			return "", fmt.Errorf("sqlite session id is required")
		}
		return filepath.Join(appRoot, ".scenery", "sessions", req.SessionID, "sqlite"), nil
	case ModeBranch:
		branchID := strings.TrimSpace(req.BranchID)
		if branchID == "" {
			branchID = "main"
		}
		return filepath.Join(appRoot, ".scenery", "db", "branches", branchID), nil
	default:
		return "", fmt.Errorf("unknown sqlite resolve mode %q", req.Mode)
	}
}

func quoteSQLiteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
