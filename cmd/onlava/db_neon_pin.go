package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
)

func resolveNeonBranchConnection(ctx context.Context, appRoot string, cfg appcfg.Config) (worktreeDBPin, neonBranchConnectionInfo, error) {
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return worktreeDBPin{}, neonBranchConnectionInfo{}, err
	}
	if !ok {
		return worktreeDBPin{}, neonBranchConnectionInfo{}, fmt.Errorf("dev.services.postgres kind %q has no worktree branch pin; run `onlava db branch checkout <name>` or `onlava up` first", "neon")
	}
	connection, err := neonBranchProviderForConfig(cfg).Connection(ctx, pin)
	if err != nil {
		return pin, neonBranchConnectionInfo{}, err
	}
	return pin, connection, nil
}

func resolveNeonBranchDatabaseURL(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) (string, error) {
	if session == nil {
		_, connection, err := resolveNeonBranchConnection(ctx, appRoot, cfg)
		if err != nil {
			return "", err
		}
		return connection.DatabaseURL, nil
	}
	resolution, err := ensureNeonBranchPinForSession(ctx, appRoot, cfg, session)
	if err != nil {
		return "", err
	}
	connection, err := neonBranchProviderForConfig(cfg).Connection(ctx, resolution.Pin)
	if err != nil {
		return "", err
	}
	return connection.DatabaseURL, nil
}

func neonManagedPostgresEnv(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) ([]string, neonBranchResolution, neonBranchConnectionInfo, error) {
	resolution, err := ensureNeonBranchPinForSession(ctx, appRoot, cfg, session)
	if err != nil {
		return nil, neonBranchResolution{}, neonBranchConnectionInfo{}, err
	}
	connection, err := neonBranchProviderForConfig(cfg).Connection(ctx, resolution.Pin)
	if err != nil {
		return nil, resolution, neonBranchConnectionInfo{}, err
	}
	envName := neonDatabaseURLEnv(cfg)
	return []string{
		envName + "=" + connection.DatabaseURL,
		"ONLAVA_MANAGED_DATABASE_URL=" + connection.DatabaseURL,
		"ONLAVA_MANAGED_DATABASE_NAME=" + connection.DatabaseName,
	}, resolution, connection, nil
}

func normalizedNeonEndpoint(endpoint neonEndpoint, pin worktreeDBPin) neonEndpoint {
	endpoint.Host = strings.TrimSpace(endpoint.Host)
	endpoint.Database = sanitizeNeonIdentifier(firstNonEmpty(endpoint.Database, pin.Database, neonDefaultDatabase))
	endpoint.Role = sanitizeNeonIdentifier(firstNonEmpty(endpoint.Role, pin.Role, neonDefaultRole))
	endpoint.SSLMode = firstNonEmpty(strings.TrimSpace(endpoint.SSLMode), "disable")
	endpoint.Source = strings.TrimSpace(endpoint.Source)
	return endpoint
}

func neonEndpointDatabaseURL(pin worktreeDBPin, endpoint neonEndpoint) (string, error) {
	endpoint = normalizedNeonEndpoint(endpoint, pin)
	if endpoint.Host == "" {
		return "", fmt.Errorf("local Neon branch lease %q endpoint is missing host", pin.Branch)
	}
	if endpoint.Port <= 0 || endpoint.Port > 65535 {
		return "", fmt.Errorf("local Neon branch lease %q endpoint has invalid port %d", pin.Branch, endpoint.Port)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.User(endpoint.Role),
		Host:   net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)),
		Path:   "/" + endpoint.Database,
	}
	q := u.Query()
	q.Set("sslmode", endpoint.SSLMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func worktreeDBPinPath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "worktree-db.json")
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
	if err := validateNeonBranchLeaseWritable(pin); err != nil {
		return err
	}
	if err := ensureOnlavaLocalStateIgnored(appRoot); err != nil {
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
	return upsertNeonBranchLease(pin)
}

func validateNeonBranchLeaseWritable(pin worktreeDBPin) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return err
	}
	for _, lease := range registry.Leases {
		if sameNeonLease(lease.Pin, pin) || sameNeonBranch(lease.Pin, pin) {
			if !isOnlavaOwnedNeonLease(lease) {
				return fmt.Errorf("refusing to reuse foreign local Neon branch lease %q; remove or rename that lease before checkout", pin.Branch)
			}
		}
	}
	return nil
}

func buildWorktreeDBPin(appRoot string, cfg appcfg.Config, branch string) (worktreeDBPin, error) {
	return buildWorktreeDBPinForSession(appRoot, cfg, nil, branch)
}

