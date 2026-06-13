package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/wiremodel"
)

type Output struct {
	Rewritten map[string][]byte
	Generated map[string][]byte
}

func Generate(app *model.App) (*Output, error) {
	return GenerateWithConfig(app, appcfg.Config{})
}

func GenerateWithConfig(appModel *model.App, cfg appcfg.Config) (*Output, error) {
	out := &Output{
		Rewritten: make(map[string][]byte),
		Generated: make(map[string][]byte),
	}

	restoreEndpointDecls := rewriteEndpointDecls(appModel)
	defer restoreEndpointDecls()
	for _, pkg := range appModel.Packages {
		for _, file := range pkg.Files {
			rel, err := filepath.Rel(appModel.Root, file.Path)
			if err != nil {
				return nil, err
			}
			if changed := fileChanged(pkg, file); changed {
				data, err := renderFile(pkg.GoPkg.Fset, file.AST)
				if err != nil {
					return nil, err
				}
				out.Rewritten[filepath.ToSlash(rel)] = data
			}
		}
	}

	for _, pkg := range appModel.Packages {
		hasSecrets := hasSecretsVar(pkg)
		if hasSecrets {
			data, err := generateEarlyConfigFile(pkg, hasSecrets)
			if err != nil {
				return nil, err
			}
			if len(data) > 0 {
				rel := filepath.ToSlash(filepath.Join(pkg.RelDir, "00_scenery_config.gen.go"))
				if pkg.RelDir == "." {
					rel = "00_scenery_config.gen.go"
				}
				out.Generated[rel] = data
			}
		}
		data, err := generatePackageFile(pkg, cfg)
		if err != nil {
			return nil, err
		}
		if len(data) > 0 {
			rel := filepath.ToSlash(filepath.Join(pkg.RelDir, "scenery.gen.go"))
			if pkg.RelDir == "." {
				rel = "scenery.gen.go"
			}
			out.Generated[rel] = data
		}
	}

	mainFile, err := generateMain(appModel, cfg)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	return out, nil
}

func generateEarlyConfigFile(pkg *model.Package, hasSecrets bool) ([]byte, error) {
	var buf strings.Builder
	fmt.Fprintf(&buf, "package %s\n\n", pkg.Name)
	buf.WriteString("import sceneryruntime \"scenery.sh/runtime\"\n\n")
	buf.WriteString("var sceneryInternalDotEnvInitialized = sceneryruntime.MustLoadDotEnvIntoEnv()\n")
	if hasSecrets {
		buf.WriteString("\n")
		buf.WriteString("var sceneryInternalSecretsInitialized = func() bool {\n")
		buf.WriteString("\tsceneryruntime.MustPopulateSecrets(&secrets)\n")
		buf.WriteString("\treturn true\n")
		buf.WriteString("}()\n")
	}
	return format.Source([]byte(buf.String()))
}

func rewriteEndpointDecls(app *model.App) func() {
	type rewrittenDecl struct {
		ident *ast.Ident
		name  string
	}
	var rewritten []rewrittenDecl
	for _, svc := range app.Services {
		for _, ep := range svc.Endpoints {
			if ep.Decl == nil || ep.Decl.Name == nil {
				continue
			}
			rewritten = append(rewritten, rewrittenDecl{
				ident: ep.Decl.Name,
				name:  ep.Decl.Name.Name,
			})
			ep.Decl.Name.Name = ep.ImplName
		}
	}
	return func() {
		for _, decl := range rewritten {
			decl.ident.Name = decl.name
		}
	}
}

func fileChanged(pkg *model.Package, file *model.File) bool {
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg && ep.File == file {
			return true
		}
	}
	return false
}

func renderFile(fset *token.FileSet, file any) ([]byte, error) {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return nil, err
	}
	return format.Source(buf.Bytes())
}

func generatePackageFile(pkg *model.Package, cfg appcfg.Config) ([]byte, error) {
	var pkgEndpoints []*model.Endpoint
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg {
			pkgEndpoints = append(pkgEndpoints, ep)
		}
	}
	var generatedModelEndpoints []*model.GeneratedModelEndpoint
	for _, ep := range pkg.Service.Generated {
		if ep.Package == pkg {
			generatedModelEndpoints = append(generatedModelEndpoints, ep)
		}
	}
	authHandler := pkg.Service.AuthHandler
	if authHandler != nil && authHandler.Package != pkg {
		authHandler = nil
	}
	pkgMiddleware := packageMiddleware(pkg)
	hasSecrets := hasSecretsVar(pkg)
	serviceStruct := pkg.Service.Struct
	if serviceStruct != nil && serviceStruct.Package != pkg {
		serviceStruct = nil
	}
	if len(pkgEndpoints) == 0 && len(generatedModelEndpoints) == 0 && len(pkgMiddleware) == 0 && authHandler == nil && serviceStruct == nil && !hasSecrets {
		return nil, nil
	}

	slices.SortFunc(pkgEndpoints, func(a, b *model.Endpoint) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(generatedModelEndpoints, func(a, b *model.GeneratedModelEndpoint) int {
		return strings.Compare(a.Name, b.Name)
	})

	im := newImports(pkg.ImportPath)
	im.use("sceneryruntime", "scenery.sh/runtime")
	if needsContextImport(pkgEndpoints, authHandler, serviceStruct) {
		im.use("context", "context")
	}
	if len(generatedModelEndpoints) > 0 {
		im.use("context", "context")
		im.use("errs", "scenery.sh/errs")
	}
	if len(pkgMiddleware) > 0 {
		im.use("scenerymiddleware", "scenery.sh/middleware")
	}
	if serviceStruct != nil {
		im.use("scenerytemporal", "scenery.sh/temporal")
		im.use("sync", "sync")
		im.use("time", "time")
	}
	if hasRaw(pkgEndpoints) {
		im.use("http", "net/http")
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "package %s\n\n", pkg.Name)

	if serviceStruct != nil {
		writeServiceStruct(&buf, im, serviceStruct)
	}
	writeGeneratedModelBackend(&buf, im, generatedModelEndpoints, cfg)
	for _, ep := range pkgEndpoints {
		writeEndpoint(&buf, im, ep, serviceStruct)
	}
	writeRegistrations(&buf, im, pkgEndpoints, generatedModelEndpoints, pkgMiddleware, authHandler, serviceStruct, hasSecrets)
	writeImports(&buf, im)

	return format.Source([]byte(buf.String()))
}

