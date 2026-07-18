package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"strings"

	"scenery.sh/internal/postgresname"
)

const (
	PrimaryConfigFilename = ".scenery.json"
)

var ErrRootNotFound = errors.New("no .scenery.json found in current directory or any parent")

type Config struct {
	ConfigPath    string                    `json:"-"`
	Name          string                    `json:"name"`
	ID            string                    `json:"id"`
	Build         BuildConfig               `json:"build"`
	Frontends     map[string]FrontendConfig `json:"frontends"`
	Envs          map[string]EnvConfig      `json:"envs"`
	Watch         WatchConfig               `json:"watch"`
	Dev           DevConfig                 `json:"dev"`
	Storage       StorageConfig             `json:"storage"`
	Generators    GeneratorsConfig          `json:"generators"`
	Database      DatabaseConfig            `json:"database"`
	Validation    ValidationConfig          `json:"validation"`
	Auth          AuthConfig                `json:"auth"`
	Observability ObservabilityConfig       `json:"observability"`
}

// MarshalJSON omits optional object sections when they are not configured.
// A zero struct cannot be omitted by encoding/json's omitempty handling.
func (c Config) MarshalJSON() ([]byte, error) {
	type configJSON struct {
		Name          string                    `json:"name"`
		ID            string                    `json:"id"`
		Build         BuildConfig               `json:"build"`
		Frontends     map[string]FrontendConfig `json:"frontends"`
		Envs          map[string]EnvConfig      `json:"envs"`
		Watch         WatchConfig               `json:"watch"`
		Dev           DevConfig                 `json:"dev"`
		Storage       *StorageConfig            `json:"storage,omitempty"`
		Generators    GeneratorsConfig          `json:"generators"`
		Database      DatabaseConfig            `json:"database"`
		Validation    ValidationConfig          `json:"validation"`
		Auth          AuthConfig                `json:"auth"`
		Observability ObservabilityConfig       `json:"observability"`
	}
	out := configJSON{
		Name: c.Name, ID: c.ID, Build: c.Build, Frontends: c.Frontends, Envs: c.Envs,
		Watch: c.Watch, Dev: c.Dev, Generators: c.Generators,
		Database: c.Database, Validation: c.Validation, Auth: c.Auth,
		Observability: c.Observability,
	}
	if !c.Storage.IsZero() {
		storage := c.Storage
		if storage.Stores == nil {
			storage.Stores = map[string]StorageStoreConfig{}
		}
		out.Storage = &storage
	}
	return json.Marshal(out)
}

func (c Config) AppID() string {
	if c.ID != "" {
		return c.ID
	}
	return c.Name
}

func (c Config) SourcePath(appRoot string) string {
	if c.ConfigPath != "" {
		return c.ConfigPath
	}
	return ConfigPath(appRoot)
}

func (c Config) SourceRelPath(appRoot string) string {
	rel, err := filepath.Rel(appRoot, c.SourcePath(appRoot))
	if err != nil {
		return filepath.ToSlash(filepath.Base(c.SourcePath(appRoot)))
	}
	return filepath.ToSlash(rel)
}

func ConfigPath(appRoot string) string {
	return filepath.Join(appRoot, PrimaryConfigFilename)
}

func ResolveConfigPath(appRoot string) (string, error) {
	return ConfigPath(appRoot), nil
}

func IsConfigFilename(name string) bool {
	return filepath.Base(name) == PrimaryConfigFilename
}

func (c Config) StorageCellID() string {
	if cellID := strings.TrimSpace(c.Storage.CellID); cellID != "" {
		return cellID
	}
	return storageSlug(c.AppID())
}

