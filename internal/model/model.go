package model

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"scenery.sh/internal/runtimeapi"
)

type App struct {
	Name       string
	Root       string
	ModulePath string
	Packages   []*Package
	Services   []*Service
	Middleware []*Middleware
	Runtime    []*RuntimeDeclaration
	Entities   []*Entity
	Views      []*View
}

type Service struct {
	Name        string
	RootRelDir  string
	RootAbsDir  string
	RootPackage *Package
	Packages    []*Package
	Struct      *ServiceStruct
	Endpoints   []*Endpoint
	Generated   []*GeneratedModelEndpoint
	AuthHandler *AuthHandler
	Middleware  []*Middleware
}

type Package struct {
	GoPkg      *packages.Package
	ImportPath string
	Name       string
	AbsDir     string
	RelDir     string
	Files      []*File
	Service    *Service
	Runtime    []*RuntimeDeclaration
}

type File struct {
	Path string
	AST  *ast.File
}

type Entity struct {
	Package  *Package
	File     *File
	Name     string
	TypeExpr string
	Table    string
	Fields   []EntityField
	Seeds    []EntitySeedRow
	CRUD     EntityCRUD
	TokenPos token.Pos
}

type EntitySeedRow struct {
	Values   []EntitySeedValue
	TokenPos token.Pos
}

type EntitySeedValue struct {
	Field    string
	Kind     EntitySeedValueKind
	Value    string
	TokenPos token.Pos
}

type EntitySeedValueKind string

const (
	EntitySeedString    EntitySeedValueKind = "string"
	EntitySeedInteger   EntitySeedValueKind = "integer"
	EntitySeedFloat     EntitySeedValueKind = "float"
	EntitySeedBool      EntitySeedValueKind = "bool"
	EntitySeedTimestamp EntitySeedValueKind = "timestamp"
)

type EntityCRUD struct {
	Actions   []EntityCRUDAction
	Disabled  []EntityCRUDAction
	Overrides []EntityCRUDOverride
}

type EntityCRUDAction string

const (
	EntityCRUDList   EntityCRUDAction = "list"
	EntityCRUDGet    EntityCRUDAction = "get"
	EntityCRUDCreate EntityCRUDAction = "create"
	EntityCRUDUpdate EntityCRUDAction = "update"
	EntityCRUDDelete EntityCRUDAction = "delete"
)

type EntityCRUDOverride struct {
	Action   EntityCRUDAction
	Endpoint string
}

type EntityFieldKind string

const (
	EntityFieldStored       EntityFieldKind = "stored"
	EntityFieldRelationship EntityFieldKind = "relationship"
	EntityFieldComputed     EntityFieldKind = "computed"
)

type EntityField struct {
	Name        string
	TypeExpr    string
	Type        types.Type
	Kind        EntityFieldKind
	Column      string
	EnumValues  []string
	Filterable  bool
	RenamedFrom string
}

func (e *Entity) TenantField() *EntityField {
	if e == nil {
		return nil
	}
	for i := range e.Fields {
		field := &e.Fields[i]
		if field.Kind == EntityFieldComputed {
			continue
		}
		if strings.EqualFold(field.Name, "TenantID") || strings.EqualFold(field.Column, "tenant_id") {
			return field
		}
	}
	return nil
}

func GeneratedTenantFieldKind(field EntityField) string {
	if isGeneratedTenantStringType(field.Type) {
		return "string"
	}
	if isGeneratedTenantUUIDType(field.Type) {
		return "uuid"
	}
	return ""
}

func isGeneratedTenantStringType(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if basic, ok := t.Underlying().(*types.Basic); ok && basic.Kind() == types.String {
		return true
	}
	return false
}

func isGeneratedTenantUUIDType(t types.Type) bool {
	if t == nil {
		return false
	}
	if types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	}) == "github.com/google/uuid.UUID" {
		return true
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == "github.com/google/uuid" && named.Obj().Name() == "UUID"
}

func EntityService(entity *Entity) string {
	if entity != nil && entity.Package != nil {
		if entity.Package.Service != nil && strings.TrimSpace(entity.Package.Service.Name) != "" {
			return entity.Package.Service.Name
		}
		rel := filepath.ToSlash(entity.Package.RelDir)
		if rel != "." && rel != "" {
			if first, _, ok := strings.Cut(rel, "/"); ok {
				return first
			}
			return rel
		}
	}
	return "app"
}