func generateMain(appModel *model.App, cfg appcfg.Config) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString("\t\"os\"\n")
	if cfg.Auth.Enabled {
		buf.WriteString("\tsceneryauth \"scenery.sh/auth\"\n")
	}
	buf.WriteString("\tsceneryruntime \"scenery.sh/runtime\"\n")
	if effectiveTemporalConfig(appModel, cfg).Enabled {
		buf.WriteString("\t_ \"scenery.sh/temporal\"\n")
	}
	for _, pkg := range appModel.Packages {
		if hasResources(pkg) {
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
	fmt.Fprintf(&buf, "\tif err := sceneryruntime.Main(%s); err != nil {\n", appConfigLiteral(appModel, cfg))
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
	fields := make([]string, 0, 3)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if cfg.ClientIDEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientIDEnv: %q", cfg.ClientIDEnv))
	}
	if cfg.ClientSecretEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientSecretEnv: %q", cfg.ClientSecretEnv))
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

func appConfigLiteral(appModel *model.App, cfg appcfg.Config) string {
	workspace := cfg.Proxy.Workspace
	if workspace == "" {
		workspace = filepath.Base(appModel.Root)
	}
	fields := []string{
		fmt.Sprintf("Name: %q", appModel.Name),
		fmt.Sprintf("Workspace: %q", workspace),
		"ListenAddr: sceneryruntime.ListenAddrFromEnv()",
	}
	if cfg.Proxy.APIHost != "" {
		fields = append(fields, fmt.Sprintf("ProxyAPIHost: %q", cfg.Proxy.APIHost))
	}
	if cfg.Proxy.ConsoleHost != "" {
		fields = append(fields, fmt.Sprintf("ProxyConsoleHost: %q", cfg.Proxy.ConsoleHost))
	}
	if cfg.Proxy.TemporalHost != "" {
		fields = append(fields, fmt.Sprintf("ProxyTemporalHost: %q", cfg.Proxy.TemporalHost))
	}
	if cfg.Proxy.GrafanaHost != "" {
		fields = append(fields, fmt.Sprintf("ProxyGrafanaHost: %q", cfg.Proxy.GrafanaHost))
	}
	if literal := proxyFrontendsLiteral(cfg.Proxy.Frontends); literal != "" {
		fields = append(fields, "ProxyFrontends: "+literal)
	}
	if literal := observabilityConfigLiteral(cfg.Observability); literal != "" {
		fields = append(fields, "Observability: "+literal)
	}
	if literal := temporalConfigLiteral(effectiveTemporalConfig(appModel, cfg)); literal != "" {
		fields = append(fields, "Temporal: "+literal)
	}
	return "sceneryruntime.AppConfig{" + strings.Join(fields, ", ") + "}"
}

func effectiveTemporalConfig(_ *model.App, cfg appcfg.Config) appcfg.TemporalConfig {
	return cfg.Temporal
}

func appUsesTemporalRuntime(appModel *model.App) bool {
	if appModel == nil {
		return false
	}
	for _, decl := range appModel.Runtime {
		switch decl.Kind {
		case model.RuntimeDeclarationTemporalWorkflow, model.RuntimeDeclarationTemporalActivity, model.RuntimeDeclarationTemporalExternalActivity, model.RuntimeDeclarationCronJob:
			return true
		}
	}
	return false
}

func AppUsesTemporalRuntime(appModel *model.App) bool {
	return appUsesTemporalRuntime(appModel)
}

func proxyFrontendsLiteral(frontends map[string]appcfg.FrontendConfig) string {
	if len(frontends) == 0 {
		return ""
	}
	names := make([]string, 0, len(frontends))
	for name := range frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		frontend := frontends[name]
		fields := []string{}
		if frontend.Host != "" {
			fields = append(fields, fmt.Sprintf("Host: %q", frontend.Host))
		}
		if frontend.Root != "" {
			fields = append(fields, fmt.Sprintf("Root: %q", frontend.Root))
		}
		if frontend.Upstream != "" {
			fields = append(fields, fmt.Sprintf("Upstream: %q", frontend.Upstream))
		}
		entries = append(entries, fmt.Sprintf("%q: {%s}", name, strings.Join(fields, ", ")))
	}
	return "map[string]sceneryruntime.ProxyFrontendConfig{" + strings.Join(entries, ", ") + "}"
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

func temporalConfigLiteral(cfg appcfg.TemporalConfig) string {
	fields := make([]string, 0, 7)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if cfg.Mode != "" {
		fields = append(fields, fmt.Sprintf("Mode: %q", cfg.Mode))
	}
	if cfg.Namespace != "" {
		fields = append(fields, fmt.Sprintf("Namespace: %q", cfg.Namespace))
	}
	if cfg.AddressEnv != "" {
		fields = append(fields, fmt.Sprintf("AddressEnv: %q", cfg.AddressEnv))
	}
	if cfg.TaskQueuePrefix != "" {
		fields = append(fields, fmt.Sprintf("TaskQueuePrefix: %q", cfg.TaskQueuePrefix))
	}
	if cfg.PayloadCodec != "" {
		fields = append(fields, fmt.Sprintf("PayloadCodec: %q", cfg.PayloadCodec))
	}
	if cfg.APIKeyEnv != "" {
		fields = append(fields, fmt.Sprintf("APIKeyEnv: %q", cfg.APIKeyEnv))
	}
	if literal := temporalTLSConfigLiteral(cfg.TLS); literal != "" {
		fields = append(fields, "TLS: "+literal)
	}
	if literal := temporalLocalConfigLiteral(cfg.Local); literal != "" {
		fields = append(fields, "Local: "+literal)
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryruntime.TemporalConfig{" + strings.Join(fields, ", ") + "}"
}

func temporalTLSConfigLiteral(cfg appcfg.TemporalTLSConfig) string {
	fields := make([]string, 0, 5)
	if cfg.Enabled {
		fields = append(fields, "Enabled: true")
	}
	if cfg.ServerNameEnv != "" {
		fields = append(fields, fmt.Sprintf("ServerNameEnv: %q", cfg.ServerNameEnv))
	}
	if cfg.CACertFileEnv != "" {
		fields = append(fields, fmt.Sprintf("CACertFileEnv: %q", cfg.CACertFileEnv))
	}
	if cfg.ClientCertFileEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientCertFileEnv: %q", cfg.ClientCertFileEnv))
	}
	if cfg.ClientKeyFileEnv != "" {
		fields = append(fields, fmt.Sprintf("ClientKeyFileEnv: %q", cfg.ClientKeyFileEnv))
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryruntime.TemporalTLSConfig{" + strings.Join(fields, ", ") + "}"
}

func temporalLocalConfigLiteral(cfg appcfg.TemporalLocalConfig) string {
	fields := make([]string, 0, 2)
	if cfg.AutoStart {
		fields = append(fields, "AutoStart: true")
	}
	if cfg.DBFilename != "" {
		fields = append(fields, fmt.Sprintf("DBFilename: %q", cfg.DBFilename))
	}
	if len(fields) == 0 {
		return ""
	}
	return "sceneryruntime.TemporalLocalConfig{" + strings.Join(fields, ", ") + "}"
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

func writeEndpoint(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	if !ep.Raw {
		writeInternalHelper(buf, im, ep)
	}
	writePackageWrapper(buf, im, ep, ss)
	if ep.Receiver != nil {
		writeMethodWrapper(buf, im, ep)
	}
}

func writeGeneratedModelBackend(buf *strings.Builder, im *imports, endpoints []*model.GeneratedModelEndpoint, cfg appcfg.Config) {
	if len(endpoints) == 0 {
		return
	}
	entities := generatedModelEntities(endpoints)
	for _, entity := range entities {
		writeGeneratedModelTypes(buf, im, entity)
		writeGeneratedModelStore(buf, im, entity, cfg)
	}
}

func generatedModelEntities(endpoints []*model.GeneratedModelEndpoint) []*model.Entity {
	seen := map[*model.Entity]bool{}
	var out []*model.Entity
	for _, ep := range endpoints {
		if ep == nil || ep.Entity == nil || seen[ep.Entity] {
			continue
		}
		seen[ep.Entity] = true
		out = append(out, ep.Entity)
	}
	slices.SortFunc(out, func(a, b *model.Entity) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func writeGeneratedModelTypes(buf *strings.Builder, im *imports, entity *model.Entity) {
	createType := generatedModelCreateType(entity)
	patchType := generatedModelPatchType(entity)
	fmt.Fprintf(buf, "type %s struct {\n", createType)
	for _, field := range generatedModelCreateFields(entity) {
		fmt.Fprintf(buf, "\t%s %s `json:%q`\n", field.Name, entityFieldTypeExpr(im, field), field.Column+",omitempty")
	}
	buf.WriteString("}\n\n")
	fmt.Fprintf(buf, "type %s struct {\n", patchType)
	for _, field := range generatedModelPatchFields(entity) {
		fmt.Fprintf(buf, "\t%s %s `json:%q`\n", field.Name, entityFieldPatchTypeExpr(im, field), field.Column+",omitempty")
	}
	buf.WriteString("}\n\n")
}

func writeGeneratedModelStore(buf *strings.Builder, im *imports, entity *model.Entity, cfg appcfg.Config) {
	errorsPkg := im.use("errors", "errors")
	fmtPkg := im.use("fmt", "fmt")
	osPkg := im.use("os", "os")
	stringsPkg := im.use("strings", "strings")
	syncPkg := im.use("sync", "sync")
	pgxPkg := im.use("pgx", "github.com/jackc/pgx/v5")
	pgconnPkg := im.use("pgconn", "github.com/jackc/pgx/v5/pgconn")
	pgxpoolPkg := im.use("pgxpool", "scenery.sh/pgxpool")
	stateName := generatedModelDBStateName(entity)
	poolFunc := generatedModelPoolFunc(entity)
	keyFunc := generatedModelKeyFunc(entity)
	fromCreate := generatedModelFromCreateFunc(entity)
	tenantField := entity.TenantField()
	tenantFunc := ""
	authPkg := ""
	if tenantField != nil {
		authPkg = im.use("sceneryauth", "scenery.sh/auth")
		tenantFunc = generatedModelTenantFunc(entity)
	}
	tenantValueFunc := ""
	if tenantField != nil {
		tenantValueFunc = generatedModelTenantValueFunc(entity)
	}
	entityType := entity.Name
	id := generatedModelIDField(entity)
	fields := generatedModelStoredFields(entity)
	createFields := generatedModelCreateFields(entity)
	patchFields := generatedModelPatchFields(entity)
	selectSQL := generatedModelSelectSQL(entity, fields)
	table := generatedModelSQLTable(entity)
	idColumn := generatedModelSQLIdent(id.Column)
	databaseURLEnv := cfg.DatabaseURLEnv()
	fmt.Fprintf(buf, "var %s = struct {\n\t%s.Mutex\n\tpool *%s.Pool\n\tdsn string\n}{}\n\n", stateName, syncPkg, pgxpoolPkg)
	fmt.Fprintf(buf, "func %s(ctx context.Context) (*%s.Pool, error) {\n", poolFunc, pgxpoolPkg)
	fmt.Fprintf(buf, "\tdsn := %s.TrimSpace(%s.Getenv(%q))\n", stringsPkg, osPkg, databaseURLEnv)
	fmt.Fprintf(buf, "\tif dsn == \"\" {\n\t\tdsn = %s.TrimSpace(%s.Getenv(\"SCENERY_MANAGED_DATABASE_URL\"))\n\t}\n", stringsPkg, osPkg)
	fmt.Fprintf(buf, "\tif dsn == \"\" {\n\t\treturn nil, errs.B().Code(errs.FailedPrecondition).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" store requires "+databaseURLEnv)
	fmt.Fprintf(buf, "\t%s.Lock()\n\tdefer %s.Unlock()\n", stateName, stateName)
	fmt.Fprintf(buf, "\tif %s.pool != nil && %s.dsn == dsn {\n\t\treturn %s.pool, nil\n\t}\n", stateName, stateName, stateName)
	fmt.Fprintf(buf, "\tif %s.pool != nil {\n\t\t%s.pool.Close()\n\t\t%s.pool = nil\n\t}\n", stateName, stateName, stateName)
	fmt.Fprintf(buf, "\tpool, err := %s.New(ctx, dsn)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", pgxpoolPkg)
	fmt.Fprintf(buf, "\t%s.pool = pool\n\t%s.dsn = dsn\n\treturn pool, nil\n}\n\n", stateName, stateName)
	fmt.Fprintf(buf, "func %s(id any) string {\n\treturn %s.Sprint(id)\n}\n\n", keyFunc, fmtPkg)
	fmt.Fprintf(buf, "func %s(input %s) %s {\n\treturn %s{\n", fromCreate, generatedModelCreateType(entity), entityType, entityType)
	for _, field := range createFields {
		fmt.Fprintf(buf, "\t\t%s: input.%s,\n", field.Name, field.Name)
	}
	buf.WriteString("\t}\n}\n\n")
	if tenantField != nil {
		fmt.Fprintf(buf, "func %s() (string, error) {\n", tenantFunc)
		fmt.Fprintf(buf, "\tauthData, ok := %s.CurrentAuthData()\n", authPkg)
		fmt.Fprintf(buf, "\tif !ok || authData == nil || %s.TrimSpace(string(authData.TenantID)) == \"\" {\n", stringsPkg)
		fmt.Fprintf(buf, "\t\treturn \"\", errs.B().Code(errs.Unauthenticated).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" store requires active tenant")
		fmt.Fprintf(buf, "\treturn %s.TrimSpace(string(authData.TenantID)), nil\n}\n\n", stringsPkg)
		writeGeneratedModelTenantValueFunc(buf, im, entity, *tenantField, tenantValueFunc)
	}
	fmt.Fprintf(buf, "func sceneryModelScan%s(row %s.Row) (%s, error) {\n\tvar out %s\n", entity.Name, pgxPkg, entityType, entityType)
	fmt.Fprintf(buf, "\tif err := row.Scan(%s); err != nil {\n\t\tif %s.Is(err, %s.ErrNoRows) {\n\t\t\treturn %s{}, errs.B().Code(errs.NotFound).Msg(%q).Err()\n\t\t}\n\t\treturn %s{}, err\n\t}\n\treturn out, nil\n}\n\n", generatedModelScanArgs(fields, "out"), errorsPkg, pgxPkg, entityType, entity.Name+" not found", entityType)
	fmt.Fprintf(buf, "func sceneryModelList%s(ctx context.Context) ([]%s, error) {\n", entity.Name, entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", poolFunc)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", tenantFunc)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", tenantValueFunc)
		fmt.Fprintf(buf, "\trows, err := pool.Query(ctx, %q, tenantValue)\n", selectSQL+" where "+generatedModelSQLIdent(tenantField.Column)+" = $1 order by "+idColumn)
	} else {
		fmt.Fprintf(buf, "\trows, err := pool.Query(ctx, %q)\n", selectSQL+" order by "+idColumn)
	}
	buf.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n\tdefer rows.Close()\n")
	fmt.Fprintf(buf, "\tout := []%s{}\n\tfor rows.Next() {\n\t\tvar item %s\n", entityType, entityType)
	fmt.Fprintf(buf, "\t\tif err := rows.Scan(%s); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tout = append(out, item)\n\t}\n\tif err := rows.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n", generatedModelScanArgs(fields, "item"))
	fmt.Fprintf(buf, "func sceneryModelGet%s(ctx context.Context, id any) (%s, error) {\n", entity.Name, entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, entityType)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRow(ctx, %q, id, tenantValue))\n}\n\n", entity.Name, selectSQL+" where "+idColumn+" = $1 and "+generatedModelSQLIdent(tenantField.Column)+" = $2")
	} else {
		fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRow(ctx, %q, id))\n}\n\n", entity.Name, selectSQL+" where "+idColumn+" = $1")
	}
	fmt.Fprintf(buf, "func sceneryModelCreate%s(ctx context.Context, input %s) (%s, error) {\n", entity.Name, generatedModelCreateType(entity), entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, entityType)
	fmt.Fprintf(buf, "\trow := %s(input)\n\tkey := %s(row.%s)\n", fromCreate, keyFunc, id.Name)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		fmt.Fprintf(buf, "\trow.%s = tenantValue\n", tenantField.Name)
	}
	fmt.Fprintf(buf, "\tif key == \"\" {\n\t\treturn %s{}, errs.B().Code(errs.InvalidArgument).Msg(%q).Err()\n\t}\n", entityType, entity.Name+" ID is required")
	fmt.Fprintf(buf, "\tcreated, err := sceneryModelScan%s(pool.QueryRow(ctx, %q, %s))\n", entity.Name, generatedModelInsertSQL(entity, fields), generatedModelFieldArgs(fields, "row"))
	fmt.Fprintf(buf, "\tif err != nil {\n\t\tvar pgErr *%s.PgError\n\t\tif %s.As(err, &pgErr) && pgErr.Code == \"23505\" {\n\t\t\treturn %s{}, errs.B().Code(errs.AlreadyExists).Msgf(%q, key).Err()\n\t\t}\n\t\treturn %s{}, err\n\t}\n\treturn created, nil\n}\n\n", pgconnPkg, errorsPkg, entityType, entity.Name+" %s already exists", entityType)
	fmt.Fprintf(buf, "func sceneryModelUpdate%s(ctx context.Context, id any, patch %s) (%s, error) {\n", entity.Name, generatedModelPatchType(entity), entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, entityType)
	buf.WriteString("\tsets := []string{}\n\targs := []any{}\n")
	for _, field := range patchFields {
		fmt.Fprintf(buf, "\tif patch.%s != nil {\n\t\targs = append(args, *patch.%s)\n\t\tsets = append(sets, %s.Sprintf(%q, len(args)))\n\t}\n", field.Name, field.Name, fmtPkg, generatedModelSQLIdent(field.Column)+" = $%d")
	}
	fmt.Fprintf(buf, "\tif len(sets) == 0 {\n\t\treturn sceneryModelGet%s(ctx, id)\n\t}\n", entity.Name)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		fmt.Fprintf(buf, "\targs = append(args, id, tenantValue)\n\tquery := %s.Sprintf(%q, %s.Join(sets, \", \"), len(args)-1, len(args))\n", fmtPkg, "update "+table+" set %s where "+idColumn+" = $%d and "+generatedModelSQLIdent(tenantField.Column)+" = $%d returning "+generatedModelColumnList(fields), stringsPkg)
	} else {
		fmt.Fprintf(buf, "\targs = append(args, id)\n\tquery := %s.Sprintf(%q, %s.Join(sets, \", \"), len(args))\n", fmtPkg, "update "+table+" set %s where "+idColumn+" = $%d returning "+generatedModelColumnList(fields), stringsPkg)
	}
	fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRow(ctx, query, args...))\n}\n\n", entity.Name)
	fmt.Fprintf(buf, "func sceneryModelDelete%s(ctx context.Context, id any) error {\n", entity.Name)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n", poolFunc)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn err\n\t}\n", tenantFunc)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn err\n\t}\n", tenantValueFunc)
		fmt.Fprintf(buf, "\ttag, err := pool.Exec(ctx, %q, id, tenantValue)\n", "delete from "+table+" where "+idColumn+" = $1 and "+generatedModelSQLIdent(tenantField.Column)+" = $2")
	} else {
		fmt.Fprintf(buf, "\ttag, err := pool.Exec(ctx, %q, id)\n", "delete from "+table+" where "+idColumn+" = $1")
	}
	fmt.Fprintf(buf, "\tif err != nil {\n\t\treturn err\n\t}\n\tif tag.RowsAffected() == 0 {\n\t\treturn errs.B().Code(errs.NotFound).Msgf(%q, %s(id)).Err()\n\t}\n\treturn nil\n}\n\n", entity.Name+" %s not found", keyFunc)
}

func generatedModelStoredFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	for _, field := range entity.Fields {
		if field.Kind == model.EntityFieldComputed {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelPatchFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range generatedModelStoredFields(entity) {
		if strings.EqualFold(field.Name, "id") {
			continue
		}
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelCreateFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range generatedModelStoredFields(entity) {
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelIDField(entity *model.Entity) model.EntityField {
	for _, field := range entity.Fields {
		if field.Kind != model.EntityFieldComputed && strings.EqualFold(field.Name, "id") {
			return field
		}
	}
	return model.EntityField{Name: "ID", TypeExpr: "string", Column: "id"}
}

func generatedModelSQLIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func generatedModelSQLTable(entity *model.Entity) string {
	return generatedModelSQLIdent(model.EntityDatabaseSchema(entity)) + "." + generatedModelSQLIdent(entity.Table)
}

func generatedModelColumnList(fields []model.EntityField) string {
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, generatedModelSQLIdent(field.Column))
	}
	return strings.Join(columns, ", ")
}

func generatedModelSelectSQL(entity *model.Entity, fields []model.EntityField) string {
	return "select " + generatedModelColumnList(fields) + " from " + generatedModelSQLTable(entity)
}

func generatedModelInsertSQL(entity *model.Entity, fields []model.EntityField) string {
	columns := generatedModelColumnList(fields)
	placeholders := make([]string, 0, len(fields))
	for i := range fields {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	return "insert into " + generatedModelSQLTable(entity) + " (" + columns + ") values (" + strings.Join(placeholders, ", ") + ") returning " + columns
}

func generatedModelScanArgs(fields []model.EntityField, target string) string {
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		args = append(args, "&"+target+"."+field.Name)
	}
	return strings.Join(args, ", ")
}

func generatedModelFieldArgs(fields []model.EntityField, target string) string {
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		args = append(args, target+"."+field.Name)
	}
	return strings.Join(args, ", ")
}

func writeGeneratedModelTenantValueFunc(buf *strings.Builder, im *imports, entity *model.Entity, field model.EntityField, name string) {
	typeExpr := entityFieldTypeExpr(im, field)
	if model.GeneratedTenantFieldKind(field) == "uuid" {
		uuidPkg := im.use("uuid", "github.com/google/uuid")
		fmt.Fprintf(buf, "func %s(tenantID string) (%s, error) {\n", name, typeExpr)
		fmt.Fprintf(buf, "\ttenantUUID, err := %s.Parse(tenantID)\n", uuidPkg)
		buf.WriteString("\tif err != nil {\n")
		fmt.Fprintf(buf, "\t\tvar zero %s\n", typeExpr)
		fmt.Fprintf(buf, "\t\treturn zero, errs.B().Code(errs.InvalidArgument).Msg(%q).Cause(err).Err()\n", "generated "+entity.Name+" store requires valid tenant_id UUID")
		buf.WriteString("\t}\n")
		buf.WriteString("\treturn tenantUUID, nil\n")
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "func %s(tenantID string) (%s, error) {\n\treturn %s(tenantID), nil\n}\n\n", name, typeExpr, typeExpr)
}

func entityFieldTypeExpr(im *imports, field model.EntityField) string {
	if field.Type != nil {
		return im.typeExpr(field.Type)
	}
	return field.TypeExpr
}

func entityFieldPatchTypeExpr(im *imports, field model.EntityField) string {
	if ptr, ok := field.Type.(*types.Pointer); ok {
		return "*" + im.typeExpr(ptr.Elem())
	}
	return "*" + entityFieldTypeExpr(im, field)
}

func generatedModelDBStateName(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "DB"
}

func generatedModelPoolFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "Pool"
}

func generatedModelKeyFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "Key"
}

func generatedModelFromCreateFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "FromCreate"
}

func generatedModelTenantFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "TenantID"
}

func generatedModelTenantValueFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "TenantValue"
}

func generatedModelCreateType(entity *model.Entity) string {
	return entity.Name + "Create"
}

func generatedModelPatchType(entity *model.Entity) string {
	return entity.Name + "Patch"
}

func writeInternalHelper(buf *strings.Builder, im *imports, ep *model.Endpoint) {
	fmt.Fprintf(buf, "func sceneryInternalCall%s(%s)%s {\n", ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))

	ctxName := generatedFieldName(ep.Params[0], 0)
	pathArgs := "nil"
	if len(ep.PathParams) > 0 {
		var args []string
		for _, path := range ep.PathParams {
			args = append(args, path.Name)
		}
		pathArgs = "[]any{" + strings.Join(args, ", ") + "}"
	}
	payload := "nil"
	if ep.Payload != nil {
		payload = generatedFieldName(*ep.Payload, len(ep.Params)-1)
	}
	if ep.Response == nil {
		fmt.Fprintf(buf, "\t_, err := sceneryruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
		buf.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		buf.WriteString("\treturn nil\n")
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "\tresp, err := sceneryruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
	respType := im.typeExpr(ep.Response.Type)
	fmt.Fprintf(buf, "\tif err != nil {\n\t\tvar zero %s\n\t\treturn zero, err\n\t}\n", respType)
	fmt.Fprintf(buf, "\tif resp == nil {\n\t\tvar zero %s\n\t\treturn zero, nil\n\t}\n", respType)
	fmt.Fprintf(buf, "\treturn resp.(%s), nil\n", respType)
	buf.WriteString("}\n\n")
}

func writePackageWrapper(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "func %s(%s)%s {\n", ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))
	if ep.Raw {
		if ep.Receiver != nil && ss != nil {
			fmt.Fprintf(buf, "\tsvc, err := %s()\n", ss.GetterName)
			buf.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
			fmt.Fprintf(buf, "\tsvc.%s(%s)\n", ep.ImplName, joinParamNames(ep.Params))
		} else {
			fmt.Fprintf(buf, "\t%s(%s)\n", ep.ImplName, joinParamNames(ep.Params))
		}
		buf.WriteString("}\n\n")
		return
	}
	call := fmt.Sprintf("sceneryInternalCall%s(%s)", ep.Name, joinParamNames(ep.Params))
	if ep.Response == nil {
		fmt.Fprintf(buf, "\treturn %s\n", call)
	} else {
		fmt.Fprintf(buf, "\treturn %s\n", call)
	}
	buf.WriteString("}\n\n")
}

