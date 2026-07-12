package codegen

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path/filepath"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

type Output struct {
	Rewritten map[string][]byte
	Generated map[string][]byte
}

type Options struct {
	CompositionImport string
}

func GenerateWithOptions(appModel *model.App, cfg appcfg.Config, options Options) (*Output, error) {
	out := &Output{Rewritten: map[string][]byte{}, Generated: map[string][]byte{}}
	for _, pkg := range appModel.Packages {
		if !hasSecretsVar(pkg) {
			continue
		}
		data, err := generateEarlyConfigFile(pkg)
		if err != nil {
			return nil, err
		}
		relative := filepath.ToSlash(filepath.Join(pkg.RelDir, "00_scenery_config.gen.go"))
		if pkg.RelDir == "." {
			relative = "00_scenery_config.gen.go"
		}
		out.Generated[relative] = data
	}
	mainFile, err := generateMain(appModel, cfg, options)
	if err != nil {
		return nil, err
	}
	out.Generated["scenery_internal_main/main.go"] = mainFile
	return out, nil
}

func generateEarlyConfigFile(pkg *model.Package) ([]byte, error) {
	source := fmt.Sprintf(`package %s

import sceneryruntime "scenery.sh/runtime"

var sceneryInternalDotEnvInitialized = sceneryruntime.MustLoadDotEnvIntoEnv()

var sceneryInternalSecretsInitialized = func() bool {
	sceneryruntime.MustPopulateSecrets(&secrets)
	return true
}()
`, pkg.Name)
	return format.Source([]byte(source))
}

func hasSecretsVar(pkg *model.Package) bool {
	for _, file := range pkg.Files {
		for _, declaration := range file.AST.Decls {
			generated, ok := declaration.(*ast.GenDecl)
			if !ok || generated.Tok != token.VAR {
				continue
			}
			for _, raw := range generated.Specs {
				value, ok := raw.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "secrets" {
					continue
				}
				if _, ok := value.Type.(*ast.StructType); ok {
					return true
				}
				if len(value.Values) != 1 {
					continue
				}
				literal, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if _, ok := literal.Type.(*ast.StructType); ok {
					return true
				}
			}
		}
	}
	return false
}
