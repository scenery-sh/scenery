package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"path/filepath"
	"slices"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

type Output struct {
	Rewritten map[string][]byte
	Generated map[string][]byte
}

type Options struct {
	NativeServices    map[string]bool
	BridgeServices    map[string]bool
	CompositionImport string
}

func GenerateWithOptions(appModel *model.App, cfg appcfg.Config, options Options) (*Output, error) {
	out := &Output{
		Rewritten: make(map[string][]byte),
		Generated: make(map[string][]byte),
	}

	restoreEndpointDecls := rewriteEndpointDecls(appModel, options.NativeServices, options.BridgeServices)
	defer restoreEndpointDecls()
	for _, pkg := range appModel.Packages {
		for _, file := range pkg.Files {
			rel, err := filepath.Rel(appModel.Root, file.Path)
			if err != nil {
				return nil, err
			}
			if changed := fileChanged(pkg, file); changed {
				data, err := renderFile(pkg.Analysis.Fset, file.AST)
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
		data, err := generatePackageFile(pkg, cfg, options.NativeServices, options.BridgeServices)
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

	mainFile, err := generateMain(appModel, cfg, options)
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

func rewriteEndpointDecls(app *model.App, nativeServices, _ map[string]bool) func() {
	type rewrittenDecl struct {
		ident *ast.Ident
		name  string
	}
	var rewritten []rewrittenDecl
	for _, svc := range app.Services {
		if nativeServices[svc.Name] {
			continue
		}
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

func generatePackageFile(pkg *model.Package, cfg appcfg.Config, nativeServices, _ map[string]bool) ([]byte, error) {
	if pkg.Service != nil && nativeServices[pkg.Service.Name] {
		return nil, nil
	}
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
