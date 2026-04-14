package devmeta

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"pulse.dev/internal/model"
	pulseruntime "pulse.dev/runtime"
)

func BuildMetadataSnapshot(app *model.App) (json.RawMessage, error) {
	if app == nil {
		return json.RawMessage(`{}`), nil
	}

	payload := map[string]any{
		"module_path":         app.ModulePath,
		"app_revision":        "",
		"uncommitted_changes": false,
		"decls":               []any{},
		"pkgs":                buildPackages(app),
		"svcs":                buildServices(app),
		"cron_jobs":           buildCronJobs(app),
		"middleware":          buildMiddleware(app),
		"pubsub_topics":       []any{},
		"cache_clusters":      []any{},
		"experiments":         []any{},
		"metrics":             []any{},
		"sql_databases":       []any{},
		"gateways":            []any{},
		"buckets":             []any{},
		"language":            "GO",
	}
	if auth := buildAuthHandler(app); auth != nil {
		payload["auth_handler"] = auth
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func BuildAPIEncoding(app *model.App) (json.RawMessage, error) {
	if app == nil {
		return json.RawMessage(`{}`), nil
	}
	services := make([]map[string]any, 0, len(app.Services))
	for _, svc := range app.Services {
		rpcs := make([]map[string]any, 0, len(svc.Endpoints))
		for _, ep := range svc.Endpoints {
			rpcs = append(rpcs, map[string]any{
				"name":         ep.Name,
				"path":         ep.Path,
				"methods":      ep.Methods,
				"raw":          ep.Raw,
				"access_type":  accessName(ep.Access),
				"service_name": svc.Name,
			})
		}
		services = append(services, map[string]any{
			"name": svc.Name,
			"rpcs": rpcs,
		})
	}
	data, err := json.Marshal(map[string]any{
		"services": services,
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func buildPackages(app *model.App) []map[string]any {
	pkgs := make([]map[string]any, 0, len(app.Packages))
	for _, pkg := range app.Packages {
		pkgs = append(pkgs, map[string]any{
			"rel_path":     pkg.RelDir,
			"name":         pkg.Name,
			"doc":          "",
			"service_name": serviceName(pkg.Service),
			"secrets":      collectPackageSecrets(pkg),
			"rpc_calls":    []any{},
			"trace_nodes":  buildTraceNodes(pkg),
		})
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i]["rel_path"].(string) < pkgs[j]["rel_path"].(string)
	})
	return pkgs
}

func buildServices(app *model.App) []map[string]any {
	services := make([]map[string]any, 0, len(app.Services))
	for _, svc := range app.Services {
		rpcs := make([]map[string]any, 0, len(svc.Endpoints))
		for _, ep := range svc.Endpoints {
			rpcs = append(rpcs, map[string]any{
				"name":            ep.Name,
				"doc":             "",
				"service_name":    svc.Name,
				"access_type":     accessName(ep.Access),
				"proto":           protocolName(ep.Raw),
				"loc":             buildLoc(ep.Package, ep.TokenPos, ep.TokenPos),
				"path":            buildPath(ep),
				"http_methods":    ep.Methods,
				"request_schema":  buildSchema(ep.Payload),
				"response_schema": buildSchema(ep.Response),
				"tags":            buildSelectors(ep.Tags),
			})
		}
		services = append(services, map[string]any{
			"name":       svc.Name,
			"rel_path":   svc.RootRelDir,
			"rpcs":       rpcs,
			"migrations": []any{},
			"databases":  []any{},
			"has_config": false,
			"buckets":    []any{},
			"metrics":    []any{},
		})
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i]["name"].(string) < services[j]["name"].(string)
	})
	return services
}

func buildAuthHandler(app *model.App) map[string]any {
	for _, svc := range app.Services {
		if svc.AuthHandler == nil {
			continue
		}
		return map[string]any{
			"name":      svc.AuthHandler.Name,
			"doc":       "",
			"pkg_path":  svc.AuthHandler.Package.RelDir,
			"pkg_name":  svc.AuthHandler.Package.Name,
			"loc":       buildLoc(svc.AuthHandler.Package, svc.AuthHandler.TokenPos, svc.AuthHandler.TokenPos),
			"auth_data": buildSchema(svc.AuthHandler.AuthData),
			"params":    buildSchema(&svc.AuthHandler.Param),
		}
	}
	return nil
}

func buildCronJobs(app *model.App) []map[string]any {
	var jobs []map[string]any
	for _, pkg := range app.Packages {
		for _, file := range pkg.Files {
			ast.Inspect(file.AST, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "NewJob" {
					return true
				}
				if len(call.Args) < 2 {
					return true
				}
				id, ok := stringLiteral(call.Args[0])
				if !ok {
					return true
				}
				job := map[string]any{
					"id":       id,
					"title":    id,
					"doc":      "",
					"schedule": "",
					"endpoint": map[string]any{},
				}
				if cfg, ok := call.Args[1].(*ast.CompositeLit); ok {
					for _, elt := range cfg.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						switch key.Name {
						case "Title":
							if title, ok := stringLiteral(kv.Value); ok {
								job["title"] = title
							}
						case "Schedule":
							if schedule, ok := stringLiteral(kv.Value); ok {
								job["schedule"] = schedule
							}
						case "Every":
							job["every"] = renderExpr(pkg, kv.Value)
						case "Endpoint":
							job["endpoint"] = mapEndpointRef(renderExpr(pkg, kv.Value))
						}
					}
				}
				jobs = append(jobs, job)
				return true
			})
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i]["id"].(string) < jobs[j]["id"].(string)
	})
	return jobs
}

