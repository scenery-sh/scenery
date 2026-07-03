package postgresdb

import (
	"encoding/json"
	"fmt"
	"strings"
)

const RegistryEnv = "SCENERY_POSTGRES_DATABASES_JSON"

type Source string

const (
	SourceExternal Source = "external"
	SourceManaged  Source = "managed"
)

type Service struct {
	Name           string `json:"service"`
	Database       string `json:"database"`
	URL            string `json:"url"`
	DatabaseURLEnv string `json:"database_url_env"`
	Source         Source `json:"source"`
}

func Env(services []Service, includeDatabaseURLAlias bool) []string {
	values := map[string]string{}
	for _, svc := range services {
		values[svc.DatabaseURLEnv] = svc.URL
	}
	if includeDatabaseURLAlias && len(services) == 1 {
		if _, ok := values["DatabaseURL"]; !ok {
			values["DatabaseURL"] = services[0].URL
		}
	}
	if data, err := json.Marshal(services); err == nil {
		values[RegistryEnv] = string(data)
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func DecodeRegistry(raw string) ([]Service, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var services []Service
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		return nil, err
	}
	for _, svc := range services {
		if strings.TrimSpace(svc.Name) == "" || strings.TrimSpace(svc.URL) == "" {
			return nil, fmt.Errorf("postgres registry contains an incomplete service")
		}
	}
	return services, nil
}
