package model

import (
	"go/ast"
	"go/token"
	"go/types"
)

// App is the Go analysis snapshot used to verify and build an edition-2027 app.
// Application resources come exclusively from .scn source.
type App struct {
	Name       string
	Root       string
	ModulePath string
	Packages   []*Package
}

type Package struct {
	Analysis   *PackageAnalysis
	ImportPath string
	Name       string
	AbsDir     string
	RelDir     string
	Files      []*File
}

type PackageAnalysis struct {
	Fset      *token.FileSet
	Types     *types.Package
	TypesInfo *types.Info
}

type File struct {
	Path string
	AST  *ast.File
}
