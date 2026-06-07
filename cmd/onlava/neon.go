package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lib/pq"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	toolchain "github.com/pbrazdil/onlava/internal/toolchain"
)

const (
	neonWorktreeBranchSchemaVersion = "onlava.db.branch.v1"
	neonCellSchemaVersion           = "onlava.db.neon.cell.v1"
	neonBranchesSchemaVersion       = "onlava.db.neon.branches.v1"
	neonProviderSelfHosted          = "neon-self-hosted"
	neonDefaultMode                 = "self-hosted"
	neonDefaultIsolation            = "branch"
	neonDefaultProject              = "default"
	neonDefaultParentBranch         = "main"
	neonDefaultBranchPolicy         = "worktree"
	neonDefaultSessionPolicy        = "session"
	neonDefaultTemplate             = "{app}/{git_branch}"
	neonDefaultSessionTemplate      = "{app}/{session}"
	neonDefaultTTL                  = "168h"
	neonDefaultDatabase             = "postgres"
	neonDefaultRole                 = "onlava"
)

type neonBranchSpec struct {
	AppRoot            string
	AppID              string
	Project            string
	ParentBranch       string
	Branch             string
	BranchID           string
	BranchPolicy       string
	BranchNameTemplate string
	Database           string
	Role               string
	TTL                string
	AdminURL           string
	SessionID          string
	WorktreeRoot       string
	GitBranch          string
	DatabaseURLEnv     string
}

type neonBranchLease struct {
	SchemaVersion string    `json:"schema_version"`
	Provider      string    `json:"provider"`
	Project       string    `json:"project"`
	ParentBranch  string    `json:"parent_branch"`
	Branch        string    `json:"branch"`
	BranchID      string    `json:"branch_id"`
	Database      string    `json:"database"`
	Role          string    `json:"role"`
	SessionID     string    `json:"session_id,omitempty"`
	WorktreeRoot  string    `json:"worktree_root,omitempty"`
	CreatedBy     string    `json:"created_by"`
	TTL           string    `json:"ttl,omitempty"`
	DatabaseName  string    `json:"database_name,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

type neonCellState struct {
	SchemaVersion string                   `json:"schema_version"`
	Provider      string                   `json:"provider"`
	Mode          string                   `json:"mode"`
	Status        string                   `json:"status"`
	Backend       string                   `json:"backend"`
	Version       string                   `json:"version"`
	Root          string                   `json:"root"`
	ComposePath   string                   `json:"compose_path"`
	AdminURL      string                   `json:"admin_url,omitempty"`
	Components    map[string]neonComponent `json:"components"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

type neonComponent struct {
	Status   string `json:"status"`
	Health   string `json:"health,omitempty"`
	Source   string `json:"source,omitempty"`
	Version  string `json:"version,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Port     int    `json:"port,omitempty"`
	LogPath  string `json:"log_path,omitempty"`
}

type neonBranchesState struct {
	SchemaVersion string                     `json:"schema_version"`
	Branches      map[string]neonBranchLease `json:"branches"`
	UpdatedAt     time.Time                  `json:"updated_at"`
}

type neonBranchStatusResult struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Database      neonDatabaseStatus `json:"database"`
}

type neonDatabaseStatus struct {
	Provider       string    `json:"provider"`
	Mode           string    `json:"mode"`
	Project        string    `json:"project"`
	Branch         string    `json:"branch"`
	BranchID       string    `json:"branch_id"`
	ParentBranch   string    `json:"parent_branch"`
	DatabaseURLEnv string    `json:"database_url_env"`
	Status         string    `json:"status"`
	TTL            string    `json:"ttl,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	PSQLCommand    string    `json:"psql_command"`
	ResetCommand   string    `json:"reset_command"`
	RestoreCommand string    `json:"restore_command"`
	DiffCommand    string    `json:"diff_command"`
	ExpireCommand  string    `json:"expire_command"`
	LogsCommand    string    `json:"logs_command"`
	Connection     string    `json:"connection"`
}

type neonBranchListResult struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Project       string             `json:"project"`
	Branches      []neonBranchLease  `json:"branches"`
}

type neonCellStatusResult struct {
	SchemaVersion string                   `json:"schema_version"`
	Provider      string                   `json:"provider"`
	Mode          string                   `json:"mode"`
	Status        string                   `json:"status"`
	Backend       string                   `json:"backend"`
	Version       string                   `json:"version"`
	Root          string                   `json:"root"`
	ComposePath   string                   `json:"compose_path"`
	AdminURL      string                   `json:"admin_url"`
	Components    map[string]neonComponent `json:"components"`
}

func postgresServiceKind(svc appcfg.DevServiceConfig, name string) string {
	kind := strings.TrimSpace(svc.Kind)
	if kind == "" && name == "postgres" {
		kind = "postgres"
	}
	return kind
}

