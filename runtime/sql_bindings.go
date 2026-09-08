package runtime

import (
	"fmt"
	"strings"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

// SQLBinding is generated from the compiled requirement projection. It carries
// no endpoint, credential, allocation authority or mutable application graph.
type SQLBinding struct {
	Name, Schema string
	DurableOnly  bool
}

// ConfigureSQLBindings runs before generated constructors. It supplies the
// existing SQL environment from explicit endpoints without opening a database.
func ConfigureSQLBindings(bindings []SQLBinding) error {
	if err := LoadDotEnvIntoEnv(); err != nil {
		return err
	}
	database, err := resolveRuntimeSQLBindings(bindings, envpolicy.Get)
	if err != nil {
		return err
	}
	if len(database.Schemas) == 0 {
		return nil
	}
	for _, entry := range postgresdb.Env(database) {
		key, value, _ := strings.Cut(entry, "=")
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

func resolveRuntimeSQLBindings(bindings []SQLBinding, lookup func(string) string) (postgresdb.Database, error) {
	supplied, err := postgresdb.DecodeRegistry(lookup(postgresdb.RegistryEnv))
	if err != nil {
		return postgresdb.Database{}, fmt.Errorf("runtime: invalid %s SQL supply", postgresdb.RegistryEnv)
	}
	baseURL := strings.TrimSpace(lookup("DATABASE_URL"))
	if baseURL == "" {
		baseURL = supplied.URL
	}
	database := postgresdb.Database{Database: postgresdb.DatabaseNameFromURL(baseURL), URL: baseURL, Source: postgresdb.SourceExternal}
	if supplied.URL == baseURL && supplied.Source == postgresdb.SourceManaged {
		database.Source = postgresdb.SourceManaged
	}
	remoteDurable := strings.TrimSpace(lookup(envDurableEndpoint)) != ""
	seen := map[string]bool{}
	for _, binding := range bindings {
		if remoteDurable && binding.DurableOnly {
			continue
		}
		if binding.Name == "" || binding.Schema == "" || seen[binding.Name] {
			return postgresdb.Database{}, fmt.Errorf("runtime: SQL bindings require distinct non-empty names and schemas")
		}
		seen[binding.Name] = true
		key := postgresname.ServiceDatabaseURLEnv(binding.Name)
		url := ""
		if binding.Name == "scenery" {
			key = "DATABASE_URL"
		} else {
			url = strings.TrimSpace(lookup(key))
		}
		if url == "" && baseURL != "" {
			url, err = postgresdb.ServiceURL(baseURL, binding.Schema)
			if err != nil {
				return postgresdb.Database{}, fmt.Errorf("runtime: SQL binding %s requires a valid PostgreSQL DATABASE_URL", binding.Name)
			}
		}
		if url == "" {
			for _, service := range supplied.Schemas {
				if service.Name == binding.Name && service.Schema == binding.Schema {
					url = service.URL
				}
			}
		}
		if _, err := postgresdb.ParseURL(url); err != nil {
			supply := key
			if key != "DATABASE_URL" {
				supply += " or DATABASE_URL"
			}
			return postgresdb.Database{}, fmt.Errorf("runtime: SQL binding %s requires a valid PostgreSQL endpoint in %s", binding.Name, supply)
		}
		if database.Source == postgresdb.SourceManaged {
			matchesSupplied := false
			for _, service := range supplied.Schemas {
				if service.Name == binding.Name && service.Schema == binding.Schema && service.URL == url {
					matchesSupplied = true
				}
			}
			if !matchesSupplied {
				database.Source = postgresdb.SourceExternal
			}
		}
		database.Schemas = append(database.Schemas, postgresdb.Service{Name: binding.Name, Schema: binding.Schema, URL: url})
	}
	return database, nil
}
