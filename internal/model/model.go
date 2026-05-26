package model

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/pbrazdil/onlava/internal/runtimeapi"
)

type App struct {
	Name       string
	Root       string
	ModulePath string
	Packages   []*Package
	Services   []*Service
	Middleware []*Middleware
	Runtime    []*RuntimeDeclaration
}

type Service struct {
	Name        string
	RootRelDir  string
	RootAbsDir  string
	RootPackage *Package
	Packages    []*Package
	Struct      *ServiceStruct
	Endpoints   []*Endpoint
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

type RuntimeDeclarationKind string

const (
	RuntimeDeclarationTemporalWorkflow RuntimeDeclarationKind = "temporal_workflow"
	RuntimeDeclarationTemporalActivity RuntimeDeclarationKind = "temporal_activity"
	RuntimeDeclarationCronJob          RuntimeDeclarationKind = "cron_job"
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
	TokenPos     token.Pos
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
