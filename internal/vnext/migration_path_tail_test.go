package vnext

import (
	"go/types"
	"strings"
	"testing"

	"scenery.sh/internal/model"
)

func TestMigrationCandidateLowersSupportedTerminalWildcard(t *testing.T) {
	endpoint := &model.Endpoint{
		Name: "Download", Path: "/drive/*path", Methods: []string{"GET"},
		Params:     []model.Field{{Name: "ctx"}, {Name: "path", Type: types.Typ[types.String], TypeExpr: "string"}},
		PathParams: []model.Param{{Name: "path"}},
	}
	deleteEndpoint := *endpoint
	deleteEndpoint.Name = "Delete"
	deleteEndpoint.Methods = []string{"DELETE"}
	service := &model.Service{Name: "drive", Endpoints: []*model.Endpoint{endpoint, &deleteEndpoint}}
	operations, diagnostics := migrationCandidateOperations(service, true)
	if len(operations) != 2 || hasDiagnostic(diagnostics, "SCN5401") || !hasDiagnostic(diagnostics, "SCN5405") {
		t.Fatalf("supported terminal wildcard operations=%#v diagnostics=%#v", operations, diagnostics)
	}
	operation := operations[1]
	if operation.Name != "download" {
		operation = operations[0]
	}
	if operation.Path != "/drive/{path...}" || len(operation.Input) != 1 || operation.Input[0].Source != "path_tail" || operation.Input[0].Type != "string" {
		t.Fatalf("lowered operation = %#v", operation)
	}
	var source strings.Builder
	renderMigrationCandidateHTTPBinding(&source, operation, "download_http", "GET")
	if !strings.Contains(source.String(), `"/drive/{path...}"`) || !strings.Contains(source.String(), `path_tail "path"`) {
		t.Fatalf("candidate source omitted native path tail:\n%s", source.String())
	}

	operations, diagnostics = migrationCandidateOperations(service, false)
	if len(operations) != 0 || !hasDiagnostic(diagnostics, "SCN5401") {
		t.Fatalf("inactive profiles did not retain legacy ownership: operations=%#v diagnostics=%#v", operations, diagnostics)
	}
}

func TestMigrationCandidateKeepsIndependentRawAndUnsupportedWildcardDiagnostics(t *testing.T) {
	service := &model.Service{Name: "drive", Endpoints: []*model.Endpoint{
		{Name: "RawDownload", Path: "/drive/*path", Raw: true, Methods: []string{"GET"}},
		{Name: "MiddleWildcard", Path: "/drive/*path/meta", Methods: []string{"GET"}},
	}}
	operations, diagnostics := migrationCandidateOperations(service, true)
	if len(operations) != 0 {
		t.Fatalf("unsupported operations were lowered: %#v", operations)
	}
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "SCN5401" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("independent rewrite diagnostics = %#v", diagnostics)
	}
}
