package wiremodel

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/wire"
)

func AppCapabilities(app *model.App) wire.Capabilities {
	endpoints := AppEndpoints(app)
	return wire.NewCapabilities(wire.HashEndpoints(endpoints), endpoints)
}

func AppEndpoints(app *model.App) []wire.Endpoint {
	if app == nil {
		return nil
	}
	var endpoints []wire.Endpoint
	for _, svc := range app.Services {
		if svc == nil {
			continue
		}
		for _, ep := range svc.Endpoints {
			endpoints = append(endpoints, Endpoint(ep))
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].ID < endpoints[j].ID
	})
	return endpoints
}

func Endpoint(ep *model.Endpoint) wire.Endpoint {
	if ep == nil || ep.Service == nil {
		return wire.Endpoint{}
	}
	item := wire.Endpoint{
		ID:                  wire.EndpointID(ep.Service.Name, ep.Name),
		Service:             ep.Service.Name,
		Endpoint:            ep.Name,
		Path:                ep.Path,
		Methods:             append([]string(nil), ep.Methods...),
		WirePath:            wire.CallPathPrefix + wire.EndpointID(ep.Service.Name, ep.Name),
		RecoveryPathPattern: wire.RecoverPathPrefix + "{call_id}",
		SafeJSONRetry:       allMethodsSafe(ep.Methods),
	}
	available, reason := endpointAvailable(ep)
	item.Available = available
	item.UnsupportedReason = reason
	if available {
		item.SchemaHash = endpointSchemaHash(ep)
	}
	return item
}

func endpointAvailable(ep *model.Endpoint) (bool, string) {
	switch {
	case ep.Raw:
		return false, "raw endpoint"
	case ep.Access == runtimeapi.Private:
		return false, "private endpoint"
	case !supportedType(ep.Payload):
		return false, "unsupported request type"
	case !supportedType(ep.Response):
		return false, "unsupported response type"
	default:
		return true, ""
	}
}

func endpointSchemaHash(ep *model.Endpoint) string {
	parts := []string{
		ep.Service.Name,
		ep.Name,
		ep.Path,
		strings.Join(ep.Methods, ","),
		string(ep.Access),
	}
	for _, param := range ep.PathParams {
		parts = append(parts, "path", param.Name, string(param.Kind))
	}
	if ep.Payload != nil {
		parts = append(parts, "payload", typeFingerprint(ep.Payload.Type))
	}
	if ep.Response != nil {
		parts = append(parts, "response", typeFingerprint(ep.Response.Type))
	}
	return wire.HashParts(parts...)
}

func supportedType(field *model.Field) bool {
	if field == nil || field.Type == nil {
		return true
	}
	seen := make(map[types.Type]bool)
	return supportedGoType(field.Type, seen)
}

func supportedGoType(typ types.Type, seen map[types.Type]bool) bool {
	if typ == nil {
		return true
	}
	if seen[typ] {
		return false
	}
	switch value := typ.(type) {
	case *types.Pointer:
		return supportedGoType(value.Elem(), seen)
	case *types.Alias:
		return supportedGoType(types.Unalias(value), seen)
	case *types.Named:
		if isSpecialSupportedNamed(value) {
			return true
		}
		if hasJSONMethod(value) {
			return false
		}
		seen[typ] = true
		ok := supportedGoType(value.Underlying(), seen)
		delete(seen, typ)
		return ok
	case *types.Basic:
		info := value.Info()
		return info&types.IsBoolean != 0 ||
			info&types.IsString != 0 ||
			info&types.IsInteger != 0 ||
			info&types.IsFloat != 0
	case *types.Slice:
		return supportedGoType(value.Elem(), seen)
	case *types.Array:
		return supportedGoType(value.Elem(), seen)
	case *types.Map:
		key, ok := value.Key().(*types.Basic)
		return ok && key.Info()&types.IsString != 0 && supportedGoType(value.Elem(), seen)
	case *types.Struct:
		for i := 0; i < value.NumFields(); i++ {
			field := value.Field(i)
			if !field.Exported() {
				continue
			}
			if field.Embedded() && !hasExplicitJSONName(value.Tag(i)) {
				return false
			}
			if !supportedGoType(field.Type(), seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func hasJSONMethod(named *types.Named) bool {
	for _, typ := range []types.Type{named, types.NewPointer(named)} {
		methods := types.NewMethodSet(typ)
		for method := range methods.Methods() {
			name := method.Obj().Name()
			if name == "MarshalJSON" || name == "UnmarshalJSON" {
				return true
			}
		}
	}
	return false
}

func isSpecialSupportedNamed(named *types.Named) bool {
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	switch obj.Pkg().Path() {
	case "time":
		return obj.Name() == "Time" || obj.Name() == "Duration"
	case "encoding/json":
		return obj.Name() == "RawMessage" || obj.Name() == "Number"
	default:
		return false
	}
}

func typeFingerprint(typ types.Type) string {
	if typ == nil {
		return ""
	}
	return canonicalType(typ, make(map[types.Type]bool))
}

func canonicalType(typ types.Type, seen map[types.Type]bool) string {
	if typ == nil {
		return "nil"
	}
	if seen[typ] {
		return "cycle"
	}
	switch value := typ.(type) {
	case *types.Pointer:
		return "*" + canonicalType(value.Elem(), seen)
	case *types.Alias:
		return "alias(" + canonicalType(types.Unalias(value), seen) + ")"
	case *types.Named:
		if obj := value.Obj(); obj != nil {
			name := obj.Name()
			if obj.Pkg() != nil {
				name = obj.Pkg().Path() + "." + name
			}
			seen[typ] = true
			underlying := canonicalType(value.Underlying(), seen)
			delete(seen, typ)
			return "named(" + name + ":" + underlying + ")"
		}
		return canonicalType(value.Underlying(), seen)
	case *types.Basic:
		return value.Name()
	case *types.Slice:
		return "[]" + canonicalType(value.Elem(), seen)
	case *types.Array:
		return fmt.Sprintf("[%d]%s", value.Len(), canonicalType(value.Elem(), seen))
	case *types.Map:
		return "map[" + canonicalType(value.Key(), seen) + "]" + canonicalType(value.Elem(), seen)
	case *types.Struct:
		var fields []string
		for i := 0; i < value.NumFields(); i++ {
			field := value.Field(i)
			if !field.Exported() {
				continue
			}
			fields = append(fields, field.Name()+"`"+value.Tag(i)+"`:"+canonicalType(field.Type(), seen))
		}
		return "struct{" + strings.Join(fields, ";") + "}"
	default:
		return typ.String()
	}
}

func hasExplicitJSONName(tag string) bool {
	raw, ok := reflect.StructTag(tag).Lookup("json")
	if !ok {
		return false
	}
	name := strings.Split(raw, ",")[0]
	return name != "" && name != "-"
}

func allMethodsSafe(methods []string) bool {
	if len(methods) == 0 {
		return false
	}
	for _, method := range methods {
		if !wire.IsSafeMethod(method) {
			return false
		}
	}
	return true
}
