package postgresdb

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"scenery.sh/internal/postgresname"
)

const RegistryEnv = "SCENERY_DATABASE_JSON"

type Source string

const (
	SourceExternal Source = "external"
	SourceManaged  Source = "managed"
)

type Service struct {
	Name   string `json:"service"`
	Schema string `json:"schema"`
	URL    string `json:"url"`
	// Compatibility fields for later-wave CLI code that still reasons in service databases.
	Database       string `json:"-"`
	DatabaseURLEnv string `json:"-"`
	Source         Source `json:"-"`
}

type Database struct {
	Database string    `json:"database"`
	URL      string    `json:"url"`
	Source   Source    `json:"source"`
	Schemas  []Service `json:"schemas"`
}

func ServiceURL(baseURL, schema string) (string, error) {
	u, err := ParseURL(baseURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(schema) == "" {
		return "", fmt.Errorf("postgres service schema is required")
	}
	copied := *u
	values := copied.Query()
	searchPath := strings.TrimSpace(schema)
	if searchPath != "scenery" {
		searchPath += ",scenery"
	}
	values.Set("search_path", searchPath)
	copied.RawQuery = values.Encode()
	return copied.String(), nil
}

func Env(database Database, databaseURLEnv string) []string {
	values := map[string]string{}
	databaseURLEnv = strings.TrimSpace(databaseURLEnv)
	if databaseURLEnv == "" {
		databaseURLEnv = "DATABASE_URL"
	}
	values[databaseURLEnv] = database.URL
	for _, svc := range database.Schemas {
		values[postgresname.ServiceDatabaseURLEnv(svc.Name)] = svc.URL
	}
	if data, err := json.Marshal(database); err == nil {
		values[RegistryEnv] = string(data)
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func DecodeRegistry(raw string) (Database, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Database{}, nil
	}
	var database Database
	if err := json.Unmarshal([]byte(raw), &database); err != nil {
		return Database{}, err
	}
	for _, svc := range database.Schemas {
		if strings.TrimSpace(svc.Name) == "" || strings.TrimSpace(svc.URL) == "" {
			return Database{}, fmt.Errorf("postgres registry contains an incomplete schema")
		}
	}
	return database, nil
}

func DatabaseNameFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(u.Path), "/")
}