func isNeonPostgresService(svc appcfg.DevServiceConfig, name string) bool {
	return postgresServiceKind(svc, name) == "neon"
}

func managedPostgresUsesNeon(cfg appcfg.Config) (string, appcfg.DevServiceConfig, bool) {
	for name, svc := range cfg.Dev.Services {
		if isNeonPostgresService(svc, name) {
			return name, svc, true
		}
	}
	return "", appcfg.DevServiceConfig{}, false
}

func validateNeonPostgresConfig(name string, svc appcfg.DevServiceConfig) error {
	mode := firstNonEmpty(strings.TrimSpace(svc.Mode), neonDefaultMode)
	if mode != neonDefaultMode {
		return fmt.Errorf("dev.services.%s mode %q is not supported for kind %q; use %q", name, mode, svc.Kind, neonDefaultMode)
	}
	isolation := firstNonEmpty(strings.TrimSpace(svc.Isolation), neonDefaultIsolation)
	if isolation != neonDefaultIsolation {
		return fmt.Errorf("dev.services.%s isolation %q is not supported for kind %q; use %q", name, isolation, svc.Kind, neonDefaultIsolation)
	}
	policy := firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), neonDefaultBranchPolicy)
	switch policy {
	case "manual", "worktree", "session":
		return nil
	default:
		return fmt.Errorf("dev.services.%s branch_policy %q is not supported; use manual, worktree, or session", name, policy)
	}
}

func envWithManagedNeonAdminURL(ctx context.Context, cfg appcfg.Config, env []string) ([]string, error) {
	name, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok || hasEnvValue(env, devPostgresAdminURLEnv) {
		return env, nil
	}
	if err := validateNeonPostgresConfig(name, svc); err != nil {
		return nil, err
	}
	cell, err := ensureNeonDevCell(ctx, cfg, svc)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cell.AdminURL) == "" {
		return env, nil
	}
	return append(append([]string(nil), env...), devPostgresAdminURLEnv+"="+cell.AdminURL), nil
}

func ensureNeonDevCell(ctx context.Context, cfg appcfg.Config, svc appcfg.DevServiceConfig) (neonCellState, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return neonCellState{}, err
	}
	root := filepath.Join(paths.AgentDir, "substrates", "neon")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return neonCellState{}, err
	}
	composePath := filepath.Join(root, "compose.generated.yml")
	neonImageRef, err := neonToolchainImageRef("neon", "neondatabase/neon:unstable")
	if err != nil {
		return neonCellState{}, err
	}
	computeImageRef, err := neonToolchainImageRef("neon-compute", "neondatabase/compute-node-v17:unstable")
	if err != nil {
		return neonCellState{}, err
	}
	if err := writeNeonGeneratedCompose(composePath, svc, neonImageRef, computeImageRef); err != nil {
		return neonCellState{}, err
	}
	version := firstNonEmpty(strings.TrimSpace(svc.Version), devPostgresDefaultVersion)
	server, err := startLocalManagedPostgres(ctx, filepath.Join(root, "postgres"), version)
	if err != nil {
		return neonCellState{}, err
	}
	state := neonCellState{
		SchemaVersion: neonCellSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Mode:          neonDefaultMode,
		Status:        "ready",
		Backend:       "postgres-compatible-local-cell",
		Version:       version,
		Root:          root,
		ComposePath:   composePath,
		AdminURL:      server.AdminURL,
		Components: map[string]neonComponent{
			"pageserver":   {Status: "ready", Health: "simulated-ready", Source: neonImageRef, Version: "unstable"},
			"safekeeper-1": {Status: "ready", Health: "simulated-ready", Source: neonImageRef, Version: "unstable"},
			"broker":       {Status: "ready", Health: "simulated-ready", Source: neonImageRef, Version: "unstable"},
			"compute":      {Status: "ready", Health: "postgres-ready", Source: server.Source, Version: version, Endpoint: "postgres://cloud_admin@127.0.0.1/postgres", Port: server.Port, LogPath: server.LogPath},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := writeJSONFile(filepath.Join(root, "cell.json"), state, 0o644); err != nil {
		return neonCellState{}, err
	}
	return state, nil
}

func readNeonCellState() (neonCellState, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return neonCellState{}, err
	}
	path := filepath.Join(paths.AgentDir, "substrates", "neon", "cell.json")
	var state neonCellState
	if err := readJSONFile(path, &state); err != nil {
		return neonCellState{}, err
	}
	return state, nil
}