func writeMethodWrapper(buf *strings.Builder, im *imports, ep *model.Endpoint) {
	fmt.Fprintf(buf, "func (%s %s) %s(%s)%s {\n", ep.Receiver.Name, ep.Receiver.TypeExpr, ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))
	if ep.Raw {
		fmt.Fprintf(buf, "\t%s.%s(%s)\n", ep.Receiver.Name, ep.ImplName, joinParamNames(ep.Params))
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "\treturn sceneryInternalCall%s(%s)\n", ep.Name, joinParamNames(ep.Params))
	buf.WriteString("}\n\n")
}

func writeRegistrations(buf *strings.Builder, im *imports, endpoints []*model.Endpoint, generatedModelEndpoints []*model.GeneratedModelEndpoint, middlewares []*model.Middleware, authHandler *model.AuthHandler, ss *model.ServiceStruct, hasSecrets bool) {
	buf.WriteString("func init() {\n")
	if hasSecrets {
		buf.WriteString("\tsceneryruntime.MustPopulateSecrets(&secrets)\n")
	}
	if ss != nil {
		fmt.Fprintf(buf, "\tsceneryruntime.RegisterServiceInitializer(%q, func() error {\n", ss.Service.Name)
		fmt.Fprintf(buf, "\t\t_, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\treturn err\n")
		buf.WriteString("\t})\n")
		fmt.Fprintf(buf, "\tscenerytemporal.RegisterServiceAccessorFor[*%s](func() (any, error) {\n", ss.TypeName)
		fmt.Fprintf(buf, "\t\treturn %s()\n", ss.GetterName)
		buf.WriteString("\t})\n")
	}
	for _, mw := range middlewares {
		writeMiddlewareRegistration(buf, im, mw, ss)
	}
	for _, ep := range endpoints {
		fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpointFunc(%s, %q, %q)\n", ep.Name, ep.Service.Name, ep.Name)
		writeEndpointRegistration(buf, im, ep, ss)
	}
	for _, ep := range generatedModelEndpoints {
		writeGeneratedModelEndpointRegistration(buf, ep)
	}
	if authHandler != nil {
		writeAuthRegistration(buf, im, authHandler, ss)
	}
	buf.WriteString("}\n")
}

