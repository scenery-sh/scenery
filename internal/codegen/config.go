package codegen

import (
	"fmt"
	"go/format"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func generateMain(appModel *model.App, cfg appcfg.Config, options Options) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString("\t\"os\"\n")
	if cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth \"scenery.sh/auth\"\n")
	}
	buf.WriteString("\tsceneryruntime \"scenery.sh/runtime\"\n")
	if options.CompositionImport != "" {
		fmt.Fprintf(&buf, "\tscenerycomposition %q\n", options.CompositionImport)
	}
	for _, pkg := range appModel.Packages {
		if hasResources(pkg) && (pkg.Service == nil || !options.NativeServices[pkg.Service.Name]) {
			fmt.Fprintf(&buf, "\t_ %q\n", pkg.ImportPath)
		}
	}
	buf.WriteString(")\n\n")
	buf.WriteString("func main() {\n")
	if cfg.Auth.Enabled {
		fmt.Fprintf(&buf, "\tif err := sceneryauth.RegisterStandard(%s); err != nil {\n", authConfigLiteral(cfg.Auth))
		buf.WriteString("\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n")
		buf.WriteString("\t\tos.Exit(1)\n")
		buf.WriteString("\t}\n")
	}
	if options.CompositionImport != "" {
		buf.WriteString("\tif err := sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision); err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
		buf.WriteString("\tcontractRegistry, err := sceneryruntime.NewContractRegistry(sceneryruntime.ContractRegistryOptions{ContractRevision: scenerycomposition.ContractRevision, RequiredAddresses: scenerycomposition.RequiredAddresses, ProviderABIs: sceneryruntime.ContractProviderABIs()})\n")
		buf.WriteString("\tif err == nil { err = scenerycomposition.Register(contractRegistry) }\n")
		buf.WriteString("\tif err == nil { err = contractRegistry.Seal() }\n")
		buf.WriteString("\tif err != nil {\n\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n\t\tos.Exit(1)\n\t}\n")
	}
	fmt.Fprintf(&buf, "\tif err := sceneryruntime.Main(%s); err != nil {\n", appConfigLiteral(appModel.Name, cfg))
	buf.WriteString("\t\t_, _ = fmt.Fprintf(os.Stderr, \"scenery: %v\\n\", err)\n")
	buf.WriteString("\t\tos.Exit(1)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")
	return format.Source([]byte(buf.String()))
}

func authConfigLiteral(cfg appcfg.AuthConfig) string {
	fields := []string{"Enabled: true"}
	if cfg.DatabaseURLEnv != "" {
		fields = append(fields, fmt.Sprintf("DatabaseURLEnv: %q", cfg.DatabaseURLEnv))
	}
	if cfg.JWTSecretEnv != "" {
		fields = append(fields, fmt.Sprintf("JWTSecretEnv: %q", cfg.JWTSecretEnv))
	}
	if cfg.RefreshCookieName != "" {
		fields = append(fields, fmt.Sprintf("RefreshCookieName: %q", cfg.RefreshCookieName))
	}
	if cfg.AuthCookieDomainEnv != "" {
		fields = append(fields, fmt.Sprintf("AuthCookieDomainEnv: %q", cfg.AuthCookieDomainEnv))
	}
	if cfg.PublicAppURLEnv != "" {
		fields = append(fields, fmt.Sprintf("PublicAppURLEnv: %q", cfg.PublicAppURLEnv))
	}
	if cfg.APIBaseURLEnv != "" {
		fields = append(fields, fmt.Sprintf("APIBaseURLEnv: %q", cfg.APIBaseURLEnv))
	}
	if cfg.EmailFromEnv != "" {
		fields = append(fields, fmt.Sprintf("EmailFromEnv: %q", cfg.EmailFromEnv))
	}
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
	fields := make([]string, 0, 5)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if cfg.ClientIDEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientIDEnv: %q", cfg.ClientIDEnv))
	}
	if cfg.ClientSecretEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientSecretEnv: %q", cfg.ClientSecretEnv))
	}
	if len(cfg.AllowedScopes) > 0 {
		quoted := make([]string, 0, len(cfg.AllowedScopes))
		for _, scope := range cfg.AllowedScopes {
			quoted = append(quoted, fmt.Sprintf("%q", scope))
		}
		fields = append(fields, "AllowedScopes: []string{"+strings.Join(quoted, ", ")+"}")
	}
	if cfg.TokenCipherKeyEnv != "" {
		fields = append(fields, fmt.Sprintf("TokenCipherKeyEnv: %q", cfg.TokenCipherKeyEnv))
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

func hasResources(pkg *model.Package) bool {
	if len(pkg.Runtime) > 0 {
		return true
	}
	if hasSecretsVar(pkg) {
		return true
	}
	if pkg.Service.Struct != nil && pkg.Service.Struct.Package == pkg {
		return true
	}
	if pkg.Service.AuthHandler != nil && pkg.Service.AuthHandler.Package == pkg {
		return true
	}
	for _, mw := range pkg.Service.Middleware {
		if mw.Package == pkg {
			return true
		}
	}
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg {
			return true
		}
	}
	for _, ep := range pkg.Service.Generated {
		if ep.Package == pkg {
			return true
		}
	}
	return false
}

