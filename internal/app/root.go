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
	"strings"
)

var ErrRootNotFound = errors.New("no .scenery.json found in current directory or any parent")

type Config struct {
	Name          string                `json:"name"`
	ID            string                `json:"id"`
	Build         BuildConfig           `json:"build"`
	Proxy         ProxyConfig           `json:"proxy"`
	Dev           DevConfig             `json:"dev"`
	Generators    GeneratorsConfig      `json:"generators"`
	Database      DatabaseConfig        `json:"database"`
	Tasks         map[string]TaskConfig `json:"tasks"`
	Validation    ValidationConfig      `json:"validation"`
	Auth          AuthConfig            `json:"auth"`
	Observability ObservabilityConfig   `json:"observability"`
	Temporal      TemporalConfig        `json:"temporal"`
}

func (c Config) AppID() string {
	if c.ID != "" {
		return c.ID
	}
	return c.Name
}

func (c Config) DatabaseURLEnv() string {
	if envName := strings.TrimSpace(c.ManagedPostgresService().DatabaseURLEnv); envName != "" {
		return envName
	}
	return "DatabaseURL"
}

func (c Config) ManagedPostgresService() DevServiceConfig {
	for name, svc := range c.Dev.Services {
		kind := strings.TrimSpace(svc.Kind)
		if kind == "" && name == "postgres" {
			kind = "postgres"
		}
		if kind == "postgres" {
			return svc
		}
	}
	return DevServiceConfig{}
}

type BuildConfig struct {
	GoFlags []string `json:"go_flags"`
}

type ProxyConfig struct {
	Workspace       string                    `json:"workspace"`
	RouteBaseDomain string                    `json:"route_base_domain"`
	APIHost         string                    `json:"api_host"`
	ConsoleHost     string                    `json:"console_host"`
	TemporalHost    string                    `json:"temporal_host"`
	GrafanaHost     string                    `json:"grafana_host"`
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
	BranchStrategy     string            `json:"branch_strategy"`
	TTL                string            `json:"ttl"`
	Role               string            `json:"role"`
	DatabaseURLEnv     string            `json:"database_url_env"`
	Image              string            `json:"image"`
	Database           string            `json:"database"`
	Route              string            `json:"route"`
	Env                map[string]string `json:"env"`
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
}

type DatabaseApplyConfig struct {
	Provider string            `json:"provider"`
	Command  string            `json:"command"`
	CWD      string            `json:"cwd"`
	Env      map[string]string `json:"env"`
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

type TemporalConfig struct {
	Enabled         bool                `json:"enabled"`
	Mode            string              `json:"mode"`
	Namespace       string              `json:"namespace"`
	AddressEnv      string              `json:"address_env"`
	TaskQueuePrefix string              `json:"task_queue_prefix"`
	PayloadCodec    string              `json:"payload_codec"`
	APIKeyEnv       string              `json:"api_key_env"`
	TLS             TemporalTLSConfig   `json:"tls"`
	Local           TemporalLocalConfig `json:"local"`
	TypeScript      TemporalTypeScript  `json:"typescript"`
}

type TemporalTLSConfig struct {
	Enabled           bool   `json:"enabled"`
	ServerNameEnv     string `json:"server_name_env"`
	CACertFileEnv     string `json:"ca_cert_file_env"`
	ClientCertFileEnv string `json:"client_cert_file_env"`
	ClientKeyFileEnv  string `json:"client_key_file_env"`
}

type TemporalLocalConfig struct {
	AutoStart  bool   `json:"auto_start"`
	DBFilename string `json:"db_filename"`
}

type TemporalTypeScript struct {
	Enabled   bool   `json:"enabled"`
	Runtime   string `json:"runtime"`
	AutoStart bool   `json:"auto_start"`
}

func DiscoverRoot(start string) (string, Config, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, err
	}
	for {
		path := filepath.Join(dir, ".scenery.json")
		if data, err := os.ReadFile(path); err == nil {
			var cfg Config
			if err := decodeConfig(path, data, &cfg); err != nil {
				return "", Config{}, err
			}
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}
			if cfg.Name == "" {
				return "", Config{}, errors.New(".scenery.json must define a non-empty name or id")
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

func decodeConfig(path string, data []byte, cfg *Config) error {
	if err := rejectUnknownConfigFields(path, data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("%s: decode .scenery.json: %w", path, err)
	}
	return nil
}

func rejectUnknownConfigFields(path string, data []byte) error {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%s: decode .scenery.json: %w", path, err)
	}
	if err := rejectUnknownFieldsValue(raw, reflect.TypeFor[Config](), nil); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func rejectUnknownFieldsValue(value any, typ reflect.Type, path []string) error {
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
				return unknownConfigFieldError(childPath)
			}
			if err := rejectUnknownFieldsValue(child, field.Type, childPath); err != nil {
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
			if err := rejectUnknownFieldsValue(child, elem, appendJSONPath(path, name)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for i, child := range items {
			if err := rejectUnknownFieldsValue(child, typ.Elem(), appendJSONIndex(path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func unknownConfigFieldError(path []string) error {
	jsonPath := strings.Join(path, ".")
	removedProxyHostPath := "proxy." + removedProxyHostField()
	if jsonPath == removedProxyHostPath {
		return fmt.Errorf("unknown .scenery.json field %q; %s was removed and has no compatibility behavior; remove it and use dev session routes or proxy.api_host/proxy.console_host/proxy.frontends for local routing", jsonPath, removedProxyHostPath)
	}
	return fmt.Errorf("unknown .scenery.json field %q", jsonPath)
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
