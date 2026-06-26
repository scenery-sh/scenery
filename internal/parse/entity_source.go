package parse

import (
	"fmt"
	"go/ast"
	"strings"

	"scenery.sh/internal/model"
)

func validateExistingEntitySource(pkg *model.Package, spec *ast.TypeSpec, entity *model.Entity) []string {
	if !model.EntityIsExistingSource(entity) {
		return nil
	}
	var errs []string
	if strings.TrimSpace(entity.Source.Schema) == "" {
		errs = append(errs, sourceDiagnostic(pkg, spec.Pos(), fmt.Sprintf("model.ExistingTable for %s requires a non-empty schema", entity.Name)))
	}
	if strings.TrimSpace(entity.Table) == "" {
		errs = append(errs, sourceDiagnostic(pkg, spec.Pos(), fmt.Sprintf("model.ExistingTable for %s requires a non-empty table", entity.Name)))
	}
	if len(entity.Seeds) > 0 {
		errs = append(errs, sourceDiagnostic(pkg, spec.Pos(), fmt.Sprintf("model %s uses model.ExistingTable and cannot declare model.Seed rows", entity.Name)))
	}
	for _, action := range entity.CRUD.Actions {
		switch action {
		case model.EntityCRUDList, model.EntityCRUDGet:
		default:
			errs = append(errs, sourceDiagnostic(pkg, spec.Pos(), fmt.Sprintf("model %s uses model.ExistingTable and cannot generate %s yet; use a handwritten endpoint or wait for mutation adoption", entity.Name, action)))
		}
	}
	return errs
}