func EntityDatabaseSchema(entity *Entity) string {
	return safeDatabaseIdent(EntityService(entity))
}

func EntityQualifiedTable(entity *Entity) string {
	if entity == nil {
		return ""
	}
	return EntityDatabaseSchema(entity) + "." + strings.TrimSpace(entity.Table)
}

func EntityCRUDRouteBase(entity *Entity) string {
	if entity == nil {
		return "/app/model"
	}
	service := safeRouteSegment(EntityService(entity))
	if service == "" {
		service = "app"
	}
	table := safeRouteSegment(entity.Table)
	if table == "" {
		table = safeRouteSegment(entity.Name)
	}
	if table == "" {
		table = "model"
	}
	return "/" + service + "/" + table
}

func safeDatabaseIdent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "app"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "s_" + out
	}
	return out
}

func safeRouteSegment(value string) string {
	value = strings.ToLower(strings.Trim(strings.TrimSpace(value), "/"))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

type View struct {
	Package  *Package
	File     *File
	Name     string
	Kind     string
	Entity   string
	Route    string
	Title    string
	Columns  []string
	Slots    []ViewSlot
	TokenPos token.Pos
}

type ViewSlot struct {
	Name string
}

type RuntimeDeclarationKind string

const (
	RuntimeDeclarationTemporalWorkflow         RuntimeDeclarationKind = "temporal_workflow"
	RuntimeDeclarationTemporalActivity         RuntimeDeclarationKind = "temporal_activity"
	RuntimeDeclarationTemporalExternalActivity RuntimeDeclarationKind = "temporal_external_activity"
	RuntimeDeclarationCronJob                  RuntimeDeclarationKind = "cron_job"
)

type RuntimeDeclaration struct {
	Package           *Package
	File              *File
	Kind              RuntimeDeclarationKind
	Name              string
	CallName          string
	TokenPos          token.Pos
	TaskQueue         string
	TaskQueueExplicit bool
	TaskQueueResolved bool
	InputType         string
	OutputType        string
}

type Receiver struct {
	Name     string
	TypeName string
	TypeExpr string
	Pointer  bool
}

type Field struct {
	Name     string
	TypeExpr string
	Type     types.Type
}

type Param struct {
	Name string
	Kind runtimeapi.ParamKind
}

type Endpoint struct {
	Service      *Service
	Package      *Package
	File         *File
	Name         string
	ImplName     string
	Access       runtimeapi.Access
	Raw          bool
	Path         string
	PathExplicit bool
	Methods      []string
	Decl         *ast.FuncDecl
	Object       types.Object
	Receiver     *Receiver
	Params       []Field
	Results      []Field
	PathParams   []Param
	Payload      *Field
	Response     *Field
	Tags         []string
	Middleware   []*Middleware
	Generated    bool
	TokenPos     token.Pos
}

type GeneratedModelEndpoint struct {
	Service    *Service
	Package    *Package
	Entity     *Entity
	Action     EntityCRUDAction
	Name       string
	Access     runtimeapi.Access
	Path       string
	Methods    []string
	PathParams []Param
	HasPayload bool
	Generated  bool
}

type SelectorKind string

const (
	SelectorAll SelectorKind = "all"
	SelectorTag SelectorKind = "tag"
)

type Selector struct {
	Kind  SelectorKind
	Value string
}

type Middleware struct {
	Service  *Service
	Package  *Package
	File     *File
	Name     string
	Decl     *ast.FuncDecl
	Receiver *Receiver
	Global   bool
	Targets  []Selector
	TokenPos token.Pos
}

type AuthHandler struct {
	Service  *Service
	Package  *Package
	File     *File
	Name     string
	Decl     *ast.FuncDecl
	Object   types.Object
	Receiver *Receiver
	Param    Field
	AuthData *Field
	TokenPos token.Pos
}

type ServiceStruct struct {
	Service     *Service
	Package     *Package
	File        *File
	TypeName    string
	TypeExpr    string
	Receiver    Receiver
	InitFunc    string
	Shutdown    string
	Decl        *ast.GenDecl
	TypeSpec    *ast.TypeSpec
	GetterName  string
	InstanceVar string
}
