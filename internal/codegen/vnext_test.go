package codegen

import (
	"go/ast"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func TestGenerateMainRegistersEdition2027CompositionExplicitly(t *testing.T) {
	source, err := generateMain(&model.App{Name: "demo"}, appcfg.Config{}, Options{CompositionImport: "example.test/internal/scenerygen/composition"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, fragment := range []string{
		`scenerycomposition "example.test/internal/scenerygen/composition"`,
		"sceneryruntime.NewContractRegistry",
		"sceneryruntime.ContractProviderABIs()",
		"scenerycomposition.Register(contractRegistry)",
		"contractRegistry.Seal()",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("generated main missing %q:\n%s", fragment, text)
		}
	}
}

func TestRewriteEndpointDeclsSkipsNativeService(t *testing.T) {
	declaration := &ast.FuncDecl{Name: ast.NewIdent("Process")}
	legacyDeclaration := &ast.FuncDecl{Name: ast.NewIdent("Legacy")}
	app := &model.App{Services: []*model.Service{
		{Name: "house", Endpoints: []*model.Endpoint{{Decl: declaration, ImplName: "sceneryProcess"}}},
		{Name: "audit", Endpoints: []*model.Endpoint{{Decl: legacyDeclaration, ImplName: "sceneryLegacy"}}},
	}}
	restore := rewriteEndpointDecls(app, map[string]bool{"house": true}, nil)
	defer restore()
	if declaration.Name.Name != "Process" {
		t.Fatalf("native declaration renamed to %q", declaration.Name.Name)
	}
	if legacyDeclaration.Name.Name != "sceneryLegacy" {
		t.Fatalf("legacy declaration = %q", legacyDeclaration.Name.Name)
	}
}
