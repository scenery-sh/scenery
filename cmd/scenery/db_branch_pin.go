package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
)

func resolveDBBranchConnection(ctx context.Context, appRoot string, cfg appcfg.Config) (worktreeDBPin, dbBranchConnectionInfo, error) {
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return worktreeDBPin{}, dbBranchConnectionInfo{}, err
	}
	if !ok {
		return worktreeDBPin{}, dbBranchConnectionInfo{}, fmt.Errorf("dev.services.postgres has no worktree branch pin; run `scenery db branch checkout <name>` or `scenery up` first")
	}
	connection, err := dbBranchProviderForConfig(cfg).Connection(ctx, pin)
	if err != nil {
		return pin, dbBranchConnectionInfo{}, err
	}
	return pin, connection, nil
}

func resolveDBBranchDatabaseURL(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) (string, error) {
	if session == nil {
		_, connection, err := resolveDBBranchConnection(ctx, appRoot, cfg)
		if err != nil {
			return "", err
		}
		return connection.DatabaseURL, nil
	}
	resolution, err := ensureReadyDBBranchPinForSession(ctx, appRoot, cfg, session)
	if err != nil {
		return "", err
	}
	connection, err := dbBranchProviderForConfig(cfg).Connection(ctx, resolution.Pin)
	if err != nil {
		return "", err
	}
	return connection.DatabaseURL, nil
}

func dbBranchManagedPostgresEnv(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) ([]string, dbBranchResolution, dbBranchConnectionInfo, error) {
	resolution, err := ensureReadyDBBranchPinForSession(ctx, appRoot, cfg, session)
	if err != nil {
		return nil, dbBranchResolution{}, dbBranchConnectionInfo{}, err
	}
	connection, err := dbBranchProviderForConfig(cfg).Connection(ctx, resolution.Pin)
	if err != nil {
		return nil, resolution, dbBranchConnectionInfo{}, err
	}
	envName := dbDatabaseURLEnv(cfg)
	return []string{
		envName + "=" + connection.DatabaseURL,
		"SCENERY_MANAGED_DATABASE_URL=" + connection.DatabaseURL,
		"SCENERY_MANAGED_DATABASE_NAME=" + connection.DatabaseName,
	}, resolution, connection, nil
}

func ensureReadyDBBranchPinForSession(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) (dbBranchResolution, error) {
	return ensureDBBranchPinForSession(ctx, appRoot, cfg, session)
}

func normalizedDBBranchEndpoint(endpoint dbBranchEndpoint, pin worktreeDBPin) dbBranchEndpoint {
	endpoint.Host = strings.TrimSpace(endpoint.Host)
	endpoint.Database = sanitizeDBIdentifier(firstNonEmpty(endpoint.Database, pin.Database, dbBranchDefaultDatabase))
	endpoint.Role = sanitizeDBIdentifier(firstNonEmpty(endpoint.Role, pin.Role, dbBranchDefaultRole))
	endpoint.SSLMode = firstNonEmpty(strings.TrimSpace(endpoint.SSLMode), "disable")
	endpoint.Source = strings.TrimSpace(endpoint.Source)
	return endpoint
}

func worktreeDBPinPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "worktree-db.json")
}

