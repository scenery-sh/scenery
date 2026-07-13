package model

import "go/types"

// App is the Go analysis snapshot used to verify and build a Scenery app.
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
	RelDir     string
}

type PackageAnalysis struct {
	Types *types.Package
}