func writeNeonGeneratedCompose(path string, svc appcfg.DevServiceConfig, neonImageRef, computeImageRef string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "# Generated by onlava. Do not edit.\n" +
		"# Onlava manages this dev-cell contract; the first local implementation uses\n" +
		"# a Postgres-compatible compute while preserving Neon branch leases.\n" +
		"services:\n" +
		"  pageserver:\n" +
		"    image: " + neonImageRef + "\n" +
		"  safekeeper:\n" +
		"    image: " + neonImageRef + "\n" +
		"  broker:\n" +
		"    image: " + neonImageRef + "\n" +
		"  compute:\n" +
		"    image: " + computeImageRef + "\n" +
		"    labels:\n" +
		"      com.onlava.substrate: neon\n" +
		"      com.onlava.mode: self-hosted\n" +
		"      com.onlava.version: " + firstNonEmpty(strings.TrimSpace(svc.Version), devPostgresDefaultVersion) + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func neonToolchainImageRef(name, fallback string) (string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", err
	}
	artifact, ok := manifest.Artifact(name)
	if !ok {
		if fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("toolchain image artifact %s is not declared", name)
	}
	for _, image := range artifact.Images {
		if image.Digest != "" {
			return image.Ref + "@" + image.Digest, nil
		}
		if image.Ref != "" {
			return image.Ref, nil
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("toolchain image artifact %s has no image ref", name)
}

func ensureNeonBranchLease(ctx context.Context, cfg appcfg.Config, session *localagent.Session, adminURL string) (neonBranchLease, string, error) {
	name, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		return neonBranchLease{}, "", fmt.Errorf("dev.services.postgres kind neon is not configured")
	}
	if err := validateNeonPostgresConfig(name, svc); err != nil {
		return neonBranchLease{}, "", err
	}
	spec, err := buildNeonBranchSpec(cfg, svc, session, adminURL)
	if err != nil {
		return neonBranchLease{}, "", err
	}
	lease, err := readNeonWorktreeLease(spec.AppRoot)
	if err == nil && lease.Provider == neonProviderSelfHosted && lease.Project == spec.Project && strings.TrimSpace(lease.Branch) != "" {
		spec.Branch = lease.Branch
		spec.BranchID = firstNonEmpty(lease.BranchID, neonBranchID(spec.Project, spec.Branch))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return neonBranchLease{}, "", err
	} else if spec.BranchPolicy == "manual" {
		return neonBranchLease{}, "", fmt.Errorf("dev.services.%s branch_policy manual requires .onlava/worktree-db.json or onlava db branch checkout <name>", name)
	}
	provider := newNeonSelfHostedBranchProvider(adminURL)
	lease, err = provider.EnsureBranch(ctx, spec)
	if err != nil {
		return neonBranchLease{}, "", err
	}
	if err := writeNeonWorktreeLease(spec.AppRoot, lease); err != nil {
		return neonBranchLease{}, "", err
	}
	if err := upsertNeonGlobalBranch(lease); err != nil {
		return neonBranchLease{}, "", err
	}
	dbURL, err := postgresDatabaseURL(adminURL, lease.DatabaseName)
	if err != nil {
		return neonBranchLease{}, "", err
	}
	return lease, dbURL, nil
}

func buildNeonBranchSpec(cfg appcfg.Config, svc appcfg.DevServiceConfig, session *localagent.Session, adminURL string) (neonBranchSpec, error) {
	root := ""
	sessionID := ""
	gitBranch := ""
	if session != nil {
		root = strings.TrimSpace(session.AppRoot)
		sessionID = strings.TrimSpace(session.SessionID)
		gitBranch = strings.TrimSpace(session.Branch)
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return neonBranchSpec{}, err
		}
		root = wd
	}
	if resolved, _, err := appcfg.DiscoverRoot(root); err == nil {
		root = resolved
	}
	if gitBranch == "" {
		gitBranch = gitBranchName(root)
	}
	if sessionID == "" {
		sessionID = localagentLabel(filepath.Base(root))
	}
	policy := firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), neonDefaultBranchPolicy)
	tmpl := strings.TrimSpace(svc.BranchNameTemplate)
	if tmpl == "" {
		if policy == "session" {
			tmpl = neonDefaultSessionTemplate
		} else {
			tmpl = neonDefaultTemplate
		}
	}
	project := firstNonEmpty(strings.TrimSpace(svc.Project), cfg.AppID(), neonDefaultProject)
	parent := firstNonEmpty(strings.TrimSpace(svc.ParentBranch), neonDefaultParentBranch)
	spec := neonBranchSpec{
		AppRoot:            root,
		AppID:              firstNonEmpty(cfg.AppID(), filepath.Base(root)),
		Project:            sanitizeNeonBranchSegment(project),
		ParentBranch:       sanitizeNeonBranch(parent),
		BranchPolicy:       policy,
		BranchNameTemplate: tmpl,
		Database:           firstNonEmpty(strings.TrimSpace(svc.Database), neonDefaultDatabase),
		Role:               firstNonEmpty(strings.TrimSpace(svc.Role), neonDefaultRole),
		TTL:                firstNonEmpty(strings.TrimSpace(svc.TTL), neonDefaultTTL),
		AdminURL:           adminURL,
		SessionID:          sessionID,
		WorktreeRoot:       root,
		GitBranch:          gitBranch,
		DatabaseURLEnv:     firstNonEmpty(strings.TrimSpace(svc.DatabaseURLEnv), appDatabaseURLEnv),
	}
	spec.Branch = renderNeonBranchName(spec)
	if spec.Branch == "" {
		spec.Branch = spec.Project + "/" + localagentLabel(sessionID)
	}
	spec.BranchID = neonBranchID(spec.Project, spec.Branch)
	return spec, nil
}

