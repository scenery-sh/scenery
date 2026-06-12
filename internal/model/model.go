package model

import (
	"go/ast"
	"go/token"
	"go/types"

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
	CRUD     EntityCRUD
	TokenPos token.Pos
}

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
