package codegen

import (
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func TestGeneratedModelBackendUsesPostgresPlaceholdersAndServicePool(t *testing.T) {
	service := &model.Service{Name: "tasks"}
	entity := &model.Entity{
		Package: &model.Package{Service: service},
		Name:    "Task",
		Table:   "tasks",
		Fields: []model.EntityField{
			{Name: "ID", TypeExpr: "string", Column: "id"},
			{Name: "TenantID", TypeExpr: "string", Column: "tenant_id"},
			{Name: "Title", TypeExpr: "string", Column: "title"},
		},
	}
	endpoints := []*model.GeneratedModelEndpoint{{Service: service, Entity: entity}}

	var buf strings.Builder
	writeGeneratedModelBackend(&buf, newImports("example.com/app/tasks"), endpoints, appcfg.Config{})
	got := buf.String()

	for _, want := range []string{
		`return scenerydb.Get(ctx, service)`,
		`sceneryModelStorePool(ctx, "tasks")`,
		`limit $2 offset $3`,
		`where \"id\" = $1 and \"tenant_id\" = $2`,
		`values ($1, $2, $3)`,
		`sets = append(sets, "\"title\" = $" + strconv.Itoa(len(args)))`,
		`idPlaceholder := "$" + strconv.Itoa(len(args))`,
		`tenantPlaceholder := "$" + strconv.Itoa(len(args))`,
		`delete from \"tasks\" where \"id\" = $1 and \"tenant_id\" = $2`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated backend missing %q:\n%s", want, got)
		}
	}
	q := string(rune(63))
	for _, bad := range []string{` = ` + q, `limit ` + q, `offset ` + q, `values (` + q + `,`} {
		if strings.Contains(got, bad) {
			t.Fatalf("generated backend still contains %q:\n%s", bad, got)
		}
	}
}
