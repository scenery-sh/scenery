package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/sqlitedb"
)

const (
	devZeroFSDefaultRoute      = "storage"
	devZeroFSToolchainArtifact = "zerofs"
	appDatabaseURLEnv          = "DatabaseURL"
	legacyDatabaseURLEnv       = "DATABASE_URL"
)

type managedZeroFSPlan struct {
	ServiceName   string
	StorageCellID string
	Route         string
	Image         string
	ToolchainDir  string
	CellRoot      string
	CacheDir      string
	ObjectsDir    string
	RunDir        string
	ConfigPath    string
	NinePListen   string
	NinePSocket   string
	RPCSocket     string
	WebUIListen   string
	WebUIAddrPath string
	LogPath       string
	Env           map[string]string
}

func managedSQLiteEnv(ctx context.Context, appRoot string, cfg app.Config, session *localagent.Session) ([]string, []sqlitedb.Service, error) {
	req := sqlitedb.ResolveRequest{AppRoot: appRoot, Config: cfg, Mode: sqlitedb.ModeLocal}
	if session != nil && strings.TrimSpace(session.SessionID) != "" {
		req.Mode = sqlitedb.ModeSession
		req.SessionID = session.SessionID
	}
	services, err := sqlitedb.ResolveServices(req)
	if err != nil {
		return nil, nil, err
	}
	services = append(services, discoverServiceSQLiteDatabases(appRoot, req.Mode, req.SessionID, services)...)
	if len(services) == 0 {
		return nil, nil, nil
	}
	if err := sqlitedb.EnsureFiles(ctx, services); err != nil {
		return nil, nil, err
	}
	includeAlias := len(services) == 1
	if includeAlias {
		cfgServices := cfg.SQLiteServices()
		includeAlias = len(cfgServices) == 1 && strings.TrimSpace(cfgServices[0].Raw.DatabaseURLEnv) == ""
	}
	return sqlitedb.Env(services, includeAlias), services, nil
}

func discoverServiceSQLiteDatabases(appRoot string, mode sqlitedb.Mode, sessionID string, existing []sqlitedb.Service) []sqlitedb.Service {
	seen := map[string]bool{}
	for _, svc := range existing {
		seen[svc.Name] = true
	}
	base := filepath.Join(appRoot, ".scenery", "sqlite", "local")
	if mode == sqlitedb.ModeSession && strings.TrimSpace(sessionID) != "" {
		base = filepath.Join(appRoot, ".scenery", "sessions", sessionID, "sqlite")
	}
	var out []sqlitedb.Service
	_ = filepath.WalkDir(appRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".scenery", "node_modules", "var", "x":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) != "schema.sql" || filepath.Base(filepath.Dir(path)) != "db" {
			return nil
		}
		name := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if name == "" || seen[name] {
			return nil
		}
		seen[name] = true
		fileLabel := localagentLabel(name)
		if fileLabel == "" {
			fileLabel = name
		}
		dbPath := filepath.Join(base, fileLabel+".sqlite")
		out = append(out, sqlitedb.Service{
			Name:            name,
			FileLabel:       fileLabel,
			Path:            dbPath,
			URL:             sqlitedb.URLForPath(dbPath),
			DatabaseURLEnv:  sqliteServiceEnvName(name, "DATABASE_URL"),
			DatabasePathEnv: sqliteServiceEnvName(name, "DATABASE_PATH"),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sqliteServiceEnvName(name, suffix string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(name) {
		ok := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	prefix := strings.Trim(b.String(), "_")
	if prefix == "" {
		prefix = "SQLITE"
	}
	return prefix + "_" + suffix
}

func shortIdentityHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func verifySubstrateOwner(substrate localagent.Substrate) error {
	if substrate.Owner.PID <= 0 && substrate.OwnerPID <= 0 {
		return fmt.Errorf("substrate owner is missing")
	}
	owner := substrate.Owner
	if owner.PID <= 0 {
		owner.PID = substrate.OwnerPID
	}
	if err := localagent.VerifyOwner(owner); err != nil {
		return err
	}
	for name, pid := range substrate.PIDs {
		if pid <= 0 {
			continue
		}
		componentOwner := substrate.Owners[name]
		if componentOwner.PID <= 0 {
			componentOwner.PID = pid
		}
		if err := localagent.VerifyOwner(componentOwner); err != nil {
			return fmt.Errorf("substrate component %s owner invalid: %w", name, err)
		}
	}
	return nil
}

func currentAgentSessionForAppRoot(ctx context.Context, appRoot string) (*localagent.Session, error) {
	client, err := localagent.DefaultClient()
	if err != nil {
		return nil, err
	}
	return currentAgentSessionForAppRootWithClient(ctx, client, appRoot)
}

func currentAgentSessionForAppRootWithClient(ctx context.Context, client *localagent.Client, appRoot string) (*localagent.Session, error) {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
	}
	return &sessions[0], nil
}

func localagentLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeManagedTCPUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return value
}

func copyManagedEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		copied[key] = strings.TrimSpace(value)
	}
	return copied
}

func copyManagedBackends(backends map[string]localagent.Backend) map[string]localagent.Backend {
	copied := make(map[string]localagent.Backend, len(backends)+1)
	for key, backend := range backends {
		copied[key] = backend
	}
	return copied
}

func envWithManagedOverrides(base []string, overrides map[string]string) []string {
	env := append([]string(nil), base...)
	index := make(map[string]int, len(env))
	for i, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			index[key] = i
		}
	}
	for key, value := range overrides {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		item := key + "=" + strings.TrimSpace(value)
		if i, ok := index[key]; ok {
			env[i] = item
			continue
		}
		index[key] = len(env)
		env = append(env, item)
	}
	return env
}

func lookupEnvValue(env []string, key string) (string, string) {
	values := envListMap(env)
	if value := strings.TrimSpace(values[key]); value != "" {
		return value, key
	}
	return "", ""
}