func readWorktreeDBPin(path string) (worktreeDBPin, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return worktreeDBPin{}, false, nil
	}
	if err != nil {
		return worktreeDBPin{}, false, err
	}
	var pin worktreeDBPin
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pin); err != nil {
		return worktreeDBPin{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if pin.SchemaVersion != dbBranchPinSchemaVersion {
		return worktreeDBPin{}, false, fmt.Errorf("%s has unsupported schema_version %q", path, pin.SchemaVersion)
	}
	return pin, true, nil
}

func writeWorktreeDBPin(appRoot string, pin worktreeDBPin) error {
	if err := validateDBBranchLeaseWritable(pin); err != nil {
		return err
	}
	if err := ensureSceneryLocalStateIgnored(appRoot); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWriteFile(worktreeDBPinPath(appRoot), data, 0o644); err != nil {
		return err
	}
	return upsertDBBranchLease(pin)
}

func validateDBBranchLeaseWritable(pin worktreeDBPin) error {
	return validatePostgresBranchLeaseWritable(pin)
}

func upsertDBBranchLease(pin worktreeDBPin) error {
	return upsertPostgresBranchLease(pin, nil, "pending")
}

func buildWorktreeDBPin(appRoot string, cfg appcfg.Config, branch string) (worktreeDBPin, error) {
	return buildWorktreeDBPinForSession(appRoot, cfg, nil, branch)
}

func buildWorktreeDBPinForSession(appRoot string, cfg appcfg.Config, session *localagent.Session, branch string) (worktreeDBPin, error) {
	svc := dbPostgresService(cfg)
	kind := firstNonEmpty(strings.TrimSpace(svc.Kind), "postgres")
	if kind != "postgres" {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres kind must be %q for db branch checkout", "postgres")
	}
	if mode := firstNonEmpty(strings.TrimSpace(svc.Mode), postgresDefaultMode); mode != postgresDefaultMode {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres mode %q is not supported for Postgres branches; use %q", mode, postgresDefaultMode)
	}
	if isolation := firstNonEmpty(strings.TrimSpace(svc.Isolation), devPostgresDefaultIsolation); isolation != devPostgresDefaultIsolation {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres isolation %q is not supported for Postgres branches; use %q", isolation, devPostgresDefaultIsolation)
	}
	project := branchProjectForConfig(cfg)
	branch = normalizeDBBranchName(branch)
	if branch == "" {
		return worktreeDBPin{}, fmt.Errorf("db branch name is empty after sanitization")
	}
	parent := normalizeDBBranchName(firstNonEmpty(svc.ParentBranch, dbBranchDefaultParentBranch))
	sessionID := ""
	if session != nil {
		sessionID = strings.TrimSpace(session.SessionID)
	}
	provider := postgresBranchProviderName
	database := managedPostgresBranchDatabaseName(project, branch)
	role := sanitizeDBIdentifier(firstNonEmpty(svc.Role, dbBranchDefaultRole))
	return worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      provider,
		Project:       project,
		ParentBranch:  parent,
		Branch:        branch,
		BranchID:      dbLocalBranchID(project, branch),
		Database:      database,
		Role:          role,
		SessionID:     sessionID,
		WorktreeRoot:  appRoot,
		CreatedBy:     "scenery",
		TTL:           firstNonEmpty(svc.TTL, dbBranchDefaultTTL),
	}, nil
}

func ensureDBBranchPinForSession(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) (dbBranchResolution, error) {
	pinPath := worktreeDBPinPath(appRoot)
	provider := dbBranchProviderForConfig(cfg)
	if existing, ok, err := readWorktreeDBPin(pinPath); err != nil {
		return dbBranchResolution{}, err
	} else if ok {
		if firstNonEmpty(strings.TrimSpace(dbPostgresService(cfg).BranchPolicy), dbBranchDefaultPolicy) == "session" {
			branch, source, err := deriveDBBranchName(appRoot, cfg, session)
			if err != nil {
				return dbBranchResolution{}, err
			}
			pin, err := buildWorktreeDBPinForSession(appRoot, cfg, session, branch)
			if err != nil {
				return dbBranchResolution{}, err
			}
			if existing.SessionID != pin.SessionID || (!sameDBBranchLease(existing, pin) && !sameDBBranch(existing, pin)) {
				if err := writeWorktreeDBPin(appRoot, pin); err != nil {
					return dbBranchResolution{}, err
				}
				backendStatus, err := provider.EnsureBranch(ctx, pin)
				if err != nil {
					return dbBranchResolution{}, err
				}
				return dbBranchResolution{Pin: pin, Source: source, Created: true, BackendStatus: backendStatus}, nil
			}
		}
		backendStatus := provider.InspectBranch(ctx, existing)
		if backendStatus.Status != "ready" {
			var err error
			backendStatus, err = provider.EnsureBranch(ctx, existing)
			if err != nil {
				return dbBranchResolution{}, err
			}
		}
		return dbBranchResolution{Pin: existing, Source: "pin", BackendStatus: backendStatus}, nil
	}
	branch, source, err := deriveDBBranchName(appRoot, cfg, session)
	if err != nil {
		return dbBranchResolution{}, err
	}
	pin, err := buildWorktreeDBPinForSession(appRoot, cfg, session, branch)
	if err != nil {
		return dbBranchResolution{}, err
	}
	if err := writeWorktreeDBPin(appRoot, pin); err != nil {
		return dbBranchResolution{}, err
	}
	backendStatus, err := provider.EnsureBranch(ctx, pin)
	if err != nil {
		return dbBranchResolution{}, err
	}
	return dbBranchResolution{Pin: pin, Source: source, Created: true, BackendStatus: backendStatus}, nil
}