func buildMiddleware(app *model.App) []map[string]any {
	items := make([]map[string]any, 0, len(app.Middleware))
	for _, mw := range app.Middleware {
		items = append(items, map[string]any{
			"name": map[string]any{
				"pkg":  mw.Package.RelDir,
				"name": mw.Name,
			},
			"doc":          "",
			"loc":          buildLoc(mw.Package, mw.TokenPos, mw.TokenPos),
			"global":       mw.Global,
			"service_name": serviceName(mw.Service),
			"target":       buildMiddlewareSelectors(mw.Targets),
		})
	}
	return items
}

func buildTraceNodes(pkg *model.Package) []map[string]any {
	var nodes []map[string]any
	nextID := 1
	if pkg.Service == nil {
		return nodes
	}
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package != pkg {
			continue
		}
		nodes = append(nodes, map[string]any{
			"id":             nextID,
			"filepath":       relFilePath(pkg, ep.File.Path),
			"start_pos":      posOffset(pkg, ep.TokenPos),
			"end_pos":        posOffset(pkg, ep.TokenPos),
			"src_line_start": posLine(pkg, ep.TokenPos),
			"src_line_end":   posLine(pkg, ep.TokenPos),
			"src_col_start":  posColumn(pkg, ep.TokenPos),
			"src_col_end":    posColumn(pkg, ep.TokenPos),
			"rpc_def": map[string]any{
				"service_name": ep.Service.Name,
				"rpc_name":     ep.Name,
				"context":      "api",
			},
		})
		nextID++
	}
	if pkg.Service.AuthHandler != nil && pkg.Service.AuthHandler.Package == pkg {
		nodes = append(nodes, map[string]any{
			"id":             nextID,
			"filepath":       relFilePath(pkg, pkg.Service.AuthHandler.File.Path),
			"start_pos":      posOffset(pkg, pkg.Service.AuthHandler.TokenPos),
			"end_pos":        posOffset(pkg, pkg.Service.AuthHandler.TokenPos),
			"src_line_start": posLine(pkg, pkg.Service.AuthHandler.TokenPos),
			"src_line_end":   posLine(pkg, pkg.Service.AuthHandler.TokenPos),
			"src_col_start":  posColumn(pkg, pkg.Service.AuthHandler.TokenPos),
			"src_col_end":    posColumn(pkg, pkg.Service.AuthHandler.TokenPos),
			"auth_handler_def": map[string]any{
				"service_name": pkg.Service.Name,
				"name":         pkg.Service.AuthHandler.Name,
				"context":      "auth",
			},
		})
		nextID++
	}
	if pkg.Service.Struct != nil && pkg.Service.Struct.Package == pkg {
		nodes = append(nodes, map[string]any{
			"id":             nextID,
			"filepath":       relFilePath(pkg, pkg.Service.Struct.File.Path),
			"start_pos":      posOffset(pkg, pkg.Service.Struct.Decl.Pos()),
			"end_pos":        posOffset(pkg, pkg.Service.Struct.Decl.End()),
			"src_line_start": posLine(pkg, pkg.Service.Struct.Decl.Pos()),
			"src_line_end":   posLine(pkg, pkg.Service.Struct.Decl.End()),
			"src_col_start":  posColumn(pkg, pkg.Service.Struct.Decl.Pos()),
			"src_col_end":    posColumn(pkg, pkg.Service.Struct.Decl.End()),
			"service_init": map[string]any{
				"service_name":    pkg.Service.Name,
				"setup_func_name": pkg.Service.Struct.InitFunc,
				"context":         "service_init",
			},
		})
		nextID++
	}
	for _, mw := range pkg.Service.Middleware {
		if mw.Package != pkg {
			continue
		}
		nodes = append(nodes, map[string]any{
			"id":             nextID,
			"filepath":       relFilePath(pkg, mw.File.Path),
			"start_pos":      posOffset(pkg, mw.TokenPos),
			"end_pos":        posOffset(pkg, mw.TokenPos),
			"src_line_start": posLine(pkg, mw.TokenPos),
			"src_line_end":   posLine(pkg, mw.TokenPos),
			"src_col_start":  posColumn(pkg, mw.TokenPos),
			"src_col_end":    posColumn(pkg, mw.TokenPos),
			"middleware_def": map[string]any{
				"pkg_rel_path": pkg.RelDir,
				"name":         mw.Name,
				"context":      "middleware",
				"target":       buildMiddlewareSelectors(mw.Targets),
			},
		})
		nextID++
	}
	return nodes
}

