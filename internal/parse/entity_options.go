package parse

import (
	"fmt"
	"go/ast"
	"go/constant"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/model"
)

func parseEntityOption(pkg *model.Package, aliases map[string]string, expr ast.Expr, cfg *entityConfig) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("model.Entity options must be static model.* calls")
	}
	switch {
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Table"):
		if len(call.Args) != 1 {
			return fmt.Errorf("model.Table requires one string argument")
		}
		value, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.Table requires a constant string argument")
		}
		cfg.Table = strings.TrimSpace(value)
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "ExistingTable"):
		if len(call.Args) != 2 {
			return fmt.Errorf("model.ExistingTable requires schema and table string arguments")
		}
		schema, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.ExistingTable schema must be a constant string")
		}
		table, ok := staticStringValue(pkg, call.Args[1])
		if !ok {
			return fmt.Errorf("model.ExistingTable table must be a constant string")
		}
		cfg.SourceKind = model.EntitySourceExisting
		cfg.SourceSchema = strings.TrimSpace(schema)
		cfg.Table = strings.TrimSpace(table)
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Field"):
		if len(call.Args) == 0 {
			return fmt.Errorf("model.Field requires a field name")
		}
		fieldName, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.Field requires a constant field-name string")
		}
		fieldName = strings.TrimSpace(fieldName)
		if fieldName == "" {
			return fmt.Errorf("model.Field requires a non-empty field name")
		}
		fieldCfg := cfg.Fields[fieldName]
		for _, opt := range call.Args[1:] {
			if err := parseEntityFieldOption(pkg, aliases, opt, &fieldCfg); err != nil {
				return fmt.Errorf("model.Field(%q): %w", fieldName, err)
			}
		}
		cfg.Fields[fieldName] = fieldCfg
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Generate"):
		actions, err := parseEntityActionArgs(pkg, call.Args)
		if err != nil {
			return fmt.Errorf("model.Generate: %w", err)
		}
		cfg.GenerateSet = true
		cfg.CRUD.Actions = actions
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Seed"):
		for _, arg := range call.Args {
			row, err := parseEntitySeedRow(pkg, aliases, arg)
			if err != nil {
				return fmt.Errorf("model.Seed: %w", err)
			}
			cfg.Seeds = append(cfg.Seeds, row)
		}
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Disable"):
		actions, err := parseEntityActionArgs(pkg, call.Args)
		if err != nil {
			return fmt.Errorf("model.Disable: %w", err)
		}
		cfg.CRUD.Disabled = append(cfg.CRUD.Disabled, actions...)
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Override"):
		if len(call.Args) != 2 {
			return fmt.Errorf("model.Override requires an action and endpoint")
		}
		actions, err := parseEntityActionArgs(pkg, call.Args[:1])
		if err != nil {
			return fmt.Errorf("model.Override: %w", err)
		}
		endpoint := staticEndpointName(pkg, call.Args[1])
		if endpoint == "" {
			return fmt.Errorf("model.Override endpoint must be a static function identifier or selector")
		}
		cfg.CRUD.Overrides = append(cfg.CRUD.Overrides, model.EntityCRUDOverride{Action: actions[0], Endpoint: endpoint})
	default:
		return fmt.Errorf("unsupported model.Entity option; use model.Table, model.ExistingTable, model.Field, model.Generate, model.Seed, model.Disable, or model.Override")
	}
	return nil
}

func parseEntitySeedRow(pkg *model.Package, aliases map[string]string, expr ast.Expr) (model.EntitySeedRow, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return model.EntitySeedRow{}, fmt.Errorf("rows must be static keyed struct literals")
	}
	row := model.EntitySeedRow{TokenPos: lit.Pos()}
	seen := map[string]bool{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return model.EntitySeedRow{}, fmt.Errorf("rows must use keyed struct fields")
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name == "" {
			return model.EntitySeedRow{}, fmt.Errorf("row field keys must be identifiers")
		}
		if seen[key.Name] {
			return model.EntitySeedRow{}, fmt.Errorf("row field %s is set more than once", key.Name)
		}
		value, err := parseEntitySeedValue(pkg, aliases, kv.Value)
		if err != nil {
			return model.EntitySeedRow{}, fmt.Errorf("%s: %w", key.Name, err)
		}
		value.Field = key.Name
		value.TokenPos = kv.Pos()
		row.Values = append(row.Values, value)
		seen[key.Name] = true
	}
	return row, nil
}