func (c Config) DatabaseServices() []DatabaseServiceConfig {
	out := make([]DatabaseServiceConfig, 0, len(c.Dev.Services))
	for name, svc := range c.Dev.Services {
		schema, err := postgresname.SchemaNameFor(name)
		if err != nil {
			schema = ""
		}
		out = append(out, DatabaseServiceConfig{
			Name:   name,
			Schema: schema,
			Raw:    svc,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (c Config) DatabaseService(name string) (DatabaseServiceConfig, bool) {
	for _, svc := range c.DatabaseServices() {
		if svc.Name == name {
			return svc, true
		}
	}
	return DatabaseServiceConfig{}, false
}

type DatabaseServiceConfig struct {
	Name   string
	Schema string
	Raw    DevServiceConfig
}

func (c Config) PostgresServices() []PostgresServiceConfig {
	services := c.DatabaseServices()
	out := make([]PostgresServiceConfig, 0, len(services))
	for _, svc := range services {
		out = append(out, PostgresServiceConfig{
			Name:          svc.Name,
			DatabaseLabel: svc.Schema,
			Schema:        svc.Schema,
			Raw:           svc.Raw,
		})
	}
	return out
}

func (c Config) PostgresService(name string) (PostgresServiceConfig, bool) {
	for _, svc := range c.PostgresServices() {
		if svc.Name == name {
			return svc, true
		}
	}
	return PostgresServiceConfig{}, false
}

type PostgresServiceConfig struct {
	Name          string
	DatabaseLabel string
	Schema        string
	Raw           DevServiceConfig
}

type BuildConfig struct {
	GoFlags []string `json:"go_flags"`
}

type WatchConfig struct {
	Ignore []string `json:"ignore"`
}

type FrontendConfig struct {
	Root                string `json:"root"`
	Upstream            string `json:"upstream"`
	AllowSharedUpstream bool   `json:"allow_shared_upstream"`
	Serve               string `json:"-"`
}

type DevConfig struct {
	Services map[string]DevServiceConfig `json:"services"`
}

type EnvConfig struct {
	Default   bool                         `json:"default"`
	Mode      string                       `json:"mode"`
	Domain    string                       `json:"domain"`
	Expose    []string                     `json:"expose"`
	Port      int                          `json:"port"`
	PortStart int                          `json:"port_start"`
	PortEnd   int                          `json:"port_end"`
	Frontends map[string]EnvFrontendConfig `json:"frontends"`
	Libraries map[string]EnvLibraryConfig  `json:"libraries,omitempty"`
	Deploy    *EnvDeployConfig             `json:"deploy,omitempty"`
	// UICatalog points local development at a live @scenery/ui catalog
	// source directory instead of the binary-embedded copy. Only the
	// default local environment may set it.
	UICatalog string `json:"ui_catalog,omitempty"`
}

type EnvFrontendConfig struct {
	Serve string `json:"serve"`
}

type EnvLibraryConfig struct {
	Linkage  string `json:"linkage"`
	Manifest string `json:"manifest,omitempty"`
}

type EnvDeployConfig struct {
	Root string   `json:"root,omitempty"`
	SSH  []string `json:"ssh,omitempty"`
}

type ResolvedEnv struct {
	Name      string
	Default   bool
	Domain    string
	Mode      string
	Expose    []string
	Port      int
	PortStart int
	PortEnd   int
	Frontends map[string]FrontendConfig
	Libraries map[string]EnvLibraryConfig
	Deploy    *EnvDeployConfig
	UICatalog string
}

func (c Config) ResolveEnv(name string) (ResolvedEnv, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		for candidate, env := range c.Envs {
			if env.Default {
				name = candidate
				break
			}
		}
	}
	env, ok := c.Envs[name]
	if !ok {
		return ResolvedEnv{}, fmt.Errorf("environment %q is not declared in envs", name)
	}
	frontends := make(map[string]FrontendConfig, len(c.Frontends))
	for frontendName, frontend := range c.Frontends {
		frontend.Serve = "development"
		if override, exists := env.Frontends[frontendName]; exists && strings.TrimSpace(override.Serve) != "" {
			frontend.Serve = strings.ToLower(strings.TrimSpace(override.Serve))
		}
		frontends[frontendName] = frontend
	}
	return ResolvedEnv{
		Name: name, Default: env.Default, Domain: strings.TrimSpace(env.Domain),
		Mode: strings.ToLower(strings.TrimSpace(env.Mode)), Expose: append([]string(nil), env.Expose...),
		Port: env.Port, PortStart: env.PortStart, PortEnd: env.PortEnd,
		Frontends: frontends, Deploy: env.Deploy,
		Libraries: cloneLibraryConfig(env.Libraries),
		UICatalog: strings.TrimSpace(env.UICatalog),
	}, nil
}

func cloneLibraryConfig(values map[string]EnvLibraryConfig) map[string]EnvLibraryConfig {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]EnvLibraryConfig, len(values))
	for name, value := range values {
		value.Linkage = strings.ToLower(strings.TrimSpace(value.Linkage))
		value.Manifest = strings.TrimSpace(value.Manifest)
		result[name] = value
	}
	return result
}

func (c Config) EnvForSSHTarget(target string) (ResolvedEnv, error) {
	for name, env := range c.Envs {
		if env.Deploy != nil {
			for _, candidate := range env.Deploy.SSH {
				if candidate == target {
					return c.ResolveEnv(name)
				}
			}
		}
	}
	return ResolvedEnv{}, fmt.Errorf("SSH target %q is not configured in any environment", target)
}

func (e ResolvedEnv) DotEnvFiles() []string {
	if e.Name == "local" {
		return []string{".env", ".env.local"}
	}
	return []string{".env", ".env." + e.Name, ".env.local", ".env." + e.Name + ".local"}
}

func (e ResolvedEnv) Deployable() bool {
	return e.Deploy != nil
}

// UICatalogDir resolves the environment's ui_catalog development override
// against the app root. missing is true when the override is set but the
// directory does not exist — callers fall back to the embedded catalog so a
// committed relative path never breaks machines without the source checkout.
// A directory that exists without index.ts and package.json is a
// misconfiguration and returns an error.
func (e ResolvedEnv) UICatalogDir(appRoot string) (dir string, missing bool, err error) {
	configured := strings.TrimSpace(e.UICatalog)
	if configured == "" {
		return "", false, nil
	}
	dir = configured
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(appRoot, dir)
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("envs.%s.ui_catalog %q is not a directory", e.Name, configured)
	}
	for _, marker := range []string{"index.ts", "package.json"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return "", false, fmt.Errorf("envs.%s.ui_catalog %q is not a UI catalog root: missing %s", e.Name, configured, marker)
		}
	}
	return dir, false, nil
}

