package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Extend only the disposable copy with generated internal and durable bindings.
func prepareObservabilityOperations(root string) error {
	configPath := filepath.Join(root, ".scenery.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	config["storage"] = map[string]any{"default": "probe", "stores": map[string]any{"probe": map[string]any{"kind": "local", "access": "auth", "tenant_scoped": false, "max_object_bytes": 1048576}}}
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	appPath := filepath.Join(root, "app.scn")
	app, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	app = bytes.Replace(app, []byte("database = data_source.library"), []byte("database = data_source.library\n    tasks = execution_engine.probe"), 1)
	app = bytes.Replace(app, []byte(`typescript_client "public_api" {`), []byte(`typescript_client "public_api" {
 include = [module.library.create]
 retry {
  policy = "scenery.retry.idempotent"
  maximum_attempts = 2
  maximum_delay_milliseconds = 10
  statuses = [503]
 }
`), 1)
	app = append(app, []byte(`
provider "durable" { source = "registry.scenery.dev/core/durable" }
execution_engine "probe" {
 provider = provider.durable
 lifecycle = "managed"
 require_capabilities = ["execution.durable/v1"]
}
`)...)
	if err := os.WriteFile(appPath, app, 0600); err != nil {
		return err
	}
	packagePath := filepath.Join(root, "library/package.scn")
	source, err := os.ReadFile(packagePath)
	if err != nil {
		return err
	}
	source = bytes.Replace(source, []byte(`service "library" {`), []byte(`service "library" {
 client "probe" { binding = binding.probe_internal }
 client "background" { binding = binding.probe_background }
`), 1)
	source = bytes.Replace(source, []byte(`operation "create" {`), []byte(`operation "create" {
 idempotency {
  mode = "keyed"
  key = [input.book_id]
 }
`), 1)
	source = append(source, []byte(observabilityOperationsSCN)...)
	if err := os.WriteFile(packagePath, source, 0600); err != nil {
		return err
	}
	servicePath := filepath.Join(root, "library/service.go")
	service, err := os.ReadFile(servicePath)
	if err != nil {
		return err
	}
	service = bytes.Replace(service, []byte("type Service struct{ database datasource.SQL }"), []byte("type Service struct{ database datasource.SQL; input contract.LibraryConstructorInput }"), 1)
	service = bytes.Replace(service, []byte("database: input.Dependencies.Database}"), []byte("database: input.Dependencies.Database, input: input}"), 1)
	return os.WriteFile(servicePath, service, 0600)
}

const observabilityOperationsSCN = `
input "tasks" {
 type = resource_ref("execution_engine")
 phase = "deployment"
 requires = ["execution.durable/v1"]
}
operation "trace_probe" {
 service = service.library
 input = std.type.unit
 handler { method = "TraceProbe" }
 result "done" { type = std.type.unit }
}
execution "probe_direct" {
 operation = operation.trace_probe
 mode = "direct"
 timeout = "5s"
}
execution "probe_durable" {
 operation = operation.trace_probe
 mode = "durable"
 engine = var.tasks
 external_name = "observability.probe"
 revision = 1
 timeout = "10s"
 lease = "5s"
 attempts = 1
 retry {
  strategy = "exponential"
  initial = "100ms"
  factor = 2
  maximum = "1s"
 }
 retention {
  success = "1h"
  failure = "1h"
 }
}
binding "probe_internal" {
 operation = operation.trace_probe
 execution = execution.probe_direct
 protocol = "internal"
 delivery = "call"
 exposure = "application"
 authentication = std.authentication.inherit
 authorization = std.authorization.public
 pipeline = std.pipeline.empty
 internal {
  visibility = "application"
  principal = "inherit"
 }
}
binding "probe_background" {
 operation = operation.trace_probe
 execution = execution.probe_durable
 protocol = "internal"
 delivery = "wait"
 exposure = "application"
 authentication = std.authentication.inherit
 authorization = std.authorization.public
 pipeline = std.pipeline.empty
 internal {
  visibility = "application"
  principal = "inherit"
 }
}
`
