package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"strings"
)

const (
	PrimaryConfigFilename = ".scenery.json"
	AliasConfigFilename   = ".config.json"
)

var ErrRootNotFound = errors.New("no .scenery.json or .config.json found in current directory or any parent")

type Config struct {
	ConfigPath    string                `json:"-"`
	Name          string                `json:"name"`
	ID            string                `json:"id"`
	Build         BuildConfig           `json:"build"`
	Proxy         ProxyConfig           `json:"proxy"`
	Watch         WatchConfig           `json:"watch"`
	Dev           DevConfig             `json:"dev"`
	Storage       StorageConfig         `json:"storage"`
	Generators    GeneratorsConfig      `json:"generators"`
	Database      DatabaseConfig        `json:"database"`
	Tasks         map[string]TaskConfig `json:"tasks"`
	Validation    ValidationConfig      `json:"validation"`
	Auth          AuthConfig            `json:"auth"`
	Observability ObservabilityConfig   `json:"observability"`
}

func (c Config) MarshalJSON() ([]byte, error) {
	type configJSON struct {
		Name          string                `json:"name"`
		ID            string                `json:"id"`
		Build         BuildConfig           `json:"build"`
		Proxy         ProxyConfig           `json:"proxy"`
		Watch         WatchConfig           `json:"watch"`
		Dev           DevConfig             `json:"dev"`
		Storage       *StorageConfig        `json:"storage,omitempty"`
		Generators    GeneratorsConfig      `json:"generators"`
		Database      DatabaseConfig        `json:"database"`
		Tasks         map[string]TaskConfig `json:"tasks"`
		Validation    ValidationConfig      `json:"validation"`
		Auth          AuthConfig            `json:"auth"`
		Observability ObservabilityConfig   `json:"observability"`
	}
	out := configJSON{
		Name:          c.Name,
		ID:            c.ID,
		Build:         c.Build,
		Proxy:         c.Proxy,
		Watch:         c.Watch,
		Dev:           c.Dev,
		Generators:    c.Generators,
		Database:      c.Database,
		Tasks:         c.Tasks,
		Validation:    c.Validation,
		Auth:          c.Auth,
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
	path, _, err := readConfigCandidate(appRoot)
	if err != nil {
		return "", err
	}
	if path != "" {
		return path, nil
	}
	return ConfigPath(appRoot), nil
}

func IsConfigFilename(name string) bool {
	switch filepath.Base(name) {
	case PrimaryConfigFilename, AliasConfigFilename:
		return true
	default:
		return false
	}
}

func (c Config) StorageCellID() string {
	if cellID := strings.TrimSpace(c.Storage.CellID); cellID != "" {
		return cellID
	}
	return storageSlug(c.AppID())
}

func (c Config) DatabaseURLEnv() string {
	services := c.SQLiteServices()
	postgresServices := c.PostgresServices()
	if len(services)+len(postgresServices) == 1 && len(services) == 1 {
		if envName := strings.TrimSpace(services[0].DatabaseURLEnv); envName != "" {
			return envName
		}
	}
	if len(services)+len(postgresServices) == 1 && len(postgresServices) == 1 {
		if envName := strings.TrimSpace(postgresServices[0].DatabaseURLEnv); envName != "" {
			return envName
		}
	}
	return "DatabaseURL"
}

func (c Config) SQLiteServices() []SQLiteServiceConfig {
	out := make([]SQLiteServiceConfig, 0, len(c.Dev.Services))
	for name, svc := range c.Dev.Services {
		if strings.TrimSpace(svc.Kind) != "sqlite" {
			continue
		}
		fileLabel := strings.TrimSpace(svc.Database)
		if fileLabel == "" {
			fileLabel = name
		}
		envName := strings.TrimSpace(svc.DatabaseURLEnv)
		if envName == "" {
			envName = upperSnake(name) + "_DATABASE_URL"
		}
		out = append(out, SQLiteServiceConfig{
			Name:            name,
			FileLabel:       storageSlug(fileLabel),
			DatabaseURLEnv:  envName,
			DatabasePathEnv: upperSnake(name) + "_DATABASE_PATH",
			Raw:             svc,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (c Config) SQLiteService(name string) (SQLiteServiceConfig, bool) {
	for _, svc := range c.SQLiteServices() {
		if svc.Name == name {
			return svc, true
		}
	}
	return SQLiteServiceConfig{}, false
}

type SQLiteServiceConfig struct {
	Name            string
	FileLabel       string
	DatabaseURLEnv  string
	DatabasePathEnv string
	Raw             DevServiceConfig
}

func (c Config) PostgresServices() []PostgresServiceConfig {
	out := make([]PostgresServiceConfig, 0, len(c.Dev.Services))
	for name, svc := range c.Dev.Services {
		if devServiceKind(name, svc) != "postgres" {
			continue
		}
		label := strings.TrimSpace(svc.Database)
		if label == "" {
			label = name
		}
		envName := strings.TrimSpace(svc.DatabaseURLEnv)
		if envName == "" {
			envName = upperSnake(name) + "_DATABASE_URL"
		}
		out = append(out, PostgresServiceConfig{
			Name:           name,
			DatabaseLabel:  label,
			DatabaseURLEnv: envName,
			Raw:            svc,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
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
	Name           string
	DatabaseLabel  string
	DatabaseURLEnv string
	Raw            DevServiceConfig
}

func upperSnake(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
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
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "SQLITE"
	}
	return out
}

type BuildConfig struct {
	GoFlags []string `json:"go_flags"`
}

type WatchConfig struct {
	Ignore []string `json:"ignore"`
}

type ProxyConfig struct {
	Workspace       string                    `json:"workspace"`
	RouteBaseDomain string                    `json:"route_base_domain"`
	APIHost         string                    `json:"api_host"`
	ConsoleHost     string                    `json:"console_host"`
	Frontends       map[string]FrontendConfig `json:"frontends"`
}

type FrontendConfig struct {
	Host                string `json:"host"`
	Root                string `json:"root"`
	Upstream            string `json:"upstream"`
	AllowSharedUpstream bool   `json:"allow_shared_upstream"`
}

type DevConfig struct {
	Services map[string]DevServiceConfig `json:"services"`
	Setup    []string                    `json:"setup"`
	Routing  DevRoutingConfig            `json:"routing"`
}

type DevRoutingConfig struct {
	Mode      string `json:"mode"`
	Port      int    `json:"port"`
	PortStart int    `json:"port_start"`
	PortEnd   int    `json:"port_end"`
}

type DevServiceConfig struct {
	Kind               string            `json:"kind"`
	Mode               string            `json:"mode"`
	Version            string            `json:"version"`
	Isolation          string            `json:"isolation"`
	Project            string            `json:"project"`
	ParentBranch       string            `json:"parent_branch"`
	ParentDatabase     string            `json:"parent_database"`
	BranchPolicy       string            `json:"branch_policy"`
	BranchNameTemplate string            `json:"branch_name_template"`
	TTL                string            `json:"ttl"`
	Role               string            `json:"role"`
	DatabaseURLEnv     string            `json:"database_url_env"`
	Image              string            `json:"image"`
	Database           string            `json:"database"`
	Route              string            `json:"route"`
	Env                map[string]string `json:"env"`
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
	Clients []ClientGeneratorConfig `json:"clients"`
	SQLC    SQLCGeneratorConfig     `json:"sqlc"`
}

type ClientGeneratorConfig struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Lang   string `json:"lang"`
	Output string `json:"output"`
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
	Enabled *bool `json:"enabled"`
}

func (c DatabaseSeedConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type TaskConfig struct {
	CWD   string            `json:"cwd"`
	Run   string            `json:"run"`
	Steps []string          `json:"steps"`
	Env   map[string]string `json:"env"`
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
	DatabaseURLEnv        string           `json:"database_url_env"`
	JWTSecretEnv          string           `json:"jwt_secret_env"`
	RefreshCookieName     string           `json:"refresh_cookie_name"`
	AuthCookieDomainEnv   string           `json:"auth_cookie_domain_env"`
	PublicAppURLEnv       string           `json:"public_app_url_env"`
	APIBaseURLEnv         string           `json:"api_base_url_env"`
	EmailFromEnv          string           `json:"email_from_env"`
	AutoBootstrapDatabase bool             `json:"auto_bootstrap_database"`
	GoogleOAuth           AuthGoogleConfig `json:"google_oauth"`
	DevBootstrap          AuthDevBootstrap `json:"dev_bootstrap"`
}

type AuthGoogleConfig struct {
	Enabled         bool   `json:"enabled"`
	ClientIDEnv     string `json:"client_id_env"`
	ClientSecretEnv string `json:"client_secret_env"`
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

	path = filepath.Join(dir, AliasConfigFilename)
	data, err = os.ReadFile(path)
	if err == nil {
		if looksLikeSceneryConfig(data) {
			return path, data, nil
		}
		return "", nil, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	return "", nil, nil
}

func looksLikeSceneryConfig(data []byte) bool {
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&obj); err != nil {
		return false
	}
	if len(obj) == 0 {
		return false
	}
	fields := jsonStructFields(reflect.TypeFor[Config]())
	for name := range obj {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func (c Config) Validate() error {
	if err := c.validateWatch(); err != nil {
		return err
	}
	if err := c.validateDevServices(); err != nil {
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
	removedSyncKind := "elec" + "tric"
	for name, svc := range c.Dev.Services {
		kind := devServiceKind(name, svc)
		if kind == "" {
			switch name {
			case removedSyncKind:
				return errors.New("the removed legacy sync service declaration must be deleted")
			}
		}
		if kind == removedSyncKind {
			return fmt.Errorf("dev.services.%s uses a removed legacy sync service kind; delete this service declaration", name)
		}
		switch kind {
		case "", "sqlite", "postgres":
		default:
			return fmt.Errorf("dev.services.%s kind %q is not supported", name, kind)
		}
		switch kind {
		case "sqlite", "postgres":
			if !isStorageIdentifier(name) {
				return fmt.Errorf("dev.services.%s name is invalid; use lowercase letters, numbers, dots, underscores, or dashes", name)
			}
			if label := strings.TrimSpace(svc.Database); label != "" && !isStorageIdentifier(storageSlug(label)) {
				return fmt.Errorf("dev.services.%s.database %q is invalid", name, label)
			}
		}
		if kind == "postgres" {
			for _, field := range postgresLegacyDevServiceFields(svc) {
				return fmt.Errorf("dev.services.%s.%s is not supported for postgres services; plan 0093 supports only kind, database_url_env, database, and env", name, field)
			}
		}
	}
	return nil
}

func devServiceKind(name string, svc DevServiceConfig) string {
	kind := strings.TrimSpace(svc.Kind)
	if kind == "" && name == "postgres" {
		return "postgres"
	}
	return kind
}

func postgresLegacyDevServiceFields(svc DevServiceConfig) []string {
	var fields []string
	if strings.TrimSpace(svc.Mode) != "" {
		fields = append(fields, "mode")
	}
	if strings.TrimSpace(svc.Version) != "" {
		fields = append(fields, "version")
	}
	if strings.TrimSpace(svc.Isolation) != "" {
		fields = append(fields, "isolation")
	}
	if strings.TrimSpace(svc.Project) != "" {
		fields = append(fields, "project")
	}
	if strings.TrimSpace(svc.ParentBranch) != "" {
		fields = append(fields, "parent_branch")
	}
	if strings.TrimSpace(svc.ParentDatabase) != "" {
		fields = append(fields, "parent_database")
	}
	if strings.TrimSpace(svc.BranchPolicy) != "" {
		fields = append(fields, "branch_policy")
	}
	if strings.TrimSpace(svc.BranchNameTemplate) != "" {
		fields = append(fields, "branch_name_template")
	}
	if strings.TrimSpace(svc.TTL) != "" {
		fields = append(fields, "ttl")
	}
	if strings.TrimSpace(svc.Role) != "" {
		fields = append(fields, "role")
	}
	if strings.TrimSpace(svc.Image) != "" {
		fields = append(fields, "image")
	}
	if strings.TrimSpace(svc.Route) != "" {
		fields = append(fields, "route")
	}
	return fields
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
			return fmt.Errorf("storage.stores.%s.kind %q is not supported; use %q (ZeroFS was removed in plan 0091)", name, kind, "local")
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
	removedProxyHostPath := "proxy." + removedProxyHostField()
	if jsonPath == removedProxyHostPath {
		return fmt.Errorf("unknown %s field %q; %s was removed and has no compatibility behavior; remove it and use dev session routes or proxy.api_host/proxy.console_host/proxy.frontends for local routing", configName, jsonPath, removedProxyHostPath)
	}
	return fmt.Errorf("unknown %s field %q", configName, jsonPath)
}

func removedProxyHostField() string {
	return "m" + "cp_host"
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