type DevServiceConfig struct {
	Env map[string]string `json:"env,omitempty"`
}

type StorageConfig struct {
	CellID  string                        `json:"cell_id,omitempty"`
	Share   string                        `json:"share,omitempty"`
	Default string                        `json:"default,omitempty"`
	Stores  map[string]StorageStoreConfig `json:"stores,omitempty"`
}

func (c StorageConfig) IsZero() bool {
	return c.CellID == "" && c.Share == "" && c.Default == "" && len(c.Stores) == 0
}

type StorageStoreConfig struct {
	Kind           string `json:"kind"`
	Access         string `json:"access,omitempty"`
	TenantScoped   bool   `json:"tenant_scoped,omitempty"`
	MaxObjectBytes int64  `json:"max_object_bytes,omitempty"`
}

type GeneratorsConfig struct {
	SQLC SQLCGeneratorConfig `json:"sqlc"`
}

type SQLCGeneratorConfig struct {
	Provider string                `json:"provider"`
	Config   string                `json:"config"`
	DevURL   string                `json:"dev_url"`
	Schemas  []SQLCGeneratorSchema `json:"schemas"`
}

type SQLCGeneratorSchema struct {
	SQLCSchema  string `json:"sqlc_schema"`
	AtlasSchema string `json:"atlas_schema"`
	AtlasSource string `json:"atlas_source"`
	AtlasDevURL string `json:"atlas_dev_url"`
}

type DatabaseConfig struct {
	Apply DatabaseApplyConfig `json:"apply"`
	Seed  DatabaseSeedConfig  `json:"seed"`
}

type DatabaseApplyConfig struct {
	Command string            `json:"command"`
	CWD     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
}

type DatabaseSeedConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (c DatabaseSeedConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type ValidationConfig struct {
	Default  string                             `json:"default"`
	Profiles map[string]ValidationProfileConfig `json:"profiles"`
}

type ValidationProfileConfig struct {
	Description string            `json:"description"`
	Cost        string            `json:"cost"`
	Paths       []string          `json:"paths"`
	Steps       []string          `json:"steps"`
	Env         map[string]string `json:"env"`
	Artifacts   []string          `json:"artifacts"`
}

type AuthConfig struct {
	Enabled               bool             `json:"enabled"`
	AutoBootstrapDatabase bool             `json:"auto_bootstrap_database"`
	GoogleOAuth           AuthGoogleConfig `json:"google_oauth"`
	DevBootstrap          AuthDevBootstrap `json:"dev_bootstrap"`
}

type AuthGoogleConfig struct {
	Enabled       bool     `json:"enabled"`
	AllowedScopes []string `json:"allowed_scopes"`
}

type AuthDevBootstrap struct {
	Enabled          bool   `json:"enabled"`
	DefaultUserEmail string `json:"default_user_email"`
	DefaultUserID    string `json:"default_user_id"`
	DefaultTenantID  string `json:"default_tenant_id"`
}

type ObservabilityConfig struct {
	Logs    EndpointFilterConfig `json:"logs"`
	Tracing EndpointFilterConfig `json:"tracing"`
}

type EndpointFilterConfig struct {
	IncludeEndpoints []string `json:"include_endpoints"`
	ExcludeEndpoints []string `json:"exclude_endpoints"`
}

func DiscoverRoot(start string) (string, Config, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, err
	}
	for {
		path, data, err := readConfigCandidate(dir)
		if err != nil {
			return "", Config{}, err
		}
		if path != "" {
			var cfg Config
			if err := decodeConfig(path, data, &cfg); err != nil {
				return "", Config{}, err
			}
			cfg.ConfigPath = path
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}
			if cfg.Name == "" {
				return "", Config{}, fmt.Errorf("%s must define a non-empty name or id", filepath.Base(path))
			}
			if err := cfg.Validate(); err != nil {
				return "", Config{}, fmt.Errorf("%s: %w", path, err)
			}
			return dir, cfg, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", Config{}, ErrRootNotFound
}

func readConfigCandidate(dir string) (string, []byte, error) {
	path := filepath.Join(dir, PrimaryConfigFilename)
	data, err := os.ReadFile(path)
	if err == nil {
		return path, data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	return "", nil, nil
}

func (c Config) Validate() error {
	if err := c.validateWatch(); err != nil {
		return err
	}
	if err := c.validateDevServices(); err != nil {
		return err
	}
	if err := c.validateEnvs(); err != nil {
		return err
	}
	return c.validateStorage()
}

func (c Config) validateWatch() error {
	for _, pattern := range c.Watch.Ignore {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return errors.New("watch.ignore contains an empty pattern")
		}
		clean := filepath.ToSlash(filepath.Clean(strings.TrimRight(pattern, "/")))
		if filepath.IsAbs(pattern) || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
			return fmt.Errorf("watch.ignore pattern %q must be app-root-relative", pattern)
		}
		if strings.HasPrefix(pattern, "!") {
			return fmt.Errorf("watch.ignore pattern %q is invalid; watch.ignore only supports exclusions", pattern)
		}
	}
	return nil
}

func (c Config) validateDevServices() error {
	schemaOwners := map[string]string{}
	for name := range c.Dev.Services {
		if !isStorageIdentifier(name) {
			return fmt.Errorf("dev.services.%s name is invalid; use lowercase letters, numbers, dots, underscores, or dashes", name)
		}
		schema, err := postgresname.SchemaNameFor(name)
		if err != nil {
			return fmt.Errorf("dev.services.%s name maps to an invalid Postgres schema: %w", name, err)
		}
		if previous := schemaOwners[schema]; previous != "" {
			return fmt.Errorf("dev.services.%s and dev.services.%s both map to Postgres schema %q", previous, name, schema)
		}
		schemaOwners[schema] = name
	}
	return nil
}