func buildWorktreeDBPinForSession(appRoot string, cfg appcfg.Config, session *localagent.Session, branch string) (worktreeDBPin, error) {
	svc := neonPostgresService(cfg)
	if kind := firstNonEmpty(strings.TrimSpace(svc.Kind), "postgres"); kind != "neon" {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres kind must be %q for db branch checkout", "neon")
	}
	if mode := firstNonEmpty(strings.TrimSpace(svc.Mode), neonDefaultMode); mode != neonDefaultMode {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres mode %q is not supported for Neon; use %q", mode, neonDefaultMode)
	}
	if isolation := firstNonEmpty(strings.TrimSpace(svc.Isolation), neonDefaultIsolation); isolation != neonDefaultIsolation {
		return worktreeDBPin{}, fmt.Errorf("dev.services.postgres isolation %q is not supported for Neon; use %q", isolation, neonDefaultIsolation)
	}
	project := neonProjectForConfig(cfg)
	branch = normalizeNeonBranchName(branch)
	if branch == "" {
		return worktreeDBPin{}, fmt.Errorf("db branch name is empty after sanitization")
	}
	parent := normalizeNeonBranchName(firstNonEmpty(svc.ParentBranch, neonDefaultParentBranch))
	sessionID := ""
	if session != nil {
		sessionID = strings.TrimSpace(session.SessionID)
	}
	return worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      neonSelfhostProvider,
		Project:       project,
		ParentBranch:  parent,
		Branch:        branch,
		BranchID:      neonLocalBranchID(project, branch),
		Database:      sanitizeNeonIdentifier(firstNonEmpty(svc.Database, cfg.AppID(), neonDefaultDatabase)),
		Role:          sanitizeNeonIdentifier(firstNonEmpty(svc.Role, neonDefaultRole)),
		SessionID:     sessionID,
		WorktreeRoot:  appRoot,
		CreatedBy:     "onlava",
		TTL:           firstNonEmpty(svc.TTL, neonDefaultTTL),
	}, nil
}

func ensureNeonBranchPinForSession(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session) (neonBranchResolution, error) {
	pinPath := worktreeDBPinPath(appRoot)
	provider := neonBranchProviderForConfig(cfg)
	if existing, ok, err := readWorktreeDBPin(pinPath); err != nil {
		return neonBranchResolution{}, err
	} else if ok {
		if firstNonEmpty(strings.TrimSpace(neonPostgresService(cfg).BranchPolicy), neonDefaultBranchPolicy) == "session" {
			branch, source, err := deriveNeonBranchName(appRoot, cfg, session)
			if err != nil {
				return neonBranchResolution{}, err
			}
			pin, err := buildWorktreeDBPinForSession(appRoot, cfg, session, branch)
			if err != nil {
				return neonBranchResolution{}, err
			}
			if existing.SessionID != pin.SessionID || (!sameNeonLease(existing, pin) && !sameNeonBranch(existing, pin)) {
				if err := writeWorktreeDBPin(appRoot, pin); err != nil {
					return neonBranchResolution{}, err
				}
				backendStatus, err := provider.EnsureBranch(ctx, pin)
				if err != nil {
					return neonBranchResolution{}, err
				}
				return neonBranchResolution{Pin: pin, Source: source, Created: true, BackendStatus: backendStatus}, nil
			}
		}
		backendStatus, err := provider.EnsureBranch(ctx, existing)
		if err != nil {
			return neonBranchResolution{}, err
		}
		return neonBranchResolution{Pin: existing, Source: "pin", BackendStatus: backendStatus}, nil
	}
	branch, source, err := deriveNeonBranchName(appRoot, cfg, session)
	if err != nil {
		return neonBranchResolution{}, err
	}
	pin, err := buildWorktreeDBPinForSession(appRoot, cfg, session, branch)
	if err != nil {
		return neonBranchResolution{}, err
	}
	if err := writeWorktreeDBPin(appRoot, pin); err != nil {
		return neonBranchResolution{}, err
	}
	backendStatus, err := provider.EnsureBranch(ctx, pin)
	if err != nil {
		return neonBranchResolution{}, err
	}
	return neonBranchResolution{Pin: pin, Source: source, Created: true, BackendStatus: backendStatus}, nil
}

func deriveNeonBranchName(appRoot string, cfg appcfg.Config, session *localagent.Session) (string, string, error) {
	svc := neonPostgresService(cfg)
	policy := firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), neonDefaultBranchPolicy)
	switch policy {
	case "manual":
		return "", "", fmt.Errorf("dev.services.postgres branch_policy %q requires `onlava db branch checkout <name>` before `onlava up`", policy)
	case "worktree", "":
		template := firstNonEmpty(strings.TrimSpace(svc.BranchNameTemplate), neonDefaultBranchNameTemplate)
		return renderNeonBranchTemplate(template, appRoot, cfg, session), "worktree", nil
	case "session":
		template := strings.TrimSpace(svc.BranchNameTemplate)
		if template == "" {
			template = "{app}/{session}"
		}
		return renderNeonBranchTemplate(template, appRoot, cfg, session), "session", nil
	default:
		return "", "", fmt.Errorf("dev.services.postgres branch_policy %q is not supported for Neon; use manual, worktree, or session", policy)
	}
}

func renderNeonBranchTemplate(template, appRoot string, cfg appcfg.Config, session *localagent.Session) string {
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
	return normalizeNeonBranchName(result)
}

func isProtectedNeonParentBranch(pin worktreeDBPin) bool {
	branch := normalizeNeonBranchName(pin.Branch)
	parent := normalizeNeonBranchName(pin.ParentBranch)
	return branch != "" && parent != "" && branch == parent
}

func neonLocalBranchID(project, branch string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + branch))
	return "br-local-" + hex.EncodeToString(sum[:])[:16]
}

func normalizeNeonBranchName(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if segment := sanitizeNeonBranchSegment(part); segment != "" {
			clean = append(clean, segment)
		}
	}
	return strings.Join(clean, "/")
}

func sanitizeNeonBranchSegment(value string) string {
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

func sanitizeNeonIdentifier(value string) string {
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
