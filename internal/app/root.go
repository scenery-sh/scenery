package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
)

type Config struct {
	Name          string                `json:"name"`
	ID            string                `json:"id"`
	Proxy         ProxyConfig           `json:"proxy"`
	Dev           DevConfig             `json:"dev"`
	Generators    GeneratorsConfig      `json:"generators"`
	Database      DatabaseConfig        `json:"database"`
	Tasks         map[string]TaskConfig `json:"tasks"`
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

type ProxyConfig struct {
	Workspace    string                    `json:"workspace"`
	APIHost      string                    `json:"api_host"`
	ConsoleHost  string                    `json:"console_host"`
	MCPHost      string                    `json:"mcp_host"`
	TemporalHost string                    `json:"temporal_host"`
	GrafanaHost  string                    `json:"grafana_host"`
	Frontends    map[string]FrontendConfig `json:"frontends"`
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
	Kind      string            `json:"kind"`
	Version   string            `json:"version"`
	Isolation string            `json:"isolation"`
	Image     string            `json:"image"`
	Database  string            `json:"database"`
	Route     string            `json:"route"`
	Env       map[string]string `json:"env"`
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
		path := filepath.Join(dir, ".onlava.json")
		if data, err := os.ReadFile(path); err == nil {
			var cfg Config
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&cfg); err != nil {
				return "", Config{}, err
			}
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}
			if cfg.Name == "" {
				return "", Config{}, errors.New(".onlava.json must define a non-empty name or id")
			}
			return dir, cfg, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", Config{}, errors.New("no .onlava.json found in current directory or any parent")
}

func RepoRoot() string {
	_, file, _, _ := goruntime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