func (c Config) validateEnvs() error {
	if len(c.Envs) == 0 {
		return errors.New("envs must declare environments and exactly one default")
	}
	defaults := 0
	seenSSH := map[string]string{}
	for name, env := range c.Envs {
		if !isStorageIdentifier(name) {
			return fmt.Errorf("envs.%s name is invalid; use lowercase letters, numbers, dots, underscores, or dashes", name)
		}
		if env.Default {
			defaults++
		}
		if name == "local" && !env.Default {
			return errors.New("envs.local must be the default environment")
		}
		if name == "local" && env.Deploy != nil {
			return errors.New("envs.local must not declare deploy")
		}
		if strings.TrimSpace(env.UICatalog) != "" && name != "local" {
			return fmt.Errorf("envs.%s.ui_catalog is a local development override; only envs.local may set it", name)
		}
		domain := strings.TrimSpace(env.Domain)
		if domain != "" {
			if domain != strings.ToLower(domain) {
				return fmt.Errorf("envs.%s.domain must be lowercase", name)
			}
			if err := validateEnvDomain(domain); err != nil {
				return fmt.Errorf("envs.%s.domain: %w", name, err)
			}
		}
		for frontendName, override := range env.Frontends {
			if _, ok := c.Frontends[frontendName]; !ok {
				return fmt.Errorf("envs.%s.frontends.%s does not match a configured frontend", name, frontendName)
			}
			serve := strings.ToLower(strings.TrimSpace(override.Serve))
			if serve != "development" && serve != "production" {
				return fmt.Errorf("envs.%s.frontends.%s.serve must be \"development\" or \"production\"", name, frontendName)
			}
		}
		for libraryName, library := range env.Libraries {
			if !isStorageIdentifier(libraryName) {
				return fmt.Errorf("envs.%s.libraries.%s name is invalid; use lowercase letters, numbers, dots, underscores, or dashes", name, libraryName)
			}
			linkage := strings.ToLower(strings.TrimSpace(library.Linkage))
			if linkage != "source" && linkage != "shared" {
				return fmt.Errorf("envs.%s.libraries.%s.linkage must be \"source\" or \"shared\"", name, libraryName)
			}
			if linkage == "shared" && strings.TrimSpace(library.Manifest) == "" {
				return fmt.Errorf("envs.%s.libraries.%s.manifest is required for shared linkage", name, libraryName)
			}
			if filepath.IsAbs(library.Manifest) {
				return fmt.Errorf("envs.%s.libraries.%s.manifest must be relative to the app root", name, libraryName)
			}
			if cleaned := filepath.Clean(library.Manifest); library.Manifest != "" && (cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))) {
				return fmt.Errorf("envs.%s.libraries.%s.manifest must stay beneath the app root", name, libraryName)
			}
		}
		if env.Deploy == nil {
			continue
		}
		for frontendName := range c.Frontends {
			if strings.ToLower(strings.TrimSpace(env.Frontends[frontendName].Serve)) != "production" {
				return fmt.Errorf("envs.%s.frontends.%s.serve must be \"production\" for a deployable environment", name, frontendName)
			}
		}
		for index, target := range env.Deploy.SSH {
			if !validDeploySSHTarget(target) {
				return fmt.Errorf("envs.%s.deploy.ssh[%d] %q must be a safe OpenSSH host alias and not a scenery deploy subcommand", name, index, target)
			}
			if previous, exists := seenSSH[target]; exists {
				return fmt.Errorf("envs.%s.deploy.ssh[%d] duplicates target %q from envs.%s", name, index, target, previous)
			}
			seenSSH[target] = name
		}
		if len(env.Deploy.SSH) > 0 && !validDeploySSHAppID(c.AppID()) {
			return fmt.Errorf("app id %q must start with a lowercase letter or number and use only lowercase letters, numbers, dots, underscores, or dashes for SSH deployment", c.AppID())
		}
		root := strings.TrimSpace(env.Deploy.Root)
		switch root {
		case "":
		case "console", "dashboard", "runtime", "__scenery":
			return fmt.Errorf("envs.%s.deploy.root %q is reserved by Scenery", name, root)
		case "api":
		default:
			if _, ok := c.Frontends[root]; !ok {
				return fmt.Errorf("envs.%s.deploy.root %q must be \"api\" or a configured frontend", name, root)
			}
		}
	}
	if defaults != 1 {
		return fmt.Errorf("envs must declare exactly one default environment; found %d", defaults)
	}
	if _, ok := c.Envs["local"]; !ok {
		return errors.New("envs.local must declare the default local environment")
	}
	return nil
}