func buildPath(ep *model.Endpoint) map[string]any {
	path := map[string]any{
		"type":     "URL",
		"segments": []map[string]any{},
	}
	parts := strings.Split(strings.TrimPrefix(ep.Path, "/"), "/")
	segments := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ":") {
			segments = append(segments, map[string]any{
				"type":       "PARAM",
				"value":      strings.TrimPrefix(part, ":"),
				"value_type": "STRING",
			})
			continue
		}
		segments = append(segments, map[string]any{
			"type":       "LITERAL",
			"value":      part,
			"value_type": "STRING",
		})
	}
	path["segments"] = segments
	return path
}

func buildSchema(field *model.Field) any {
	if field == nil || field.Type == nil {
		return nil
	}
	return buildType(field.Type)
}

func buildType(typ types.Type) map[string]any {
	switch value := typ.(type) {
	case *types.Pointer:
		return map[string]any{
			"pointer": map[string]any{
				"base": buildType(value.Elem()),
			},
		}
	case *types.Named:
		if basic, ok := builtinType(value.Underlying()); ok {
			return basic
		}
		return map[string]any{
			"named": map[string]any{
				"id":             0,
				"type_arguments": []any{},
			},
		}
	case *types.Struct:
		fields := make([]map[string]any, 0, value.NumFields())
		for i := 0; i < value.NumFields(); i++ {
			field := value.Field(i)
			tag := value.Tag(i)
			fields = append(fields, map[string]any{
				"name":              field.Name(),
				"doc":               "",
				"typ":               buildType(field.Type()),
				"json_name":         jsonName(field.Name(), tag),
				"optional":          tagContains(tag, `pulse:"optional"`),
				"query_string_name": "",
				"raw_tag":           tag,
				"tags":              parseTags(tag),
			})
		}
		return map[string]any{
			"struct": map[string]any{
				"fields": fields,
			},
		}
	case *types.Slice:
		return map[string]any{
			"list": map[string]any{
				"elem": buildType(value.Elem()),
			},
		}
	case *types.Map:
		return map[string]any{
			"map": map[string]any{
				"key":   buildType(value.Key()),
				"value": buildType(value.Elem()),
			},
		}
	default:
		if basic, ok := builtinType(typ); ok {
			return basic
		}
		return map[string]any{
			"builtin": "ANY",
		}
	}
}

func builtinType(typ types.Type) (map[string]any, bool) {
	basic, ok := typ.(*types.Basic)
	if !ok {
		return nil, false
	}
	switch basic.Kind() {
	case types.Bool:
		return map[string]any{"builtin": "BOOL"}, true
	case types.Int:
		return map[string]any{"builtin": "INT"}, true
	case types.Int8:
		return map[string]any{"builtin": "INT8"}, true
	case types.Int16:
		return map[string]any{"builtin": "INT16"}, true
	case types.Int32:
		return map[string]any{"builtin": "INT32"}, true
	case types.Int64:
		return map[string]any{"builtin": "INT64"}, true
	case types.Uint:
		return map[string]any{"builtin": "UINT"}, true
	case types.Uint8:
		return map[string]any{"builtin": "UINT8"}, true
	case types.Uint16:
		return map[string]any{"builtin": "UINT16"}, true
	case types.Uint32:
		return map[string]any{"builtin": "UINT32"}, true
	case types.Uint64:
		return map[string]any{"builtin": "UINT64"}, true
	case types.Float32:
		return map[string]any{"builtin": "FLOAT32"}, true
	case types.Float64:
		return map[string]any{"builtin": "FLOAT64"}, true
	case types.String:
		return map[string]any{"builtin": "STRING"}, true
	default:
		return map[string]any{"builtin": "ANY"}, true
	}
}

