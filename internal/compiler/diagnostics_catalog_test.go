package compiler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var diagnosticCodePattern = regexp.MustCompile(`^SCN[0-9]{4}$`)

func TestDiagnosticCatalogDeclaresEveryEmittedCodeOnce(t *testing.T) {
	definitions := DiagnosticDefinitions()
	declared := map[string]DiagnosticDefinition{}
	identities := map[string]string{}
	for _, definition := range definitions {
		if !diagnosticCodePattern.MatchString(definition.Code) || definition.Category == "" || definition.Identity == "" || definition.Meaning == "" || definition.DefaultSeverity == "" || definition.Documentation == "" {
			t.Errorf("incomplete diagnostic definition %#v", definition)
		}
		if previous, exists := declared[definition.Code]; exists {
			if previous.Identity != definition.Identity {
				t.Errorf("%s has conflicting identities %q and %q", definition.Code, previous.Identity, definition.Identity)
			} else {
				t.Errorf("%s is declared more than once", definition.Code)
			}
		}
		if previous := identities[definition.Identity]; previous != "" && previous != definition.Code {
			t.Errorf("identity %q is shared by %s and %s", definition.Identity, previous, definition.Code)
		}
		declared[definition.Code] = definition
		identities[definition.Identity] = definition.Code
	}

	for _, root := range []string{".", filepath.Join("..", "..", "cmd", "scenery")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "dashboard_static" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil && diagnosticCodePattern.MatchString(value) {
					if _, ok := declared[value]; !ok {
						t.Errorf("%s emits undeclared diagnostic %s", path, value)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiagnosticCatalogSeparatesReviewedMeanings(t *testing.T) {
	wants := map[string]string{
		"SCN1011": "source_encoding",
		"SCN1012": "comment_syntax",
		"SCN1013": "source_identifier",
		"SCN1016": "authored_label_count",
		"SCN1017": "authored_attribute_shape",
		"SCN1018": "authored_child_block",
		"SCN1020": "authored_attribute_constraint",
		"SCN8001": "invalid_request",
		"SCN8002": "revision_conflict",
		"SCN8003": "failed_precondition",
		"SCN8004": "capability_unavailable",
		"SCN8005": "permission_denied",
		"SCN9000": "internal_tooling_failure",
	}
	for code, identity := range wants {
		definition, ok := DiagnosticDefinitionFor(code)
		if !ok || definition.Identity != identity {
			t.Errorf("definition %s = %#v, want identity %q", code, definition, identity)
		}
	}
}

func TestDiagnosticCatalogRejectsOneCodeWithDivergentIdentities(t *testing.T) {
	deferred := false
	func() {
		defer func() { deferred = recover() != nil }()
		parseDiagnosticDefinitions("SCN8001|invalid_request|Invalid request\nSCN8001|other_meaning|Other meaning")
	}()
	if !deferred {
		t.Fatal("catalog accepted one code with divergent identities")
	}
}

func TestDiagnosticCategoriesCoverEveryDeclaredRange(t *testing.T) {
	wants := map[string]string{
		"SCN1000": "syntax", "SCN1100": "identity", "SCN1200": "types_and_evaluation",
		"SCN2000": "operation_and_http_binding", "SCN2200": "execution_and_delivery", "SCN2400": "binding_and_cli",
		"SCN2500": "data", "SCN2600": "ui", "SCN2700": "events", "SCN2800": "deployment", "SCN2900": "patches",
		"SCN3000": "packages_modules_and_registry", "SCN3200": "providers_entities_and_extensions", "SCN3400": "go_configuration",
		"SCN4000": "security_and_secret_flow", "SCN4200": "runtime_policy",
		"SCN6000": "go_implementation_abi", "SCN6200": "go_generation_and_verification", "SCN6300": "typescript_generation",
		"SCN6400": "compatibility", "SCN7000": "profile_conformance", "SCN8000": "request_protocol", "SCN9000": "internal",
	}
	for code, want := range wants {
		if got := diagnosticCategory(code); got != want {
			t.Errorf("diagnosticCategory(%q) = %q, want %q", code, got, want)
		}
	}
}