func parseEntitySeedValue(pkg *model.Package, aliases map[string]string, expr ast.Expr) (model.EntitySeedValue, error) {
	if call, ok := expr.(*ast.CallExpr); ok && isPackageCall(call.Fun, aliases, "time", "Date") {
		value, err := parseStaticTimeDate(pkg, aliases, call)
		if err != nil {
			return model.EntitySeedValue{}, err
		}
		return model.EntitySeedValue{Kind: model.EntitySeedTimestamp, Value: value}, nil
	}
	if pkg != nil && pkg.GoPkg != nil {
		if tv, ok := pkg.GoPkg.TypesInfo.Types[expr]; ok && tv.Value != nil {
			switch tv.Value.Kind() {
			case constant.String:
				value, err := strconv.Unquote(tv.Value.ExactString())
				if err != nil {
					return model.EntitySeedValue{}, err
				}
				return model.EntitySeedValue{Kind: model.EntitySeedString, Value: value}, nil
			case constant.Int:
				return model.EntitySeedValue{Kind: model.EntitySeedInteger, Value: tv.Value.ExactString()}, nil
			case constant.Float:
				return model.EntitySeedValue{Kind: model.EntitySeedFloat, Value: tv.Value.ExactString()}, nil
			case constant.Bool:
				return model.EntitySeedValue{Kind: model.EntitySeedBool, Value: tv.Value.ExactString()}, nil
			}
		}
	}
	return model.EntitySeedValue{}, fmt.Errorf("seed values must be compile-time constants or time.Date(...)")
}

func parseStaticTimeDate(pkg *model.Package, aliases map[string]string, call *ast.CallExpr) (string, error) {
	if len(call.Args) != 8 {
		return "", fmt.Errorf("time.Date seed values require eight arguments")
	}
	ints := make([]int, 7)
	for i := 0; i < 7; i++ {
		tv, ok := pkg.GoPkg.TypesInfo.Types[call.Args[i]]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.Int {
			return "", fmt.Errorf("time.Date argument %d must be a constant integer", i+1)
		}
		value, ok := constant.Int64Val(tv.Value)
		if !ok {
			return "", fmt.Errorf("time.Date argument %d is out of range", i+1)
		}
		ints[i] = int(value)
	}
	if !isTimeUTCSelector(call.Args[7], aliases) {
		return "", fmt.Errorf("time.Date seed values must use time.UTC")
	}
	t := time.Date(ints[0], time.Month(ints[1]), ints[2], ints[3], ints[4], ints[5], ints[6], time.UTC)
	return t.UTC().Format(time.RFC3339Nano), nil
}

func isTimeUTCSelector(expr ast.Expr, aliases map[string]string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "UTC" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && aliases[ident.Name] == "time"
}

func parseEntityActionArgs(pkg *model.Package, args []ast.Expr) ([]model.EntityCRUDAction, error) {
	if len(args) == 0 {
		return defaultEntityCRUDActions(), nil
	}
	seen := make(map[model.EntityCRUDAction]bool, len(args))
	out := make([]model.EntityCRUDAction, 0, len(args))
	for _, arg := range args {
		value, ok := staticStringValue(pkg, arg)
		if !ok {
			return nil, fmt.Errorf("actions must be constant model.Action/string values")
		}
		action, ok := normalizeEntityCRUDAction(value)
		if !ok {
			return nil, fmt.Errorf("unsupported action %q", value)
		}
		if !seen[action] {
			seen[action] = true
			out = append(out, action)
		}
	}
	return out, nil
}

func normalizeEntityCRUDAction(value string) (model.EntityCRUDAction, bool) {
	switch model.EntityCRUDAction(strings.ToLower(strings.TrimSpace(value))) {
	case model.EntityCRUDList:
		return model.EntityCRUDList, true
	case model.EntityCRUDGet:
		return model.EntityCRUDGet, true
	case model.EntityCRUDCreate:
		return model.EntityCRUDCreate, true
	case model.EntityCRUDUpdate:
		return model.EntityCRUDUpdate, true
	case model.EntityCRUDDelete:
		return model.EntityCRUDDelete, true
	default:
		return "", false
	}
}

func defaultEntityCRUDActions() []model.EntityCRUDAction {
	return []model.EntityCRUDAction{
		model.EntityCRUDList,
		model.EntityCRUDGet,
		model.EntityCRUDCreate,
		model.EntityCRUDUpdate,
		model.EntityCRUDDelete,
	}
}

func staticEndpointName(pkg *model.Package, expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

func parseEntityFieldOption(pkg *model.Package, aliases map[string]string, expr ast.Expr, cfg *entityFieldConfig) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("field options must be static model.* calls")
	}
	switch {
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "EnumValues"):
		if len(call.Args) == 0 {
			return fmt.Errorf("model.EnumValues requires at least one value")
		}
		cfg.EnumValues = nil
		for _, arg := range call.Args {
			value, ok := staticStringValue(pkg, arg)
			if !ok {
				return fmt.Errorf("model.EnumValues requires constant string arguments")
			}
			cfg.EnumValues = append(cfg.EnumValues, value)
		}
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Filterable"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Filterable takes no arguments")
		}
		cfg.Filterable = true
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Computed"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Computed takes no arguments")
		}
		cfg.Kind = model.EntityFieldComputed
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Relationship"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Relationship takes no arguments")
		}
		cfg.Kind = model.EntityFieldRelationship
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "RenamedFrom"):
		if len(call.Args) != 1 {
			return fmt.Errorf("model.RenamedFrom requires one string argument")
		}
		value, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.RenamedFrom requires a constant string argument")
		}
		cfg.RenamedFrom = strings.TrimSpace(value)
	default:
		return fmt.Errorf("unsupported field option")
	}
	return nil
}