func writeEndpointRegistration(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	wireInfo := wiremodel.Endpoint(ep)
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ep.Service.Name)
	fmt.Fprintf(buf, "\t\tName: %q,\n", ep.Name)
	fmt.Fprintf(buf, "\t\tAccess: sceneryruntime.%s,\n", exportAccess(ep.Access))
	fmt.Fprintf(buf, "\t\tRaw: %t,\n", ep.Raw)
	fmt.Fprintf(buf, "\t\tPath: %q,\n", ep.Path)
	fmt.Fprintf(buf, "\t\tMethods: %s,\n", renderMethodLiteral(ep.Methods))
	if len(ep.Middleware) > 0 {
		fmt.Fprintf(buf, "\t\tMiddlewareIDs: %s,\n", renderMiddlewareIDs(ep.Middleware))
	}
	fmt.Fprintf(buf, "\t\tPathParams: %s,\n", renderParamSpecs(ep.PathParams))
	if ep.Payload != nil {
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ep.Payload.Type))
	} else {
		buf.WriteString("\t\tPayloadType: nil,\n")
	}
	if ep.Response != nil {
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ep.Response.Type))
	} else {
		buf.WriteString("\t\tResponseType: nil,\n")
	}
	fmt.Fprintf(buf, "\t\tWireID: %q,\n", wireInfo.ID)
	fmt.Fprintf(buf, "\t\tWireSchemaHash: %q,\n", wireInfo.SchemaHash)
	fmt.Fprintf(buf, "\t\tWireAvailable: %t,\n", wireInfo.Available)
	if wireInfo.UnsupportedReason != "" {
		fmt.Fprintf(buf, "\t\tWireUnsupportedReason: %q,\n", wireInfo.UnsupportedReason)
	}
	if ep.Raw {
		fmt.Fprintf(buf, "\t\tRawHandler: func(w http.ResponseWriter, req *http.Request) {\n")
		if ep.Receiver != nil && ss != nil {
			fmt.Fprintf(buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
			buf.WriteString("\t\t\tif err != nil {\n\t\t\t\tpanic(err)\n\t\t\t}\n")
			fmt.Fprintf(buf, "\t\t\tsvc.%s(w, req)\n", ep.ImplName)
		} else {
			fmt.Fprintf(buf, "\t\t\t%s(w, req)\n", ep.ImplName)
		}
		buf.WriteString("\t\t},\n")
	} else {
		fmt.Fprintf(buf, "\t\tInvoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {\n")
		call := renderInvokeCall(im, ep, ss)
		buf.WriteString(call)
		buf.WriteString("\t\t},\n")
		if ep.Access == runtimeapi.Public && ep.Payload != nil {
			fmt.Fprintf(buf, "\t\tWireInvoke: func(ctx context.Context, pathArgs []any, payloadJSON []byte) (any, error) {\n")
			wireCall := renderWireInvokeCall(im, ep, ss)
			buf.WriteString(wireCall)
			buf.WriteString("\t\t},\n")
			if ep.Response != nil {
				fmt.Fprintf(buf, "\t\tWireInvokeJSON: func(ctx context.Context, pathArgs []any, payloadJSON []byte) ([]byte, error) {\n")
				wireJSONCall := renderWireInvokeJSONCall(im, ep, ss)
				buf.WriteString(wireJSONCall)
				buf.WriteString("\t\t},\n")
			}
		}
	}
	buf.WriteString("\t})\n")
}

