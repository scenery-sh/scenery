package parse

import (
	"fmt"
	"go/ast"
	"strings"

	"scenery.sh/internal/model"
)

func parsePageColumnDisplays(pkg *model.Package, aliases map[string]string, expr ast.Expr) ([]model.ViewColumnDisplay, []string) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, []string{sourceDiagnostic(pkg, expr.Pos(), "page.Collection.ColumnDisplays must be a static page.Column slice")}
	}
	var out []model.ViewColumnDisplay
	var errs []string
	for _, elt := range lit.Elts {
		call, ok := elt.(*ast.CallExpr)
		if !ok || !isPackageCall(call.Fun, aliases, "scenery.sh/page", "Column") || len(call.Args) != 2 {
			errs = append(errs, sourceDiagnostic(pkg, elt.Pos(), "page column displays must use page.Column(\"Field\", page.DisplayKind)"))
			continue
		}
		field, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), "page.Column requires a constant field name"))
			continue
		}
		kind, ok := staticStringValue(pkg, call.Args[1])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), "page.Column requires a constant display kind"))
			continue
		}
		kind = strings.TrimSpace(kind)
		switch kind {
		case "text", "datetime", "badge":
		default:
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), fmt.Sprintf("unsupported page column display kind %q", kind)))
			continue
		}
		out = append(out, model.ViewColumnDisplay{Field: strings.TrimSpace(field), Kind: kind})
	}
	return out, errs
}

func parsePageFilters(pkg *model.Package, aliases map[string]string, expr ast.Expr) ([]model.ViewFilter, []string) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, []string{sourceDiagnostic(pkg, expr.Pos(), "page.Collection.Filters must be a static page.Filter slice")}
	}
	var out []model.ViewFilter
	var errs []string
	for _, elt := range lit.Elts {
		call, ok := elt.(*ast.CallExpr)
		if !ok || !isPackageCall(call.Fun, aliases, "scenery.sh/page", "Filter") || len(call.Args) < 2 || len(call.Args) > 3 {
			errs = append(errs, sourceDiagnostic(pkg, elt.Pos(), "page filters must use page.Filter(\"Field\", page.Op[, \"value\"])"))
			continue
		}
		field, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), "page.Filter requires a constant field name"))
			continue
		}
		op, ok := staticStringValue(pkg, call.Args[1])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), "page.Filter requires a constant operator"))
			continue
		}
		op = strings.TrimSpace(op)
		switch op {
		case "eq", "neq":
			if len(call.Args) != 3 {
				errs = append(errs, sourceDiagnostic(pkg, call.Pos(), fmt.Sprintf("page.Filter operator %q requires a value", op)))
				continue
			}
		case "is_null", "is_not_null":
			if len(call.Args) != 2 {
				errs = append(errs, sourceDiagnostic(pkg, call.Args[2].Pos(), fmt.Sprintf("page.Filter operator %q does not take a value", op)))
				continue
			}
		default:
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), fmt.Sprintf("unsupported page filter operator %q", op)))
			continue
		}
		value := ""
		if len(call.Args) == 3 {
			var valueOK bool
			value, valueOK = staticStringValue(pkg, call.Args[2])
			if !valueOK {
				errs = append(errs, sourceDiagnostic(pkg, call.Args[2].Pos(), "page.Filter value must be a constant string"))
				continue
			}
		}
		out = append(out, model.ViewFilter{Field: strings.TrimSpace(field), Op: op, Value: value})
	}
	return out, errs
}

func parsePageSorts(pkg *model.Package, aliases map[string]string, expr ast.Expr) ([]model.ViewSort, []string) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, []string{sourceDiagnostic(pkg, expr.Pos(), "page.Collection.Sorts must be a static page.Sort slice")}
	}
	var out []model.ViewSort
	var errs []string
	for _, elt := range lit.Elts {
		call, ok := elt.(*ast.CallExpr)
		if !ok || !isPackageCall(call.Fun, aliases, "scenery.sh/page", "Sort") || len(call.Args) != 2 {
			errs = append(errs, sourceDiagnostic(pkg, elt.Pos(), "page sorts must use page.Sort(\"Field\", page.Asc|page.Desc)"))
			continue
		}
		field, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), "page.Sort requires a constant field name"))
			continue
		}
		direction, ok := staticStringValue(pkg, call.Args[1])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), "page.Sort requires a constant direction"))
			continue
		}
		direction = strings.TrimSpace(direction)
		if direction != "asc" && direction != "desc" {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[1].Pos(), fmt.Sprintf("unsupported page sort direction %q", direction)))
			continue
		}
		out = append(out, model.ViewSort{Field: strings.TrimSpace(field), Direction: direction})
	}
	return out, errs
}

func validateViewQuery(view *model.View, entity *model.Entity) []string {
	fields := make(map[string]model.EntityField, len(entity.Fields))
	for _, field := range entity.Fields {
		fields[field.Name] = field
	}
	var errs []string
	resolve := func(name, kind string) (model.EntityField, bool) {
		field, ok := fields[name]
		if !ok {
			errs = append(errs, sourceDiagnostic(view.Package, view.TokenPos, fmt.Sprintf("page %s %s field %q does not match a field on %s", view.Name, kind, name, entity.Name)))
			return model.EntityField{}, false
		}
		if !model.EntityFieldIsStored(field) {
			errs = append(errs, sourceDiagnostic(view.Package, view.TokenPos, fmt.Sprintf("page %s %s field %q is %s and cannot be used by generated collection queries yet", view.Name, kind, name, field.Kind)))
			return model.EntityField{}, false
		}
		return field, true
	}
	for i := range view.ColumnDisplays {
		if field, ok := resolve(view.ColumnDisplays[i].Field, "display"); ok {
			view.ColumnDisplays[i].Field = field.Name
		}
	}
	for i := range view.Filters {
		if field, ok := resolve(view.Filters[i].Field, "filter"); ok {
			view.Filters[i].Field = field.Name
			view.Filters[i].Column = field.Column
		}
	}
	for i := range view.Sorts {
		if field, ok := resolve(view.Sorts[i].Field, "sort"); ok {
			view.Sorts[i].Field = field.Name
			view.Sorts[i].Column = field.Column
		}
	}
	return errs
}