func leaseFromNeonSpec(spec neonBranchSpec) neonBranchLease {
	now := time.Now().UTC()
	ttl, _ := time.ParseDuration(spec.TTL)
	lease := neonBranchLease{
		SchemaVersion: neonWorktreeBranchSchemaVersion,
		Provider:      neonProviderSelfHosted,
		Project:       spec.Project,
		ParentBranch:  spec.ParentBranch,
		Branch:        spec.Branch,
		BranchID:      firstNonEmpty(spec.BranchID, neonBranchID(spec.Project, spec.Branch)),
		Database:      spec.Database,
		Role:          spec.Role,
		SessionID:     spec.SessionID,
		WorktreeRoot:  spec.WorktreeRoot,
		CreatedBy:     "onlava",
		TTL:           spec.TTL,
		DatabaseName:  neonBranchDatabaseName(spec.Project, spec.Branch),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if ttl > 0 {
		lease.ExpiresAt = now.Add(ttl)
	}
	return lease
}

func renderNeonBranchName(spec neonBranchSpec) string {
	values := map[string]string{
		"app":        sanitizeNeonBranchSegment(spec.AppID),
		"git_branch": sanitizeNeonBranch(spec.GitBranch),
		"worktree":   sanitizeNeonBranchSegment(filepath.Base(spec.WorktreeRoot)),
		"session":    sanitizeNeonBranchSegment(spec.SessionID),
	}
	value := spec.BranchNameTemplate
	for key, replacement := range values {
		value = strings.ReplaceAll(value, "{"+key+"}", replacement)
	}
	return sanitizeNeonBranch(value)
}

func sanitizeNeonBranch(value string) string {
	value = strings.TrimSpace(value)
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if segment := sanitizeNeonBranchSegment(part); segment != "" {
			out = append(out, segment)
		}
	}
	return strings.Join(out, "/")
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
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func neonBranchID(project, branch string) string {
	sum := sha256.Sum256([]byte(project + ":" + branch))
	return "br-local-" + hex.EncodeToString(sum[:])[:20]
}

func neonBranchDatabaseName(project, branch string) string {
	label := postgresIdentifierPart(project) + "_" + postgresIdentifierPart(branch)
	label = strings.Trim(label, "_")
	if label == "" {
		label = "onlava_neon_branch"
	}
	if len(label) <= 55 {
		return label
	}
	sum := sha256.Sum256([]byte(project + ":" + branch))
	suffix := "_" + hex.EncodeToString(sum[:])[:7]
	return strings.TrimRight(label[:55-len(suffix)], "_") + suffix
}

func gitBranchName(root string) string {
	cmd := exec.Command("git", "-C", root, "branch", "--show-current")
	out, err := cmd.Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	return filepath.Base(root)
}

func neonWorktreeLeasePath(appRoot string) string {
	return filepath.Join(appRoot, ".onlava", "worktree-db.json")
}

func readNeonWorktreeLease(appRoot string) (neonBranchLease, error) {
	var lease neonBranchLease
	if err := readJSONFile(neonWorktreeLeasePath(appRoot), &lease); err != nil {
		return neonBranchLease{}, err
	}
	if lease.SchemaVersion != neonWorktreeBranchSchemaVersion {
		return neonBranchLease{}, fmt.Errorf("%s has schema_version %q, want %q", neonWorktreeLeasePath(appRoot), lease.SchemaVersion, neonWorktreeBranchSchemaVersion)
	}
	return lease, nil
}

func writeNeonWorktreeLease(appRoot string, lease neonBranchLease) error {
	return writeJSONFile(neonWorktreeLeasePath(appRoot), lease, 0o644)
}

func readNeonBranchesState() (neonBranchesState, string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return neonBranchesState{}, "", err
	}
	path := filepath.Join(paths.AgentDir, "substrates", "neon", "branches.json")
	state := neonBranchesState{SchemaVersion: neonBranchesSchemaVersion, Branches: map[string]neonBranchLease{}}
	if err := readJSONFile(path, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, path, nil
		}
		return neonBranchesState{}, "", err
	}
	if state.Branches == nil {
		state.Branches = map[string]neonBranchLease{}
	}
	return state, path, nil
}

func upsertNeonGlobalBranch(lease neonBranchLease) error {
	state, path, err := readNeonBranchesState()
	if err != nil {
		return err
	}
	if existing, ok := state.Branches[lease.BranchID]; ok && !existing.CreatedAt.IsZero() {
		lease.CreatedAt = existing.CreatedAt
	}
	lease.UpdatedAt = time.Now().UTC()
	state.SchemaVersion = neonBranchesSchemaVersion
	state.UpdatedAt = lease.UpdatedAt
	state.Branches[lease.BranchID] = lease
	return writeJSONFile(path, state, 0o644)
}