func jsonName(name, tag string) string {
	if raw, ok := reflect.StructTag(tag).Lookup("json"); ok {
		parts := strings.Split(raw, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	return name
}

func parseTags(raw string) []map[string]any {
	var tags []map[string]any
	structTag := reflect.StructTag(raw)
	for _, key := range []string{"json", "query", "qs", "header", "cookie", "pulse"} {
		value, ok := structTag.Lookup(key)
		if !ok {
			continue
		}
		parts := strings.Split(value, ",")
		item := map[string]any{
			"key":     key,
			"name":    parts[0],
			"options": []string{},
		}
		if len(parts) > 1 {
			item["options"] = parts[1:]
		}
		tags = append(tags, item)
	}
	return tags
}

func tagContains(tag, want string) bool {
	return strings.Contains(tag, want)
}

func buildSelectors(tags []string) []map[string]any {
	if len(tags) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		result = append(result, map[string]any{
			"type":  "TAG",
			"value": tag,
		})
	}
	return result
}

func buildMiddlewareSelectors(selectors []model.Selector) []map[string]any {
	items := make([]map[string]any, 0, len(selectors))
	for _, selector := range selectors {
		kind := "TAG"
		if selector.Kind == model.SelectorAll {
			kind = "ALL"
		}
		items = append(items, map[string]any{
			"type":  kind,
			"value": selector.Value,
		})
	}
	return items
}

func collectPackageSecrets(pkg *model.Package) []string {
	seen := make(map[string]bool)
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
				if structType, ok := value.Type.(*ast.StructType); ok {
					collectStructSecretFields(structType, seen)
				}
				if len(value.Values) != 1 {
					continue
				}
				lit, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if structType, ok := lit.Type.(*ast.StructType); ok {
					collectStructSecretFields(structType, seen)
				}
			}
		}
	}
	secrets := make([]string, 0, len(seen))
	for name := range seen {
		secrets = append(secrets, name)
	}
	sort.Strings(secrets)
	return secrets
}

func collectStructSecretFields(structType *ast.StructType, seen map[string]bool) {
	if structType == nil || structType.Fields == nil {
		return
	}
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			if ast.IsExported(name.Name) {
				seen[name.Name] = true
			}
		}
	}
}

func buildLoc(pkg *model.Package, start, end token.Pos) map[string]any {
	fset := pkg.GoPkg.Fset
	startPos := fset.PositionFor(start, false)
	endPos := fset.PositionFor(end, false)
	return map[string]any{
		"pkg_path":       pkg.RelDir,
		"pkg_name":       pkg.Name,
		"filename":       filepath.Base(startPos.Filename),
		"start_pos":      startPos.Offset,
		"end_pos":        endPos.Offset,
		"src_line_start": startPos.Line,
		"src_line_end":   endPos.Line,
		"src_col_start":  startPos.Column,
		"src_col_end":    endPos.Column,
	}
}

func serviceName(svc *model.Service) string {
	if svc == nil {
		return ""
	}
	return svc.Name
}

func accessName(access pulseruntime.Access) string {
	switch access {
	case pulseruntime.Public:
		return "PUBLIC"
	case pulseruntime.Auth:
		return "AUTH"
	default:
		return "PRIVATE"
	}
}

func protocolName(raw bool) string {
	if raw {
		return "RAW"
	}
	return "REGULAR"
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value := strings.Trim(lit.Value, `"`)
	return value, true
}

func renderExpr(pkg *model.Package, expr ast.Expr) string {
	var b strings.Builder
	_ = ast.Fprint(&b, pkg.GoPkg.Fset, expr, nil)
	return b.String()
}

func mapEndpointRef(expr string) map[string]any {
	service := ""
	endpoint := expr
	if parts := strings.Split(expr, "."); len(parts) >= 2 {
		service = parts[len(parts)-2]
		endpoint = parts[len(parts)-1]
	}
	return map[string]any{
		"service_name": service,
		"rpc_name":     endpoint,
	}
}

func relFilePath(pkg *model.Package, abs string) string {
	path, err := filepath.Rel(pkg.Service.RootAbsDir, abs)
	if err != nil {
		return filepath.Base(abs)
	}
	return filepath.ToSlash(path)
}

func posOffset(pkg *model.Package, pos token.Pos) int {
	return pkg.GoPkg.Fset.PositionFor(pos, false).Offset
}

func posLine(pkg *model.Package, pos token.Pos) int {
	return pkg.GoPkg.Fset.PositionFor(pos, false).Line
}

func posColumn(pkg *model.Package, pos token.Pos) int {
	return pkg.GoPkg.Fset.PositionFor(pos, false).Column
}