func validDeploySSHTarget(target string) bool {
	if target == "" || target == "plan" || target == "apply" || target == "setup" || target == "status" || target == "enable" || target == "disable" || target == "publish" || target == "resume" || target == "teardown" {
		return false
	}
	for index, r := range target {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (index > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func validDeploySSHAppID(value string) bool {
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (index > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return value != ""
}

func validateEnvDomain(domain string) error {
	if domain == "localhost" {
		return errors.New("must not be localhost")
	}
	if ip := net.ParseIP(strings.Trim(domain, "[]")); ip != nil {
		return errors.New("must not be an IP address")
	}
	if !validDeployFQDN(domain) {
		return fmt.Errorf("%q must be a valid lowercase FQDN", domain)
	}
	base := "local.dev"
	if domain == base || strings.HasSuffix(domain, "."+base) {
		return fmt.Errorf("%q must not use the local route base domain %q", domain, base)
	}
	return nil
}

func validDeployFQDN(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func (c Config) validateStorage() error {
	cfg := c.Storage
	if cfg.CellID == "" && cfg.Share == "" && cfg.Default == "" && len(cfg.Stores) == 0 {
		return nil
	}
	if strings.TrimSpace(cfg.CellID) != "" && !isStorageIdentifier(cfg.CellID) {
		return fmt.Errorf("storage.cell_id %q is invalid; use lowercase letters, numbers, dots, underscores, or dashes", cfg.CellID)
	}
	share := strings.TrimSpace(cfg.Share)
	switch share {
	case "", "worktree":
	default:
		return fmt.Errorf("storage.share %q is not supported; use %q", share, "worktree")
	}
	if len(cfg.Stores) == 0 {
		return errors.New("storage.stores must define at least one store")
	}
	for name, store := range cfg.Stores {
		if strings.TrimSpace(name) == "" {
			return errors.New("storage.stores contains an empty store name")
		}
		if !isStorageIdentifier(name) {
			return fmt.Errorf("storage.stores.%s name is invalid; use lowercase letters, numbers, dots, underscores, or dashes", name)
		}
		kind := strings.TrimSpace(store.Kind)
		switch kind {
		case "", "local":
		default:
			return fmt.Errorf("storage.stores.%s.kind %q is not supported; use %q (ZeroFS was removed in plan 0094)", name, kind, "local")
		}
		access := strings.TrimSpace(store.Access)
		switch access {
		case "", "auth", "private":
		default:
			return fmt.Errorf("storage.stores.%s.access %q is not supported; use %q or %q", name, access, "auth", "private")
		}
		if store.MaxObjectBytes < 0 {
			return fmt.Errorf("storage.stores.%s.max_object_bytes must be >= 0", name)
		}
	}
	if def := strings.TrimSpace(cfg.Default); def != "" {
		if _, ok := cfg.Stores[def]; !ok {
			return fmt.Errorf("storage.default %q does not match a configured store", def)
		}
	}
	return nil
}

func isStorageIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func storageSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

func decodeConfig(path string, data []byte, cfg *Config) error {
	if err := rejectUnknownConfigFields(path, data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("%s: decode %s: %w", path, filepath.Base(path), err)
	}
	return nil
}

func ReadWatchIgnorePatterns(appRoot string) ([]string, error) {
	path, data, err := readConfigCandidate(appRoot)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	var partial struct {
		Watch WatchConfig `json:"watch"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return nil, fmt.Errorf("%s: decode %s watch.ignore: %w", path, filepath.Base(path), err)
	}
	return append([]string(nil), partial.Watch.Ignore...), nil
}

func rejectUnknownConfigFields(path string, data []byte) error {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%s: decode %s: %w", path, filepath.Base(path), err)
	}
	if err := rejectUnknownFieldsValue(raw, reflect.TypeFor[Config](), nil, filepath.Base(path)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func rejectUnknownFieldsValue(value any, typ reflect.Type, path []string, configName string) error {
	typ = indirectType(typ)
	switch typ.Kind() {
	case reflect.Struct:
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		fields := jsonStructFields(typ)
		for name, child := range obj {
			field, ok := fields[name]
			childPath := appendJSONPath(path, name)
			if !ok {
				return unknownConfigFieldError(childPath, configName)
			}
			if err := rejectUnknownFieldsValue(child, field.Type, childPath, configName); err != nil {
				return err
			}
		}
	case reflect.Map:
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		elem := indirectType(typ.Elem())
		if elem.Kind() != reflect.Struct {
			return nil
		}
		for name, child := range obj {
			if err := rejectUnknownFieldsValue(child, elem, appendJSONPath(path, name), configName); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for i, child := range items {
			if err := rejectUnknownFieldsValue(child, typ.Elem(), appendJSONIndex(path, i), configName); err != nil {
				return err
			}
		}
	}
	return nil
}

func unknownConfigFieldError(path []string, configName string) error {
	jsonPath := strings.Join(path, ".")
	return fmt.Errorf("unknown %s field %q", configName, jsonPath)
}

func jsonStructFields(typ reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField)
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
		}
		if name == "-" {
			continue
		}
		fields[name] = field
	}
	return fields
}

func indirectType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func appendJSONPath(path []string, field string) []string {
	next := make([]string, 0, len(path)+1)
	next = append(next, path...)
	next = append(next, field)
	return next
}

func appendJSONIndex(path []string, index int) []string {
	next := make([]string, len(path))
	copy(next, path)
	if len(next) == 0 {
		return []string{fmt.Sprintf("[%d]", index)}
	}
	next[len(next)-1] = fmt.Sprintf("%s[%d]", next[len(next)-1], index)
	return next
}

func RepoRoot() string {
	_, file, _, _ := goruntime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