func removeNeonGlobalBranch(branchID string) error {
	state, path, err := readNeonBranchesState()
	if err != nil {
		return err
	}
	delete(state.Branches, branchID)
	state.UpdatedAt = time.Now().UTC()
	return writeJSONFile(path, state, 0o644)
}

func dbNeonCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: onlava db neon install|status|logs|restart|uninstall [--app-root <path>] [--json]")
	}
	switch args[0] {
	case "install":
		return dbNeonInstallCommand(args[1:])
	case "status":
		return dbNeonStatusCommand(args[1:])
	case "logs":
		return dbNeonLogsCommand(args[1:])
	case "restart":
		return dbNeonInstallCommand(args[1:])
	case "uninstall":
		return dbNeonUninstallCommand(args[1:])
	default:
		return fmt.Errorf("unknown db neon command %q", args[0])
	}
}

type dbNeonOptions struct {
	AppRoot     string
	JSON        bool
	DestroyData bool
}

func parseDBNeonArgs(args []string) (dbNeonOptions, error) {
	var opts dbNeonOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbNeonOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--destroy-data":
			opts.DestroyData = true
		default:
			return dbNeonOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func dbNeonInstallCommand(args []string) error {
	opts, err := parseDBNeonArgs(args)
	if err != nil {
		return err
	}
	_, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		cfg = appcfg.Config{Name: neonDefaultProject}
	}
	_, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		svc = appcfg.DevServiceConfig{Kind: "neon", Mode: neonDefaultMode, Isolation: neonDefaultIsolation}
	}
	cell, err := ensureNeonDevCell(context.Background(), cfg, svc)
	if err != nil {
		return err
	}
	result := neonCellResult(cell)
	if opts.JSON {
		return writeInspectJSON(os.Stdout, result)
	}
	fmt.Fprintf(os.Stdout, "onlava Neon dev cell ready at %s\n", cell.Root)
	return nil
}

func dbNeonStatusCommand(args []string) error {
	opts, err := parseDBNeonArgs(args)
	if err != nil {
		return err
	}
	cell, err := readNeonCellState()
	if err != nil {
		return err
	}
	result := neonCellResult(cell)
	if opts.JSON {
		return writeInspectJSON(os.Stdout, result)
	}
	fmt.Fprintf(os.Stdout, "onlava Neon dev cell %s at %s\n", result.Status, result.Root)
	return nil
}

func neonCellResult(cell neonCellState) neonCellStatusResult {
	components := map[string]neonComponent{}
	for name, component := range cell.Components {
		components[name] = component
	}
	return neonCellStatusResult{
		SchemaVersion: "onlava.db.neon.status.v1",
		Provider:      cell.Provider,
		Mode:          cell.Mode,
		Status:        cell.Status,
		Backend:       cell.Backend,
		Version:       cell.Version,
		Root:          cell.Root,
		ComposePath:   cell.ComposePath,
		AdminURL:      redactedDatabaseURL(cell.AdminURL),
		Components:    components,
	}
}