func hasRaw(endpoints []*model.Endpoint) bool {
	for _, ep := range endpoints {
		if ep.Raw {
			return true
		}
	}
	return false
}

func packageMiddleware(pkg *model.Package) []*model.Middleware {
	var middlewares []*model.Middleware
	for _, mw := range pkg.Service.Middleware {
		if mw.Package == pkg {
			middlewares = append(middlewares, mw)
		}
	}
	return middlewares
}

func needsContextImport(endpoints []*model.Endpoint, authHandler *model.AuthHandler, ss *model.ServiceStruct) bool {
	if ss != nil && ss.Shutdown != "" {
		return true
	}
	if authHandler != nil {
		return true
	}
	for _, ep := range endpoints {
		if !ep.Raw {
			return true
		}
	}
	return false
}

func writeImports(buf *strings.Builder, im *imports) {
	if len(im.entries) == 0 {
		return
	}

	// Rebuild the file with imports first.
	body := buf.String()
	buf.Reset()
	parts := strings.SplitN(body, "\n\n", 2)
	buf.WriteString(parts[0])
	buf.WriteString("\n\nimport (\n")
	for _, entry := range im.sorted() {
		if entry.alias == pathBase(entry.path) {
			fmt.Fprintf(buf, "\t%q\n", entry.path)
		} else {
			fmt.Fprintf(buf, "\t%s %q\n", entry.alias, entry.path)
		}
	}
	buf.WriteString(")\n\n")
	if len(parts) > 1 {
		buf.WriteString(parts[1])
	}
}

func writeServiceStruct(buf *strings.Builder, im *imports, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "var %s struct {\n\tonce sync.Once\n\tsvc *%s\n\terr error\n}\n\n", ss.InstanceVar, ss.TypeName)
	fmt.Fprintf(buf, "func %s() (*%s, error) {\n", ss.GetterName, ss.TypeName)
	fmt.Fprintf(buf, "\tif mock, ok, err := sceneryruntime.LookupServiceMock(sceneryruntime.TypeOf[*%s]()); ok || err != nil {\n", ss.TypeName)
	buf.WriteString("\t\tif err != nil {\n")
	buf.WriteString("\t\t\treturn nil, err\n")
	buf.WriteString("\t\t}\n")
	fmt.Fprintf(buf, "\t\tif mock == nil {\n\t\t\treturn (*%s)(nil), nil\n\t\t}\n", ss.TypeName)
	fmt.Fprintf(buf, "\t\treturn mock.(*%s), nil\n", ss.TypeName)
	buf.WriteString("\t}\n")
	fmt.Fprintf(buf, "\t%s.once.Do(func() {\n", ss.InstanceVar)
	buf.WriteString("\t\tstarted := time.Now()\n")
	if ss.InitFunc != "" {
		fmt.Fprintf(buf, "\t\t%s.svc, %s.err = %s()\n", ss.InstanceVar, ss.InstanceVar, ss.InitFunc)
	} else {
		fmt.Fprintf(buf, "\t\t%s.svc = &%s{}\n", ss.InstanceVar, ss.TypeName)
	}
	if ss.Shutdown != "" {
		fmt.Fprintf(buf, "\t\tif %s.err == nil && %s.svc != nil {\n", ss.InstanceVar, ss.InstanceVar)
		fmt.Fprintf(buf, "\t\t\tsceneryruntime.MarkServiceInitialized(%q, func(force context.Context) { %s.svc.%s(force) })\n", ss.Service.Name, ss.InstanceVar, ss.Shutdown)
		buf.WriteString("\t\t}\n")
	}
	fmt.Fprintf(buf, "\t\tsceneryruntime.RecordServiceInit(%q, time.Since(started), %s.err)\n", ss.Service.Name, ss.InstanceVar)
	buf.WriteString("\t})\n")
	fmt.Fprintf(buf, "\treturn %s.svc, %s.err\n", ss.InstanceVar, ss.InstanceVar)
	buf.WriteString("}\n\n")
}
