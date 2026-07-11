package vnext

import "sort"

type resourceSchema struct{ Required, Allowed []string }

var resourceSchemas = map[string]resourceSchema{
	"scenery.go-module/v1":         {[]string{"root", "import_path"}, []string{"root", "import_path"}},
	"scenery.go-toolchain/v1":      {[]string{"version"}, []string{"version", "experiments"}},
	"scenery.go-target/v1":         {[]string{"role", "toolchain", "module", "packages"}, []string{"role", "platform", "toolchain", "module", "packages", "cgo", "extends", "inherits", "build_tags", "go_flags", "environment", "goos", "goarch", "architecture_features", "native_input", "native_inputs", "verify_by_default"}},
	"scenery.http-gateway/v1":      {[]string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded"}, []string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded", "request_limit", "response_limit", "timeouts"}},
	"scenery.authentication/v1":    {[]string{"provider", "scheme"}, []string{"provider", "scheme", "config"}},
	"scenery.authorization/v1":     {[]string{"principal"}, []string{"principal", "strategy", "rule"}},
	"scenery.workload-identity/v1": {[]string{"issuer", "principal_type", "claims"}, []string{"issuer", "principal_type", "claims"}},
	"scenery.pipeline/v1":          {nil, []string{"step"}},
	"scenery.provider/v1":          {[]string{"source", "version"}, []string{"source", "version", "locked_version", "locked_integrity", "compile_descriptor_digest", "runtime_abi", "deployment_abi", "migration_abi", "config_schema", "capabilities", "instance_kinds"}},
	"scenery.data-source/v1":       {[]string{"provider", "lifecycle"}, []string{"provider", "lifecycle", "require_capabilities", "effective_capabilities", "provider_descriptor_digest", "config"}},
	"scenery.execution-engine/v1":  {[]string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "effective_capabilities", "provider_descriptor_digest", "config"}},
	"scenery.event-bus/v1":         {[]string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "effective_capabilities", "provider_descriptor_digest", "config"}},
	"scenery.secret-store/v1":      {[]string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "effective_capabilities", "provider_descriptor_digest", "config"}},
	"scenery.secret/v1":            {[]string{"store", "key"}, []string{"store", "key"}},
	"scenery.deployment/v1":        {[]string{"environment"}, []string{"environment", "module", "data_source", "service", "http_gateway", "provider", "secret", "fixture_policy"}},
	"scenery.typescript-client/v1": {[]string{"gateways", "package", "module", "runtime", "output_root"}, []string{"gateways", "package", "module", "runtime", "output_root", "typescript_version", "javascript_target", "include", "retry", "version_policy"}},
	"scenery.patch/v1":             {[]string{"target", "module_version", "schema", "expect", "set"}, []string{"target", "module_version", "schema", "expect", "set"}},
	"scenery.module/v1":            {[]string{"source"}, []string{"source", "version", "inputs", "package", "interface_inputs", "exports", "export_metadata", "workspace_package_root", "locked_version", "locked_integrity", "compile_descriptor_digest", "package_contract_abi_revision"}},
	"scenery.service/v1":           {[]string{"runtime", "implementation"}, []string{"runtime", "implementation", "dependency", "config", "config_schema", "client", "lifecycle"}},
	"scenery.record/v1":            {nil, []string{"field", "validation", "unknown_fields"}},
	"scenery.enum/v1":              {[]string{"value"}, []string{"value", "open"}},
	"scenery.union/v1":             {[]string{"variant", "discriminator"}, []string{"variant", "open", "discriminator", "unknown_variant"}},
	"scenery.operation/v1":         {[]string{"service", "input", "handler"}, []string{"service", "input", "handler", "result", "error", "idempotency"}},
	"scenery.execution/v1":         {[]string{"operation", "mode"}, []string{"operation", "mode", "engine", "revision", "timeout", "lease", "attempts", "retry", "concurrency", "retention", "deduplication", "external_name"}},
	"scenery.binding/v1":           {[]string{"operation", "protocol", "delivery"}, []string{"gateway", "operation", "execution", "protocol", "delivery", "exposure", "authentication", "authorization", "pipeline", "http", "internal", "cli", "event"}},
	"scenery.schedule/v1":          {[]string{"trigger", "invoke", "overlap"}, []string{"trigger", "invoke", "overlap", "catchup"}},
	"scenery.event/v1":             {[]string{"payload", "version"}, []string{"payload", "version"}},
	"scenery.event-emission/v1":    {[]string{"bus", "channel", "contract", "guarantee", "from"}, []string{"bus", "channel", "contract", "guarantee", "ordering_key", "deduplication_key", "broker_retry", "dead_letter_channel", "from"}},
	"scenery.entity/v1":            {[]string{"type", "data_source", "mapping", "field"}, []string{"type", "data_source", "mapping", "field", "index", "unique", "foreign_key", "deletion"}},
	"scenery.view/v1":              {[]string{"data_source", "input", "result", "implementation"}, []string{"data_source", "input", "result", "implementation", "implementation_digest"}},
	"scenery.crud/v1":              {[]string{"entity", "implementation", "actions", "execution"}, []string{"entity", "implementation", "actions", "execution", "http", "internal", "extension"}},
	"scenery.fixture/v1":           {[]string{"entity", "environments", "mode", "values"}, []string{"entity", "environments", "mode", "values"}},
	"scenery.page/v1":              {[]string{"path", "load"}, []string{"path", "load", "action"}},
	"scenery.renderer/v1":          {[]string{"page", "runtime", "module"}, []string{"page", "runtime", "module", "config", "implementation_digest"}},
	"scenery.middleware/v1":        {[]string{"protocols", "phases"}, []string{"protocols", "phases", "before", "after", "exclusive", "effects"}},
}

func CoreSchema(kind string) (map[string]any, bool) {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return nil, false
	}
	required := append([]string(nil), schema.Required...)
	allowed := append([]string(nil), schema.Allowed...)
	sort.Strings(required)
	sort.Strings(allowed)
	return map[string]any{"schema_revision": kind, "kind": kind, "required": required, "allowed": allowed, "additional_properties": false}, true
}

func validateResourceSchemas(resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		schema, ok := resourceSchemas[resource.Kind]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1008", Severity: "error", Message: "unknown resource schema " + resource.Kind, Address: resource.Address})
			continue
		}
		allowed := map[string]bool{}
		for _, name := range schema.Allowed {
			allowed[name] = true
		}
		for name := range resource.Spec {
			if !allowed[name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1007", Severity: "error", Message: "unknown field " + name + " for " + resource.Kind, Address: resource.Address})
			}
		}
		if resource.Origin.Kind == "legacy_v0" {
			continue
		}
		for _, name := range schema.Required {
			if resource.Spec[name] == nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1009", Severity: "error", Message: "missing required field " + name, Address: resource.Address})
			}
		}
	}
	return diagnostics
}