func dbNeonLogsCommand(args []string) error {
	if _, err := parseDBNeonArgs(args); err != nil {
		return err
	}
	cell, err := readNeonCellState()
	if err != nil {
		return err
	}
	for name, component := range cell.Components {
		if component.LogPath == "" {
			continue
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\n", name, component.LogPath)
	}
	return nil
}

func dbNeonUninstallCommand(args []string) error {
	opts, err := parseDBNeonArgs(args)
	if err != nil {
		return err
	}
	if !opts.DestroyData {
		return fmt.Errorf("onlava db neon uninstall requires --destroy-data")
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	root := filepath.Join(paths.AgentDir, "substrates", "neon")
	if err := stopNeonDevCell(root); err != nil {
		return err
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.db.neon.uninstall.v1", "status": "removed", "root": root})
	}
	fmt.Fprintf(os.Stdout, "removed onlava Neon dev cell %s\n", root)
	return nil
}

func stopNeonDevCell(root string) error {
	postgresRoot := filepath.Join(root, "postgres")
	port, err := localPostgresPort(postgresRoot)
	if err != nil {
		return nil
	}
	containerName := managedPostgresContainerName(postgresRoot, devPostgresDefaultVersion, port)
	if docker, err := execLookPath("docker"); err == nil {
		_ = exec.Command(docker, "rm", "-f", containerName).Run()
	}
	if owner, err := verifyManagedPostgresPortOwner(postgresRoot); err == nil && owner.PID > 0 {
		_ = stopStaleSessionChildPID(context.Background(), owner.PID)
	}
	return nil
}

func dbBranchCommand(args []string) error {
	if len(args) == 0 {
		return dbBranchStatusCommand(nil)
	}
	switch args[0] {
	case "status":
		return dbBranchStatusCommand(args[1:])
	case "list":
		return dbBranchListCommand(args[1:])
	case "checkout":
		return dbBranchCheckoutCommand(args[1:])
	case "reset":
		return dbBranchResetCommand(args[1:])
	case "delete":
		return dbBranchDeleteCommand(args[1:])
	case "prune":
		return dbBranchPruneCommand(args[1:])
	case "restore":
		return dbBranchRestoreCommand(args[1:])
	case "diff":
		return dbBranchDiffCommand(args[1:])
	case "expire":
		return dbBranchExpireCommand(args[1:])
	default:
		return fmt.Errorf("unknown db branch command %q", args[0])
	}
}

type dbBranchOptions struct {
	AppRoot   string
	JSON      bool
	Yes       bool
	Force     bool
	Name      string
	OlderThan time.Duration
	At        string
	After     time.Duration
}

func parseDBBranchArgs(args []string, allowName bool) (dbBranchOptions, error) {
	var opts dbBranchOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--yes":
			opts.Yes = true
		case "--force":
			opts.Force = true
		case "--older-than":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --older-than")
			}
			d, err := parsePruneAge(args[i])
			if err != nil {
				return dbBranchOptions{}, err
			}
			opts.OlderThan = d
		case "--at":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --at")
			}
			opts.At = args[i]
		case "--after":
			i++
			if i >= len(args) {
				return dbBranchOptions{}, fmt.Errorf("missing value for --after")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return dbBranchOptions{}, fmt.Errorf("invalid value for --after: %w", err)
			}
			opts.After = d
		default:
			if allowName && opts.Name == "" {
				opts.Name = args[i]
				continue
			}
			return dbBranchOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func dbBranchStatusCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	lease, _, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	result := neonBranchStatusResultFor(appRoot, cfg, lease, "ready")
	if opts.JSON {
		return writeInspectJSON(os.Stdout, result)
	}
	fmt.Fprintf(os.Stdout, "onlava Neon branch %s (%s)\n", lease.Branch, lease.BranchID)
	return nil
}

func dbBranchListCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	wantProject := cfg.AppID()
	if _, svc, ok := managedPostgresUsesNeon(cfg); ok {
		wantProject = sanitizeNeonBranchSegment(firstNonEmpty(strings.TrimSpace(svc.Project), cfg.AppID(), neonDefaultProject))
	}
	state, _, err := readNeonBranchesState()
	if err != nil {
		return err
	}
	branches := make([]neonBranchLease, 0, len(state.Branches))
	for _, branch := range state.Branches {
		if wantProject != "" && branch.Project != wantProject {
			continue
		}
		branches = append(branches, branch)
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Branch < branches[j].Branch })
	result := neonBranchListResult{SchemaVersion: "onlava.db.branch.list.v1", App: appRef(appRoot, cfg), Project: wantProject, Branches: branches}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, result)
	}
	for _, branch := range branches {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", branch.BranchID, branch.Branch)
	}
	return nil
}

func dbBranchCheckoutCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("onlava db branch checkout requires a branch name")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	lease, _, err := ensureNeonLeaseForCLIWithBranch(context.Background(), appRoot, cfg, opts.Name)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, neonBranchStatusResultFor(appRoot, cfg, lease, "ready"))
	}
	fmt.Fprintf(os.Stdout, "checked out onlava Neon branch %s\n", lease.Branch)
	return nil
}

func dbBranchResetCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	if !opts.Yes {
		return fmt.Errorf("onlava db branch reset requires --yes")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	lease, adminURL, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	provider := newNeonSelfHostedBranchProvider(adminURL)
	if err := provider.ResetBranch(context.Background(), lease); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.db.branch.reset.v1", "branch": lease.Branch, "status": "reset"})
	}
	fmt.Fprintf(os.Stdout, "reset onlava Neon branch %s\n", lease.Branch)
	return nil
}

func dbBranchDeleteCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("onlava db branch delete requires a branch name")
	}
	if !opts.Force {
		return fmt.Errorf("onlava db branch delete requires --force")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	current, adminURL, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	lease, err := resolveNeonTargetLease(current, opts.Name)
	if err != nil {
		return err
	}
	name := lease.Branch
	if name == current.ParentBranch {
		return fmt.Errorf("refusing to delete protected parent branch %s", name)
	}
	if name == current.Branch && !opts.Yes {
		return fmt.Errorf("refusing to delete current branch %s without --yes", name)
	}
	if name != current.Branch && lease.CreatedBy != "onlava" {
		return fmt.Errorf("refusing to delete foreign Neon branch %s", name)
	}
	provider := newNeonSelfHostedBranchProvider(adminURL)
	if err := provider.DeleteBranch(context.Background(), lease); err != nil {
		return err
	}
	if name == current.Branch {
		_ = os.Remove(neonWorktreeLeasePath(appRoot))
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.db.branch.delete.v1", "branch": name, "status": "deleted"})
	}
	fmt.Fprintf(os.Stdout, "deleted onlava Neon branch %s\n", name)
	return nil
}

func dbBranchPruneCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	if opts.OlderThan <= 0 {
		opts.OlderThan = 168 * time.Hour
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	_, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		return fmt.Errorf("dev.services.postgres kind neon is not configured")
	}
	cell, err := ensureNeonDevCell(context.Background(), cfg, svc)
	if err != nil {
		return err
	}
	state, _, err := readNeonBranchesState()
	if err != nil {
		return err
	}
	wantProject := sanitizeNeonBranchSegment(firstNonEmpty(strings.TrimSpace(svc.Project), cfg.AppID(), neonDefaultProject))
	cutoff := time.Now().UTC().Add(-opts.OlderThan)
	var pruned []string
	provider := newNeonSelfHostedBranchProvider(cell.AdminURL)
	for _, branch := range state.Branches {
		if branch.CreatedBy != "onlava" || branch.UpdatedAt.After(cutoff) || branch.Branch == branch.ParentBranch {
			continue
		}
		if wantProject != "" && branch.Project != wantProject {
			continue
		}
		if err := provider.DeleteBranch(context.Background(), branch); err != nil {
			return err
		}
		pruned = append(pruned, branch.Branch)
	}
	sort.Strings(pruned)
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.db.branch.prune.v1", "app": appRef(appRoot, cfg), "project": wantProject, "pruned": pruned})
	}
	fmt.Fprintf(os.Stdout, "pruned %d onlava Neon branches\n", len(pruned))
	return nil
}

func ensureNeonLeaseForCLI(ctx context.Context, appRoot string, cfg appcfg.Config) (neonBranchLease, string, error) {
	return ensureNeonLeaseForCLIWithBranch(ctx, appRoot, cfg, "")
}

func ensureNeonLeaseForCLIWithBranch(ctx context.Context, appRoot string, cfg appcfg.Config, branch string) (neonBranchLease, string, error) {
	name, svc, ok := managedPostgresUsesNeon(cfg)
	if !ok {
		return neonBranchLease{}, "", fmt.Errorf("dev.services.postgres kind neon is not configured")
	}
	if err := validateNeonPostgresConfig(name, svc); err != nil {
		return neonBranchLease{}, "", err
	}
	cell, err := ensureNeonDevCell(ctx, cfg, svc)
	if err != nil {
		return neonBranchLease{}, "", err
	}
	session := &localagent.Session{SessionID: localagentLabel(filepath.Base(appRoot)), BaseAppID: cfg.AppID(), AppRoot: appRoot, Branch: gitBranchName(appRoot), StateRoot: filepath.Join(appRoot, ".onlava", "sessions", "cli")}
	if branch != "" {
		svc.BranchPolicy = "worktree"
		svc.BranchNameTemplate = branch
	}
	cfg.Dev.Services["postgres"] = svc
	lease, _, err := ensureNeonBranchLease(ctx, cfg, session, cell.AdminURL)
	return lease, cell.AdminURL, err
}

func neonBranchStatusResultFor(appRoot string, cfg appcfg.Config, lease neonBranchLease, status string) neonBranchStatusResult {
	envName := appDatabaseURLEnv
	if _, svc, ok := managedPostgresUsesNeon(cfg); ok {
		envName = firstNonEmpty(strings.TrimSpace(svc.DatabaseURLEnv), appDatabaseURLEnv)
	}
	connection := "redacted"
	if cell, err := readNeonCellState(); err == nil && strings.TrimSpace(cell.AdminURL) != "" {
		if dbURL, urlErr := postgresDatabaseURL(cell.AdminURL, lease.DatabaseName); urlErr == nil {
			connection = redactedDatabaseURL(dbURL)
		}
	}
	return neonBranchStatusResult{
		SchemaVersion: "onlava.db.branch.status.v1",
		App:           appRef(appRoot, cfg),
		Database: neonDatabaseStatus{
			Provider:       "neon",
			Mode:           neonDefaultMode,
			Project:        lease.Project,
			Branch:         lease.Branch,
			BranchID:       lease.BranchID,
			ParentBranch:   lease.ParentBranch,
			DatabaseURLEnv: envName,
			Status:         status,
			TTL:            lease.TTL,
			ExpiresAt:      lease.ExpiresAt,
			PSQLCommand:    "onlava db psql",
			ResetCommand:   "onlava db branch reset --yes",
			RestoreCommand: "onlava db branch restore --at <restore-point> --yes",
			DiffCommand:    "onlava db branch diff <branch>",
			ExpireCommand:  "onlava db branch expire --after <duration>",
			LogsCommand:    "onlava db neon logs",
			Connection:     connection,
		},
	}
}

