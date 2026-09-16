package codegen

import (
	"fmt"
	"go/format"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// entrypointRegistration names the generated package whose Register function
// populates the contract registry, and the Go expression for the resources that
// registry must cover.
type entrypointRegistration struct {
	Import            string
	RequiredAddresses string
}

func generateMain(appName string, cfg appcfg.Config, compositionImport string, sql compiler.SQLRequirements) ([]byte, error) {
	registration := entrypointRegistration{}
	if compositionImport != "" {
		registration = entrypointRegistration{Import: compositionImport, RequiredAddresses: "scenerycomposition.RequiredAddresses"}
	}
	return renderEntrypoint(appName, cfg, registration, sql)
}

// generateServiceMain renders the entrypoint of one service process: the same
// SQL, auth and runtime startup as the application entrypoint, registering only
// that service's adapter and requiring exactly the resources it covers.
func generateServiceMain(appName string, cfg appcfg.Config, service generateapi.ServiceProcessPlan, sql compiler.SQLRequirements) ([]byte, error) {
	return renderEntrypoint(appName, cfg, entrypointRegistration{Import: service.AdapterImport, RequiredAddresses: fmt.Sprintf("%#v", service.RequiredAddresses)}, sql)
}

// generateHostMain renders the process host entrypoint. It links no adapter, so
// implementation edits never rebuild it; the route and MCP tool tables and the
// contract revision are literal data, and the first service process serves
// framework and unmatched routes. A host that serves requests itself, because
// it has application-level registrations (assistants and MCP federation) or no
// service process serves the framework, renders the entrypoint's SQL and
// authentication wiring.
func generateHostMain(appName string, cfg appcfg.Config, plan generateapi.RuntimeIntegrationPlan, sql compiler.SQLRequirements) ([]byte, error) {
	if plan.ContractRevision == "" {
		return nil, fmt.Errorf("process host requires a contract revision")
	}
	application := len(plan.HostApplication) > 0
	servesItself := application || len(plan.Services) == 0
	var buf strings.Builder
	buf.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n")
	if servesItself && cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth \"scenery.sh/auth\"\n")
	}
	buf.WriteString("\tsceneryruntime \"scenery.sh/runtime\"\n)\n\n")
	fmt.Fprintf(&buf, "const contractRevision = %q\n\n", plan.ContractRevision)
	buf.WriteString("func main() {\n")
	buf.WriteString("\tif len(os.Args) == 2 && os.Args[1] == sceneryruntime.RuntimePreflightFlag {\n")
	buf.WriteString("\t\tproof := os.NewFile(3, \"scenery-runtime-preflight\")\n\t\tdefer proof.Close()\n")
	buf.WriteString("\t\tif err := sceneryruntime.WriteRuntimePreflight(proof, contractRevision); err != nil {\n\t\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\t\tos.Exit(1)\n\t\t}\n\t\treturn\n\t}\n")
	buf.WriteString("\tif err := sceneryruntime.VerifyLinkedContractBundle(contractRevision); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	if servesItself {
		renderSQLBindings(&buf, sql)
		renderAuthRegistration(&buf, cfg)
	}
	if application {
		buf.WriteString("\tcontractRegistry, err := sceneryruntime.NewContractRegistry(sceneryruntime.ContractRegistryOptions{ContractRevision: contractRevision, RequiredAddresses: applicationRequiredAddresses, ProviderABIs: sceneryruntime.ContractProviderABIs()})\n")
		buf.WriteString("\tif err == nil { err = registerApplication(contractRegistry) }\n")
		buf.WriteString("\tif err == nil { err = contractRegistry.Seal() }\n")
		buf.WriteString("\tif err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	}
	// The first service process serves framework and unmatched routes; an
	// application without a native service has none, and its host serves them.
	fallback := ""
	if len(plan.Services) > 0 {
		fallback = plan.Services[0].Name
	}
	observability := ""
	if literal := observabilityConfigLiteral(cfg.Observability); literal != "" {
		observability = " Observability: " + literal + ","
	}
	fmt.Fprintf(&buf, "\tif err := sceneryruntime.MainProcessHost(sceneryruntime.ProcessHostConfig{Name: %q, ListenAddr: sceneryruntime.ListenAddrFromEnv(), Fallback: %q,%s\n", appName, fallback, observability)
	buf.WriteString("\t\tRoutes: []sceneryruntime.ProcessHostRoute{\n")
	for _, service := range plan.Services {
		for _, route := range service.Routes {
			if len(route.Methods) == 0 || !strings.HasPrefix(route.Path, "/") {
				return nil, fmt.Errorf("service process %s has an invalid route %q", service.Name, route.Path)
			}
			fmt.Fprintf(&buf, "\t\t\t{Process: %q, Methods: %#v, Path: %q, PathTail: %t},\n", service.Name, route.Methods, route.Path, route.PathTail)
		}
	}
	buf.WriteString("\t\t},\n\t\tMCPTools: []sceneryruntime.ProcessHostMCPTool{\n")
	for _, service := range plan.Services {
		for _, tool := range service.MCPTools {
			fmt.Fprintf(&buf, "\t\t\t{Process: %q, AssistantAddress: %q, Name: %q},\n", service.Name, tool.AssistantAddress, tool.Name)
		}
	}
	buf.WriteString("\t\t},\n\t}); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n}\n")
	return format.Source([]byte(buf.String()))
}

func renderSQLBindings(buf *strings.Builder, sql compiler.SQLRequirements) {
	if len(sql) == 0 {
		return
	}
	buf.WriteString("\tif err := sceneryruntime.ConfigureSQLBindings([]sceneryruntime.SQLBinding{\n")
	for _, binding := range sql.Bindings(false) {
		durableOnly := true
		for _, requirement := range sql {
			if requirement.Name == binding.Name && requirement.Kind != compiler.SQLDurable {
				durableOnly = false
			}
		}
		fmt.Fprintf(buf, "\t\t{Name: %q, Schema: %q, DurableOnly: %t},\n", binding.Name, binding.Schema, durableOnly)
	}
	buf.WriteString("\t}); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
}

func renderAuthRegistration(buf *strings.Builder, cfg appcfg.Config) {
	if !cfg.Auth.Enabled {
		return
	}
	fmt.Fprintf(buf, "\tif err := sceneryauth.RegisterStandard(%s); err != nil {\n", authConfigLiteral(cfg.Auth))
	buf.WriteString("\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n")
	buf.WriteString("\t\tos.Exit(1)\n")
	buf.WriteString("\t}\n")
}

func renderEntrypoint(appName string, cfg appcfg.Config, registration entrypointRegistration, sql compiler.SQLRequirements) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString("\t\"os\"\n")
	if cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth \"scenery.sh/auth\"\n")
	}
	buf.WriteString("\tsceneryruntime \"scenery.sh/runtime\"\n")
	if registration.Import != "" {
		fmt.Fprintf(&buf, "\tscenerycomposition %q\n", registration.Import)
	}
	buf.WriteString(")\n\n")
	buf.WriteString("func main() {\n")
	if registration.Import != "" {
		buf.WriteString("\tif len(os.Args) == 2 && os.Args[1] == sceneryruntime.RuntimePreflightFlag {\n")
		buf.WriteString("\t\tproof := os.NewFile(3, \"scenery-runtime-preflight\")\n\t\tdefer proof.Close()\n")
		buf.WriteString("\t\tif err := sceneryruntime.WriteRuntimePreflight(proof, scenerycomposition.ContractRevision); err != nil {\n\t\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\t\tos.Exit(1)\n\t\t}\n\t\treturn\n\t}\n")
	}
	renderSQLBindings(&buf, sql)
	renderAuthRegistration(&buf, cfg)
	if registration.Import != "" {
		buf.WriteString("\tif err := sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
		fmt.Fprintf(&buf, "\tcontractRegistry, err := sceneryruntime.NewContractRegistry(sceneryruntime.ContractRegistryOptions{ContractRevision: scenerycomposition.ContractRevision, RequiredAddresses: %s, ProviderABIs: sceneryruntime.ContractProviderABIs()})\n", registration.RequiredAddresses)
		buf.WriteString("\tif err == nil { err = scenerycomposition.Register(contractRegistry) }\n")
		buf.WriteString("\tif err == nil { err = contractRegistry.Seal() }\n")
		buf.WriteString("\tif err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	}
	fmt.Fprintf(&buf, "\tif err := sceneryruntime.Main(%s); err != nil {\n", appConfigLiteral(appName, cfg))
	buf.WriteString("\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n")
	buf.WriteString("\t\tos.Exit(1)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")
	return format.Source([]byte(buf.String()))
}

func authConfigLiteral(cfg appcfg.AuthConfig) string {
	fields := []string{"Enabled: true"}
	if cfg.AutoBootstrapDatabase {
		fields = append(fields, "AutoBootstrapDatabase: true")
	}
	if literal := authGoogleConfigLiteral(cfg.GoogleOAuth); literal != "" {
		fields = append(fields, "GoogleOAuth: "+literal)
	}
	if literal := authDevBootstrapConfigLiteral(cfg.DevBootstrap); literal != "" {
		fields = append(fields, "DevBootstrap: "+literal)
	}
	return "sceneryauth.StandardConfig{" + strings.Join(fields, ", ") + "}"
}

func authGoogleConfigLiteral(cfg appcfg.AuthGoogleConfig) string {
	fields := make([]string, 0, 2)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if len(cfg.AllowedScopes) > 0 {
		quoted := make([]string, 0, len(cfg.AllowedScopes))
		for _, scope := range cfg.AllowedScopes {
			quoted = append(quoted, fmt.Sprintf("%q", scope))
		}
		fields = append(fields, "AllowedScopes: []string{"+strings.Join(quoted, ", ")+"}")
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryauth.GoogleOAuthConfig{" + strings.Join(fields, ", ") + "}"
}

func authDevBootstrapConfigLiteral(cfg appcfg.AuthDevBootstrap) string {
	fields := make([]string, 0, 3)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if cfg.DefaultUserEmail != "" {
		fields = append(fields, fmt.Sprintf("DefaultUserEmail: %q", cfg.DefaultUserEmail))
	}
	if cfg.DefaultUserID != "" {
		fields = append(fields, fmt.Sprintf("DefaultUserID: %q", cfg.DefaultUserID))
	}
	if cfg.DefaultTenantID != "" {
		fields = append(fields, fmt.Sprintf("DefaultTenantID: %q", cfg.DefaultTenantID))
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryauth.DevBootstrapConfig{" + strings.Join(fields, ", ") + "}"
}

func appConfigLiteral(appName string, cfg appcfg.Config) string {
	fields := []string{
		fmt.Sprintf("Name: %q", appName),
		"ListenAddr: sceneryruntime.ListenAddrFromEnv()",
	}
	if literal := observabilityConfigLiteral(cfg.Observability); literal != "" {
		fields = append(fields, "Observability: "+literal)
	}
	return "sceneryruntime.AppConfig{" + strings.Join(fields, ", ") + "}"
}

func observabilityConfigLiteral(cfg appcfg.ObservabilityConfig) string {
	fields := make([]string, 0, 2)
	if literal := endpointFilterConfigLiteral(cfg.Logs); literal != "" {
		fields = append(fields, "Logs: "+literal)
	}
	if literal := endpointFilterConfigLiteral(cfg.Tracing); literal != "" {
		fields = append(fields, "Tracing: "+literal)
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryruntime.ObservabilityConfig{" + strings.Join(fields, ", ") + "}"
}

func endpointFilterConfigLiteral(cfg appcfg.EndpointFilterConfig) string {
	fields := make([]string, 0, 2)
	if len(cfg.IncludeEndpoints) > 0 {
		fields = append(fields, "IncludeEndpoints: "+stringSliceLiteral(cfg.IncludeEndpoints))
	}
	if len(cfg.ExcludeEndpoints) > 0 {
		fields = append(fields, "ExcludeEndpoints: "+stringSliceLiteral(cfg.ExcludeEndpoints))
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryruntime.EndpointFilterConfig{" + strings.Join(fields, ", ") + "}"
}

func stringSliceLiteral(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}
