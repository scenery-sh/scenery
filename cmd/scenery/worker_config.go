package main

import (
	"context"
	"encoding/json"
	"slices"

	"scenery.sh/auth"
	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/compiler"
	workerruntime "scenery.sh/runtime"
)

func (bundle *workerBundle) projectConfig(result *compiler.Result) error {
	_, cfg, err := app.DiscoverRoot(result.Root)
	if err != nil {
		return err
	}
	catalog, err := appconfig.BuildCatalog(result.Manifest, configFrameworkOptions(cfg))
	if err != nil {
		return err
	}
	bundle.Host.Observability = workerruntime.ObservabilityConfig{
		Logs:    workerruntime.EndpointFilterConfig(cfg.Observability.Logs),
		Tracing: workerruntime.EndpointFilterConfig(cfg.Observability.Tracing),
	}
	if cfg.Auth.Enabled {
		bundle.Auth = &workerAuthPlan{StandardConfig: auth.StandardConfig{
			Enabled: true, AutoBootstrapDatabase: cfg.Auth.AutoBootstrapDatabase,
			GoogleOAuth: auth.GoogleOAuthConfig{Enabled: cfg.Auth.GoogleOAuth.Enabled}, Connections: cfg.Auth.Connections,
			DevBootstrap: auth.DevBootstrapConfig{
				Enabled: cfg.Auth.DevBootstrap.Enabled, DefaultUserEmail: cfg.Auth.DevBootstrap.DefaultUserEmail,
				DefaultTenantID: cfg.Auth.DevBootstrap.DefaultTenantID,
			},
		}}
		if cfg.Auth.AutoBootstrapDatabase {
			bundle.Auth.BootstrapSQL, err = auth.StandardSchemaSQL()
			if err != nil {
				return err
			}
		}
	}
	for _, input := range catalog.Inputs {
		if input.Public || consumes(input, hostConsumer) {
			bundle.HostConfigKeys = append(bundle.HostConfigKeys, input.Key)
		}
		for index, program := range bundle.Programs {
			selected := false
			if program.Runtime == "go" {
				selected = consumes(input, program.Service)
			} else {
				for _, consumer := range input.Consumers {
					selected = selected || consumer.Service == program.Service
				}
			}
			if selected {
				bundle.Programs[index].ConfigKeys = append(program.ConfigKeys, input.Key)
			}
		}
	}
	return nil
}

func (bundle *workerBundle) resolveDevelopmentConfig(ctx context.Context, compiled *compiler.Result, cfg app.Config, envName string) error {
	env, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	store, err := devConfigStore(cfg)
	if err != nil {
		return err
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return err
	}
	resolution, err := resolveDevConfig(ctx, compiled.Manifest, cfg, env, store, document, []string{applicationConsumer}, func() (appconfig.SecretBackend, error) {
		return defaultConfigSecretBackend(store)
	})
	if err != nil {
		return err
	}
	bundle.config, err = workerruntime.DecodeConfigSnapshot(resolution.snapshotFor(applicationConsumer).data)
	return err
}

func (bundle *workerBundle) setWorkerConfig() {
	for index, program := range bundle.Programs {
		snapshot := projectWorkerConfig(bundle.config, program.Service, program.ConfigKeys)
		bundle.Programs[index].config = snapshot
	}
}

func projectWorkerConfig(base *workerruntime.ConfigSnapshot, consumer string, keys []string) *workerruntime.ConfigSnapshot {
	if base == nil {
		return nil
	}
	projected := workerruntime.ConfigSnapshot{
		Kind: base.Kind, AppID: base.AppID, Environment: base.Environment, Revision: base.Revision,
		CatalogRevision: base.CatalogRevision, Consumer: consumer,
		Values: make(map[string]json.RawMessage), Secrets: make(map[string][]byte), Public: []string{},
	}
	for _, key := range keys {
		if value, ok := base.Values[key]; ok {
			projected.Values[key] = value
		}
		if secret, ok := base.Secrets[key]; ok {
			projected.Secrets[key] = secret
		}
		if slices.Contains(base.Public, key) {
			projected.Public = append(projected.Public, key)
		}
	}
	return &projected
}