func appRef(appRoot string, cfg appcfg.Config) inspectdata.AppRef {
	return inspectdata.AppRef{Name: cfg.Name, ID: cfg.ID, Root: appRoot, ConfigPath: filepath.Join(appRoot, ".onlava.json")}
}

func worktreeCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: onlava worktree create|list|remove")
	}
	switch args[0] {
	case "create":
		return worktreeCreateCommand(args[1:])
	case "list":
		return worktreeListCommand(args[1:])
	case "remove":
		return worktreeRemoveCommand(args[1:])
	default:
		return fmt.Errorf("unknown worktree command %q", args[0])
	}
}

type worktreeOptions struct {
	Name    string
	From    string
	AppRoot string
	JSON    bool
	DB      bool
}

func parseWorktreeArgs(args []string, needName bool) (worktreeOptions, error) {
	var opts worktreeOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			i++
			if i >= len(args) {
				return worktreeOptions{}, fmt.Errorf("missing value for --from")
			}
			opts.From = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return worktreeOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--db":
			opts.DB = true
		default:
			if opts.Name == "" {
				opts.Name = args[i]
				continue
			}
			return worktreeOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if needName && strings.TrimSpace(opts.Name) == "" {
		return worktreeOptions{}, fmt.Errorf("missing worktree name")
	}
	return opts, nil
}

func worktreeCreateCommand(args []string) error {
	opts, err := parseWorktreeArgs(args, true)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	name := localagentLabel(opts.Name)
	if name == "" {
		return fmt.Errorf("worktree name must contain at least one letter or digit")
	}
	parent := filepath.Dir(appRoot)
	target := filepath.Join(parent, filepath.Base(appRoot)+"-"+name)
	from := firstNonEmpty(strings.TrimSpace(opts.From), "HEAD")
	cmd := exec.Command("git", "-C", appRoot, "worktree", "add", target, "-b", name, from)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	lease, _, err := ensureNeonLeaseForCLIWithBranch(context.Background(), target, cfg, cfg.AppID()+"/"+name)
	if err != nil {
		return err
	}
	result := map[string]any{"schema_version": "onlava.worktree.create.v1", "name": name, "path": target, "branch": lease.Branch, "branch_id": lease.BranchID, "next": "cd " + target + " && onlava up"}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, result)
	}
	fmt.Fprintf(os.Stdout, "created worktree %s\nnext: cd %s && onlava up\n", target, target)
	return nil
}

func worktreeListCommand(args []string) error {
	opts, err := parseWorktreeArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", appRoot, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	items := parseGitWorktreeList(string(out))
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.worktree.list.v1", "worktrees": items})
	}
	for _, item := range items {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", item["branch"], item["path"])
	}
	return nil
}

func parseGitWorktreeList(output string) []map[string]string {
	var items []map[string]string
	current := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(current) > 0 {
				items = append(items, current)
				current = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if key == "worktree" {
			current["path"] = value
		} else if key == "branch" {
			current["branch"] = strings.TrimPrefix(value, "refs/heads/")
		}
	}
	if len(current) > 0 {
		items = append(items, current)
	}
	return items
}

func worktreeRemoveCommand(args []string) error {
	opts, err := parseWorktreeArgs(args, true)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	name := localagentLabel(opts.Name)
	target := filepath.Join(filepath.Dir(appRoot), filepath.Base(appRoot)+"-"+name)
	if opts.DB {
		if lease, leaseErr := readNeonWorktreeLease(target); leaseErr == nil {
			if _, svc, ok := managedPostgresUsesNeon(cfg); ok {
				cell, cellErr := ensureNeonDevCell(context.Background(), cfg, svc)
				if cellErr != nil {
					return cellErr
				}
				if lease.Branch != lease.ParentBranch {
					if err := dropManagedPostgresDatabase(context.Background(), cell.AdminURL, lease.DatabaseName); err != nil {
						return err
					}
				}
			}
			_ = removeNeonGlobalBranch(lease.BranchID)
		}
		_ = os.Remove(neonWorktreeLeasePath(target))
	}
	cmd := exec.Command("git", "-C", appRoot, "worktree", "remove", target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "onlava.worktree.remove.v1", "name": name, "path": target, "db": opts.DB})
	}
	fmt.Fprintf(os.Stdout, "removed worktree %s\n", target)
	return nil
}

func writeJSONFile(path string, value any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, perm)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func copyPostgresDatabase(ctx context.Context, adminURL, sourceDB, targetDB string) error {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	quotedTarget := pq.QuoteIdentifier(targetDB)
	quotedSource := pq.QuoteIdentifier(sourceDB)
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, targetDB); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quotedTarget); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+quotedTarget+" WITH TEMPLATE "+quotedSource)
	return err
}

func redactedDatabaseURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return "redacted"
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
	return parsed.String()
}

func discardOutput(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return io.Discard
}

func parsePositiveInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}