func writeGeneratedModelEndpointRegistration(buf *strings.Builder, ep *model.GeneratedModelEndpoint) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ep.Service.Name)
	fmt.Fprintf(buf, "\t\tName: %q,\n", ep.Name)
	fmt.Fprintf(buf, "\t\tAccess: sceneryruntime.%s,\n", exportAccess(ep.Access))
	buf.WriteString("\t\tRaw: false,\n")
	fmt.Fprintf(buf, "\t\tPath: %q,\n", ep.Path)
	fmt.Fprintf(buf, "\t\tMethods: %s,\n", renderMethodLiteral(ep.Methods))
	fmt.Fprintf(buf, "\t\tPathParams: %s,\n", renderParamSpecs(ep.PathParams))
	switch ep.Action {
	case model.EntityCRUDCreate:
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", generatedModelCreateType(ep.Entity))
	case model.EntityCRUDUpdate:
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", generatedModelPatchType(ep.Entity))
	default:
		buf.WriteString("\t\tPayloadType: nil,\n")
	}
	switch ep.Action {
	case model.EntityCRUDDelete:
		buf.WriteString("\t\tResponseType: nil,\n")
	case model.EntityCRUDList:
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[[]%s](),\n", ep.Entity.Name)
	default:
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[%s](),\n", ep.Entity.Name)
	}
	buf.WriteString("\t\tWireAvailable: false,\n")
	buf.WriteString("\t\tWireUnsupportedReason: \"generated model endpoints do not publish wire contracts yet\",\n")
	buf.WriteString("\t\tInvoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {\n")
	switch ep.Action {
	case model.EntityCRUDList:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelList%s(ctx)\n", ep.Entity.Name)
	case model.EntityCRUDGet:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelGet%s(ctx, pathArgs[0])\n", ep.Entity.Name)
	case model.EntityCRUDCreate:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelCreate%s(ctx, payload.(%s))\n", ep.Entity.Name, generatedModelCreateType(ep.Entity))
	case model.EntityCRUDUpdate:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelUpdate%s(ctx, pathArgs[0], payload.(%s))\n", ep.Entity.Name, generatedModelPatchType(ep.Entity))
	case model.EntityCRUDDelete:
		fmt.Fprintf(buf, "\t\t\tif err := sceneryModelDelete%s(ctx, pathArgs[0]); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n\t\t\treturn nil, nil\n", ep.Entity.Name)
	}
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func writeMiddlewareRegistration(buf *strings.Builder, im *imports, mw *model.Middleware, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterMiddleware(&sceneryruntime.Middleware{\n")
	fmt.Fprintf(buf, "\t\tID: %q,\n", middlewareID(mw))
	buf.WriteString("\t\tInvoke: func(req scenerymiddleware.Request, next scenerymiddleware.Next) scenerymiddleware.Response {\n")
	callTarget := mw.Name
	if mw.Receiver != nil && ss != nil {
		fmt.Fprintf(buf, "\t\t\tservice, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn scenerymiddleware.Response{Err: err}\n\t\t\t}\n")
		callTarget = "service." + mw.Name
	}
	fmt.Fprintf(buf, "\t\t\treturn %s(req, next)\n", callTarget)
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func hasSecretsVar(pkg *model.Package) bool {
	for _, file := range pkg.Files {
		for _, decl := range file.AST.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "secrets" {
					continue
				}
				if _, ok := value.Type.(*ast.StructType); ok {
					return true
				}
				if len(value.Values) != 1 {
					continue
				}
				lit, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if _, ok := lit.Type.(*ast.StructType); ok {
					return true
				}
			}
		}
	}
	return false
}

func writeAuthRegistration(buf *strings.Builder, im *imports, ah *model.AuthHandler, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterAuthHandler(&sceneryruntime.AuthHandler{\n")
	fmt.Fprintf(buf, "\t\tName: %q,\n", ah.Name)
	fmt.Fprintf(buf, "\t\tService: %q,\n", ah.Service.Name)
	fmt.Fprintf(buf, "\t\tParamType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ah.Param.Type))
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\tAuthDataType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ah.AuthData.Type))
	} else {
		buf.WriteString("\t\tAuthDataType: nil,\n")
	}
	buf.WriteString("\t\tAuthenticate: func(ctx context.Context, param any) (sceneryruntime.AuthInfo, error) {\n")
	callTarget := ah.Name
	if ah.Receiver != nil && ss != nil {
		fmt.Fprintf(buf, "\t\t\tservice, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		callTarget = "service." + ah.Name
	}
	argExpr := "param.(" + im.typeExpr(ah.Param.Type) + ")"
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\t\tuid, data, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn sceneryruntime.AuthInfo{UID: string(uid), Data: data}, nil\n")
	} else {
		fmt.Fprintf(buf, "\t\t\tuid, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn sceneryruntime.AuthInfo{UID: string(uid)}, nil\n")
	}
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func renderInvokeCall(im *imports, ep *model.Endpoint, ss *model.ServiceStruct) string {
	var buf strings.Builder
	target := ep.ImplName
	if ep.Receiver != nil && ss != nil {
		fmt.Fprintf(&buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		target = "svc." + ep.ImplName
	}

	args := []string{"ctx"}
	for i, path := range ep.PathParams {
		_ = path
		field := ep.Params[i+1]
		args = append(args, fmt.Sprintf("pathArgs[%d].(%s)", i, im.typeExpr(field.Type)))
	}
	if ep.Payload != nil {
		args = append(args, fmt.Sprintf("payload.(%s)", im.typeExpr(ep.Payload.Type)))
	}

	if ep.Response != nil {
		fmt.Fprintf(&buf, "\t\t\tresp, err := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn resp, nil\n")
	} else {
		fmt.Fprintf(&buf, "\t\t\tcallErr := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif callErr != nil {\n\t\t\t\treturn nil, callErr\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn nil, nil\n")
	}
	return buf.String()
}

func renderWireInvokeCall(im *imports, ep *model.Endpoint, ss *model.ServiceStruct) string {
	var buf strings.Builder
	target := ep.ImplName
	if ep.Receiver != nil && ss != nil {
		fmt.Fprintf(&buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		target = "svc." + ep.ImplName
	}

	args := []string{"ctx"}
	for i, path := range ep.PathParams {
		_ = path
		field := ep.Params[i+1]
		args = append(args, fmt.Sprintf("pathArgs[%d].(%s)", i, im.typeExpr(field.Type)))
	}
	if ep.Payload != nil {
		jsonPkg := im.use("json", "encoding/json")
		payloadType := im.typeExpr(ep.Payload.Type)
		buf.WriteString("\t\t\tvar payload " + payloadType + "\n")
		buf.WriteString("\t\t\tif len(payloadJSON) != 0 {\n")
		fmt.Fprintf(&buf, "\t\t\t\tif err := %s.Unmarshal(payloadJSON, &payload); err != nil {\n", jsonPkg)
		buf.WriteString("\t\t\t\t\treturn nil, err\n")
		buf.WriteString("\t\t\t\t}\n")
		buf.WriteString("\t\t\t}\n")
		buf.WriteString("\t\t\tsceneryruntime.SetCurrentRequestPayload(ctx, payload)\n")
		args = append(args, "payload")
	}

	if ep.Response != nil {
		fmt.Fprintf(&buf, "\t\t\tresp, err := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn resp, nil\n")
	} else {
		fmt.Fprintf(&buf, "\t\t\tcallErr := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif callErr != nil {\n\t\t\t\treturn nil, callErr\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn nil, nil\n")
	}
	return buf.String()
}

func renderWireInvokeJSONCall(im *imports, ep *model.Endpoint, ss *model.ServiceStruct) string {
	var buf strings.Builder
	jsonPkg := im.use("json", "encoding/json")
	target := ep.ImplName
	if ep.Receiver != nil && ss != nil {
		fmt.Fprintf(&buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		target = "svc." + ep.ImplName
	}

	args := []string{"ctx"}
	for i, path := range ep.PathParams {
		_ = path
		field := ep.Params[i+1]
		args = append(args, fmt.Sprintf("pathArgs[%d].(%s)", i, im.typeExpr(field.Type)))
	}
	if ep.Payload != nil {
		payloadType := im.typeExpr(ep.Payload.Type)
		buf.WriteString("\t\t\tvar payload " + payloadType + "\n")
		buf.WriteString("\t\t\tif len(payloadJSON) != 0 {\n")
		fmt.Fprintf(&buf, "\t\t\t\tif err := %s.Unmarshal(payloadJSON, &payload); err != nil {\n", jsonPkg)
		buf.WriteString("\t\t\t\t\treturn nil, err\n")
		buf.WriteString("\t\t\t\t}\n")
		buf.WriteString("\t\t\t}\n")
		buf.WriteString("\t\t\tsceneryruntime.SetCurrentRequestPayload(ctx, payload)\n")
		args = append(args, "payload")
	}

	fmt.Fprintf(&buf, "\t\t\tresp, err := %s(%s)\n", target, strings.Join(args, ", "))
	buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
	fmt.Fprintf(&buf, "\t\t\treturn %s.Marshal(resp)\n", jsonPkg)
	return buf.String()
}

func renderParams(im *imports, fields []model.Field) string {
	parts := make([]string, 0, len(fields))
	for i, field := range fields {
		parts = append(parts, generatedFieldName(field, i)+" "+im.typeExpr(field.Type))
	}
	return strings.Join(parts, ", ")
}

func renderResults(im *imports, fields []model.Field) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, im.typeExpr(field.Type))
	}
	if len(parts) == 1 {
		return " " + parts[0]
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func joinParamNames(fields []model.Field) string {
	names := make([]string, 0, len(fields))
	for i, field := range fields {
		names = append(names, generatedFieldName(field, i))
	}
	return strings.Join(names, ", ")
}

func generatedFieldName(field model.Field, index int) string {
	if field.Name == "" || field.Name == "_" {
		return fmt.Sprintf("sceneryArg%d", index)
	}
	return field.Name
}

func exportAccess(access runtimeapi.Access) string {
	switch access {
	case runtimeapi.Public:
		return "Public"
	case runtimeapi.Auth:
		return "Auth"
	default:
		return "Private"
	}
}

func renderMethodLiteral(methods []string) string {
	if len(methods) == 0 {
		return "nil"
	}
	quoted := make([]string, 0, len(methods))
	for _, method := range methods {
		quoted = append(quoted, fmt.Sprintf("%q", method))
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

func renderMiddlewareIDs(middlewares []*model.Middleware) string {
	if len(middlewares) == 0 {
		return "nil"
	}
	ids := make([]string, 0, len(middlewares))
	for _, mw := range middlewares {
		ids = append(ids, fmt.Sprintf("%q", middlewareID(mw)))
	}
	return "[]string{" + strings.Join(ids, ", ") + "}"
}

func middlewareID(mw *model.Middleware) string {
	return mw.Package.ImportPath + "." + mw.Name
}

func renderParamSpecs(params []model.Param) string {
	if len(params) == 0 {
		return "nil"
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, fmt.Sprintf("sceneryruntime.ParamSpec{Name: %q, Kind: sceneryruntime.%s}", param.Name, exportParamKind(param.Kind)))
	}
	return "[]sceneryruntime.ParamSpec{" + strings.Join(parts, ", ") + "}"
}

func exportParamKind(kind runtimeapi.ParamKind) string {
	switch kind {
	case runtimeapi.ParamString:
		return "ParamString"
	case runtimeapi.ParamBool:
		return "ParamBool"
	case runtimeapi.ParamInt:
		return "ParamInt"
	case runtimeapi.ParamInt8:
		return "ParamInt8"
	case runtimeapi.ParamInt16:
		return "ParamInt16"
	case runtimeapi.ParamInt32:
		return "ParamInt32"
	case runtimeapi.ParamInt64:
		return "ParamInt64"
	case runtimeapi.ParamUint:
		return "ParamUint"
	case runtimeapi.ParamUint8:
		return "ParamUint8"
	case runtimeapi.ParamUint16:
		return "ParamUint16"
	case runtimeapi.ParamUint32:
		return "ParamUint32"
	case runtimeapi.ParamUint64:
		return "ParamUint64"
	default:
		return "ParamString"
	}
}

type imports struct {
	current string
	entries map[string]string
	aliases map[string]string
}

type importEntry struct {
	alias string
	path  string
}

func newImports(current string) *imports {
	return &imports{
		current: current,
		entries: make(map[string]string),
		aliases: make(map[string]string),
	}
}

func (im *imports) use(alias, path string) string {
	if existing, ok := im.entries[alias]; ok && existing == path {
		return alias
	}
	if existing, ok := im.aliases[path]; ok {
		return existing
	}
	base := alias
	if base == "" {
		base = pathBase(path)
	}
	final := base
	for i := 2; ; i++ {
		if existing, ok := im.entries[final]; !ok || existing == path {
			break
		}
		final = fmt.Sprintf("%s%d", base, i)
	}
	im.entries[final] = path
	im.aliases[path] = final
	return final
}

func (im *imports) typeExpr(t types.Type) string {
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil || pkg.Path() == im.current {
			return ""
		}
		return im.use(pkg.Name(), pkg.Path())
	})
}

func (im *imports) sorted() []importEntry {
	items := make([]importEntry, 0, len(im.entries))
	for alias, path := range im.entries {
		items = append(items, importEntry{alias: alias, path: path})
	}
	slices.SortFunc(items, func(a, b importEntry) int {
		return strings.Compare(a.path, b.path)
	})
	return items
}

func pathBase(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