func deriveDBBranchName(appRoot string, cfg appcfg.Config, session *localagent.Session) (string, string, error) {
	svc := dbPostgresService(cfg)
	policy := firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), dbBranchDefaultPolicy)
	switch policy {
	case "manual":
		return "", "", fmt.Errorf("dev.services.postgres branch_policy %q requires `scenery db branch checkout <name>` before `scenery up`", policy)
	case "worktree", "":
		template := firstNonEmpty(strings.TrimSpace(svc.BranchNameTemplate), dbBranchDefaultNameTemplate)
		return renderDBBranchTemplate(template, appRoot, cfg, session), "worktree", nil
	case "session":
		template := strings.TrimSpace(svc.BranchNameTemplate)
		if template == "" {
			template = "{app}/{session}"
		}
		return renderDBBranchTemplate(template, appRoot, cfg, session), "session", nil
	default:
		return "", "", fmt.Errorf("dev.services.postgres branch_policy %q is not supported; use manual, worktree, or session", policy)
	}
}

func renderDBBranchTemplate(template, appRoot string, cfg appcfg.Config, session *localagent.Session) string {
	appID := firstNonEmpty(cfg.AppID(), "app")
	gitBranch := discoverDevGitBranch(appRoot)
	worktree := filepath.Base(strings.TrimRight(appRoot, string(os.PathSeparator)))
	sessionID := ""
	if session != nil {
		sessionID = strings.TrimSpace(session.SessionID)
		if gitBranch == "" {
			gitBranch = strings.TrimSpace(session.Branch)
		}
	}
	values := map[string]string{
		"{app}":        appID,
		"{git_branch}": firstNonEmpty(gitBranch, worktree, appID),
		"{worktree}":   firstNonEmpty(worktree, appID),
		"{session}":    firstNonEmpty(sessionID, appID),
	}
	result := template
	for placeholder, value := range values {
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return normalizeDBBranchName(result)
}

func isProtectedDBParentBranch(pin worktreeDBPin) bool {
	branch := normalizeDBBranchName(pin.Branch)
	parent := normalizeDBBranchName(pin.ParentBranch)
	return branch != "" && parent != "" && branch == parent
}

func dbLocalBranchID(project, branch string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + branch))
	return "br-local-" + hex.EncodeToString(sum[:])[:16]
}

func normalizeDBBranchName(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if segment := sanitizeDBBranchSegment(part); segment != "" {
			clean = append(clean, segment)
		}
	}
	return strings.Join(clean, "/")
}

func sanitizeDBBranchSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if r == '-' || r == '_' || r == '.' || unicode.IsSpace(r) {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func sanitizeDBIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	underscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if r == '_' || r == '-' || r == '.' || unicode.IsSpace(r) {
			if !underscore && b.Len() > 0 {
				b.WriteByte('_')
				underscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
