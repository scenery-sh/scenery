package model

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"

	pulseruntime "pulse.dev/runtime"
)

type App struct {
	Name       string
	Root       string
	ModulePath string
	Packages   []*Package
	Services   []*Service
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
}

type Package struct {
	GoPkg      *packages.Package
	ImportPath string
	Name       string
	AbsDir     string
	RelDir     string
	Files      []*File
	Service    *Service
}

type File struct {
	Path string
	AST  *ast.File
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
	Kind pulseruntime.ParamKind
}

type Endpoint struct {
	Service      *Service
	Package      *Package
	File         *File
	Name         string
	ImplName     string
	Access       pulseruntime.Access
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
	TokenPos     token.Pos
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
	Decl        *ast.GenDecl
	TypeSpec    *ast.TypeSpec
	GetterName  string
	InstanceVar string
}
