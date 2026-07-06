package codegen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func writeGeneratedModelBackend(buf *strings.Builder, im *imports, endpoints []*model.GeneratedModelEndpoint, cfg appcfg.Config) {
	if len(endpoints) == 0 {
		return
	}
	entities := generatedModelEntities(endpoints)
	writeGeneratedModelSharedPool(buf, im, cfg)
	for _, entity := range entities {
		writeGeneratedModelTypes(buf, im, entity)
		writeGeneratedModelStore(buf, im, entity)
	}
}

func generatedModelEntities(endpoints []*model.GeneratedModelEndpoint) []*model.Entity {
	seen := map[*model.Entity]bool{}
	var out []*model.Entity
	for _, ep := range endpoints {
		if ep == nil || ep.Entity == nil || seen[ep.Entity] {
			continue
		}
		seen[ep.Entity] = true
		out = append(out, ep.Entity)
	}
	slices.SortFunc(out, func(a, b *model.Entity) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func writeGeneratedModelTypes(buf *strings.Builder, im *imports, entity *model.Entity) {
	listType := generatedModelListQueryType(entity)
	createType := generatedModelCreateType(entity)
	patchType := generatedModelPatchType(entity)
	createFields := generatedModelCreateFields(entity)
	patchFields := generatedModelPatchFields(entity)
	fmt.Fprintf(buf, "type %s struct {\n", listType)
	buf.WriteString("\tLimit *int `json:\"limit,omitempty\"`\n")
	buf.WriteString("\tOffset int `json:\"offset,omitempty\"`\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(buf, "func (query %s) sceneryModelBounds() (int, int, error) {\n", listType)
	buf.WriteString("\tlimit := sceneryModelDefaultListLimit\n")
	buf.WriteString("\tif query.Limit != nil {\n\t\tlimit = *query.Limit\n\t}\n")
	fmt.Fprintf(buf, "\tif limit < 1 {\n\t\treturn 0, 0, errs.B().Code(errs.InvalidArgument).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" list limit must be at least 1")
	fmt.Fprintf(buf, "\tif limit > sceneryModelMaxListLimit {\n\t\treturn 0, 0, errs.B().Code(errs.InvalidArgument).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" list limit exceeds 500")
	fmt.Fprintf(buf, "\tif query.Offset < 0 {\n\t\treturn 0, 0, errs.B().Code(errs.InvalidArgument).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" list offset must be non-negative")
	buf.WriteString("\treturn limit, query.Offset, nil\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(buf, "func (query %s) Validate() error {\n", listType)
	buf.WriteString("\t_, _, err := query.sceneryModelBounds()\n\treturn err\n")
	buf.WriteString("}\n\n")
	fmt.Fprintf(buf, "type %s struct {\n", createType)
	for _, field := range createFields {
		fmt.Fprintf(buf, "\t%s %s `json:%q`\n", field.Name, entityFieldTypeExpr(im, field), field.Column+",omitempty")
	}
	buf.WriteString("}\n\n")
	writeGeneratedModelPayloadUnmarshal(buf, im, createType, createFields)
	fmt.Fprintf(buf, "type %s struct {\n", patchType)
	for _, field := range patchFields {
		fmt.Fprintf(buf, "\t%s %s `json:%q`\n", field.Name, entityFieldPatchTypeExpr(im, field), field.Column+",omitempty")
	}
	buf.WriteString("}\n\n")
	writeGeneratedModelPayloadUnmarshal(buf, im, patchType, patchFields)
}

func writeGeneratedModelPayloadUnmarshal(buf *strings.Builder, im *imports, typeName string, fields []model.EntityField) {
	aliasFields := generatedModelJSONAliasFields(fields)
	if len(aliasFields) == 0 {
		return
	}
	jsonPkg := im.use("json", "encoding/json")
	fmtPkg := im.use("fmt", "fmt")
	fmt.Fprintf(buf, "func (input *%s) UnmarshalJSON(data []byte) error {\n", typeName)
	fmt.Fprintf(buf, "\ttype sceneryModelPayload %s\n", typeName)
	buf.WriteString("\tvar out sceneryModelPayload\n")
	fmt.Fprintf(buf, "\tif err := %s.Unmarshal(data, &out); err != nil {\n\t\treturn err\n\t}\n", jsonPkg)
	fmt.Fprintf(buf, "\tvar raw map[string]%s.RawMessage\n", jsonPkg)
	fmt.Fprintf(buf, "\tif err := %s.Unmarshal(data, &raw); err != nil {\n\t\treturn err\n\t}\n", jsonPkg)
	for _, field := range aliasFields {
		fmt.Fprintf(buf, "\tif value, ok := raw[%q]; ok {\n", field.Name)
		fmt.Fprintf(buf, "\t\tif err := %s.Unmarshal(value, &out.%s); err != nil {\n", jsonPkg, field.Name)
		fmt.Fprintf(buf, "\t\t\treturn %s.Errorf(%q, err)\n", fmtPkg, field.Name+": %w")
		buf.WriteString("\t\t}\n\t}\n")
	}
	fmt.Fprintf(buf, "\t*input = %s(out)\n\treturn nil\n}\n\n", typeName)
}

func generatedModelJSONAliasFields(fields []model.EntityField) []model.EntityField {
	var out []model.EntityField
	for _, field := range fields {
		if field.Name == field.Column {
			continue
		}
		out = append(out, field)
	}
	return out
}

func writeGeneratedModelSharedPool(buf *strings.Builder, im *imports, cfg appcfg.Config) {
	sqlPkg := im.use("sql", "database/sql")
	dbPkg := im.use("scenerydb", "scenery.sh/db")
	poolFunc := generatedModelPoolFunc()
	_ = cfg
	buf.WriteString("const (\n")
	buf.WriteString("\tsceneryModelDefaultListLimit = 100\n")
	buf.WriteString("\tsceneryModelMaxListLimit = 500\n")
	buf.WriteString(")\n\n")
	fmt.Fprintf(buf, "func %s(ctx context.Context, service string) (*%s.DB, error) {\n", poolFunc, sqlPkg)
	buf.WriteString("\tif service != \"\" {\n")
	fmt.Fprintf(buf, "\t\treturn %s.Get(ctx, service)\n", dbPkg)
	buf.WriteString("\t}\n")
	fmt.Fprintf(buf, "\treturn %s.Get(ctx)\n}\n\n", dbPkg)
}

func writeGeneratedModelStore(buf *strings.Builder, im *imports, entity *model.Entity) {
	errorsPkg := im.use("errors", "errors")
	fmtPkg := im.use("fmt", "fmt")
	strconvPkg := im.use("strconv", "strconv")
	stringsPkg := im.use("strings", "strings")
	sqlPkg := im.use("sql", "database/sql")
	poolFunc := generatedModelPoolFunc()
	keyFunc := generatedModelKeyFunc(entity)
	fromCreate := generatedModelFromCreateFunc(entity)
	tenantField := entity.TenantField()
	tenantFunc := ""
	authPkg := ""
	if tenantField != nil {
		authPkg = im.use("sceneryauth", "scenery.sh/auth")
		tenantFunc = generatedModelTenantFunc(entity)
	}
	tenantValueFunc := ""
	if tenantField != nil {
		tenantValueFunc = generatedModelTenantValueFunc(entity)
	}
	entityType := entity.Name
	id := generatedModelIDField(entity)
	fields := generatedModelStoredFields(entity)
	createFields := generatedModelCreateFields(entity)
	patchFields := generatedModelPatchFields(entity)
	selectSQL := generatedModelSelectSQL(entity, fields)
	table := generatedModelSQLTable(entity)
	idColumn := generatedModelSQLIdent(id.Column)
	service := generatedModelEntityService(entity)
	fmt.Fprintf(buf, "func %s(id any) string {\n\treturn %s.Sprint(id)\n}\n\n", keyFunc, fmtPkg)
	fmt.Fprintf(buf, "func %s(input %s) %s {\n\treturn %s{\n", fromCreate, generatedModelCreateType(entity), entityType, entityType)
	for _, field := range createFields {
		fmt.Fprintf(buf, "\t\t%s: input.%s,\n", field.Name, field.Name)
	}
	buf.WriteString("\t}\n}\n\n")
	if tenantField != nil {
		fmt.Fprintf(buf, "func %s() (string, error) {\n", tenantFunc)
		fmt.Fprintf(buf, "\tauthData, ok := %s.CurrentAuthData()\n", authPkg)
		fmt.Fprintf(buf, "\tif !ok || authData == nil || %s.TrimSpace(string(authData.TenantID)) == \"\" {\n", stringsPkg)
		fmt.Fprintf(buf, "\t\treturn \"\", errs.B().Code(errs.Unauthenticated).Msg(%q).Err()\n\t}\n", "generated "+entity.Name+" store requires active tenant")
		fmt.Fprintf(buf, "\treturn %s.TrimSpace(string(authData.TenantID)), nil\n}\n\n", stringsPkg)
		writeGeneratedModelTenantValueFunc(buf, im, entity, *tenantField, tenantValueFunc)
	}
	fmt.Fprintf(buf, "func sceneryModelScan%s(row *%s.Row) (%s, error) {\n\tvar out %s\n", entity.Name, sqlPkg, entityType, entityType)
	fmt.Fprintf(buf, "\tif err := row.Scan(%s); err != nil {\n\t\tif %s.Is(err, %s.ErrNoRows) {\n\t\t\treturn %s{}, errs.B().Code(errs.NotFound).Msg(%q).Err()\n\t\t}\n\t\treturn %s{}, err\n\t}\n\treturn out, nil\n}\n\n", generatedModelScanArgs(fields, "out"), errorsPkg, sqlPkg, entityType, entity.Name+" not found", entityType)
	fmt.Fprintf(buf, "func sceneryModelList%s(ctx context.Context, query %s) ([]%s, error) {\n", entity.Name, generatedModelListQueryType(entity), entityType)
	buf.WriteString("\tlimit, offset, err := query.sceneryModelBounds()\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fmt.Fprintf(buf, "\tpool, err := %s(ctx, %q)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", poolFunc, service)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", tenantFunc)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", tenantValueFunc)
		fmt.Fprintf(buf, "\trows, err := pool.QueryContext(ctx, %q, tenantValue, limit, offset)\n", selectSQL+" where "+generatedModelSQLIdent(tenantField.Column)+" = $1 order by "+idColumn+" limit $2 offset $3")
	} else {
		fmt.Fprintf(buf, "\trows, err := pool.QueryContext(ctx, %q, limit, offset)\n", selectSQL+" order by "+idColumn+" limit $1 offset $2")
	}
	buf.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n\tdefer rows.Close()\n")
	fmt.Fprintf(buf, "\tout := []%s{}\n\tfor rows.Next() {\n\t\tvar item %s\n", entityType, entityType)
	fmt.Fprintf(buf, "\t\tif err := rows.Scan(%s); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tout = append(out, item)\n\t}\n\tif err := rows.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n", generatedModelScanArgs(fields, "item"))
	fmt.Fprintf(buf, "func sceneryModelGet%s(ctx context.Context, id any) (%s, error) {\n", entity.Name, entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx, %q)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, service, entityType)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRowContext(ctx, %q, id, tenantValue))\n}\n\n", entity.Name, selectSQL+" where "+idColumn+" = $1 and "+generatedModelSQLIdent(tenantField.Column)+" = $2")
	} else {
		fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRowContext(ctx, %q, id))\n}\n\n", entity.Name, selectSQL+" where "+idColumn+" = $1")
	}
	fmt.Fprintf(buf, "func sceneryModelCreate%s(ctx context.Context, input %s) (%s, error) {\n", entity.Name, generatedModelCreateType(entity), entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx, %q)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, service, entityType)
	fmt.Fprintf(buf, "\trow := %s(input)\n\tkey := %s(row.%s)\n", fromCreate, keyFunc, id.Name)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		fmt.Fprintf(buf, "\trow.%s = tenantValue\n", tenantField.Name)
	}
	fmt.Fprintf(buf, "\tif key == \"\" {\n\t\treturn %s{}, errs.B().Code(errs.InvalidArgument).Msg(%q).Err()\n\t}\n", entityType, entity.Name+" ID is required")
	fmt.Fprintf(buf, "\tcreated, err := sceneryModelScan%s(pool.QueryRowContext(ctx, %q, %s))\n", entity.Name, generatedModelInsertSQL(entity, fields), generatedModelFieldArgs(fields, "row"))
	fmt.Fprintf(buf, "\tif err != nil {\n\t\tif %s.Contains(%s.ToLower(err.Error()), \"constraint\") {\n\t\t\treturn %s{}, errs.B().Code(errs.AlreadyExists).Msgf(%q, key).Err()\n\t\t}\n\t\treturn %s{}, err\n\t}\n\treturn created, nil\n}\n\n", stringsPkg, stringsPkg, entityType, entity.Name+" %s already exists", entityType)
	fmt.Fprintf(buf, "func sceneryModelUpdate%s(ctx context.Context, id any, patch %s) (%s, error) {\n", entity.Name, generatedModelPatchType(entity), entityType)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx, %q)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", poolFunc, service, entityType)
	buf.WriteString("\tsets := []string{}\n\targs := []any{}\n")
	for _, field := range patchFields {
		fmt.Fprintf(buf, "\tif patch.%s != nil {\n\t\targs = append(args, *patch.%s)\n\t\tsets = append(sets, %q + %s.Itoa(len(args)))\n\t}\n", field.Name, field.Name, generatedModelSQLIdent(field.Column)+" = $", strconvPkg)
	}
	fmt.Fprintf(buf, "\tif len(sets) == 0 {\n\t\treturn sceneryModelGet%s(ctx, id)\n\t}\n", entity.Name)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantFunc, entityType)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", tenantValueFunc, entityType)
		buf.WriteString("\targs = append(args, id)\n")
		fmt.Fprintf(buf, "\tidPlaceholder := %q + %s.Itoa(len(args))\n", "$", strconvPkg)
		buf.WriteString("\targs = append(args, tenantValue)\n")
		fmt.Fprintf(buf, "\ttenantPlaceholder := %q + %s.Itoa(len(args))\n", "$", strconvPkg)
		fmt.Fprintf(buf, "\tquery := %s.Sprintf(%q, %s.Join(sets, \", \"), idPlaceholder, tenantPlaceholder)\n", fmtPkg, "update "+table+" set %s where "+idColumn+" = %s and "+generatedModelSQLIdent(tenantField.Column)+" = %s returning "+generatedModelColumnList(fields), stringsPkg)
	} else {
		buf.WriteString("\targs = append(args, id)\n")
		fmt.Fprintf(buf, "\tidPlaceholder := %q + %s.Itoa(len(args))\n", "$", strconvPkg)
		fmt.Fprintf(buf, "\tquery := %s.Sprintf(%q, %s.Join(sets, \", \"), idPlaceholder)\n", fmtPkg, "update "+table+" set %s where "+idColumn+" = %s returning "+generatedModelColumnList(fields), stringsPkg)
	}
	fmt.Fprintf(buf, "\treturn sceneryModelScan%s(pool.QueryRowContext(ctx, query, args...))\n}\n\n", entity.Name)
	fmt.Fprintf(buf, "func sceneryModelDelete%s(ctx context.Context, id any) error {\n", entity.Name)
	fmt.Fprintf(buf, "\tpool, err := %s(ctx, %q)\n\tif err != nil {\n\t\treturn err\n\t}\n", poolFunc, service)
	if tenantField != nil {
		fmt.Fprintf(buf, "\ttenantID, err := %s()\n\tif err != nil {\n\t\treturn err\n\t}\n", tenantFunc)
		fmt.Fprintf(buf, "\ttenantValue, err := %s(tenantID)\n\tif err != nil {\n\t\treturn err\n\t}\n", tenantValueFunc)
		fmt.Fprintf(buf, "\tresult, err := pool.ExecContext(ctx, %q, id, tenantValue)\n", "delete from "+table+" where "+idColumn+" = $1 and "+generatedModelSQLIdent(tenantField.Column)+" = $2")
	} else {
		fmt.Fprintf(buf, "\tresult, err := pool.ExecContext(ctx, %q, id)\n", "delete from "+table+" where "+idColumn+" = $1")
	}
	fmt.Fprintf(buf, "\tif err != nil {\n\t\treturn err\n\t}\n\taffected, err := result.RowsAffected()\n\tif err != nil {\n\t\treturn err\n\t}\n\tif affected == 0 {\n\t\treturn errs.B().Code(errs.NotFound).Msgf(%q, %s(id)).Err()\n\t}\n\treturn nil\n}\n\n", entity.Name+" %s not found", keyFunc)
}

func generatedModelStoredFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	for _, field := range entity.Fields {
		if !model.EntityFieldIsStored(field) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelPatchFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range generatedModelStoredFields(entity) {
		if strings.EqualFold(field.Name, "id") {
			continue
		}
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelCreateFields(entity *model.Entity) []model.EntityField {
	var fields []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range generatedModelStoredFields(entity) {
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func generatedModelIDField(entity *model.Entity) model.EntityField {
	for _, field := range entity.Fields {
		if model.EntityFieldIsStored(field) && strings.EqualFold(field.Name, "id") {
			return field
		}
	}
	return model.EntityField{Name: "ID", TypeExpr: "string", Column: "id"}
}

func generatedModelSQLIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func generatedModelSQLTable(entity *model.Entity) string {
	return generatedModelSQLIdent(entity.Table)
}

func generatedModelColumnList(fields []model.EntityField) string {
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, generatedModelSQLIdent(field.Column))
	}
	return strings.Join(columns, ", ")
}

func generatedModelSelectSQL(entity *model.Entity, fields []model.EntityField) string {
	return "select " + generatedModelColumnList(fields) + " from " + generatedModelSQLTable(entity)
}

func generatedModelInsertSQL(entity *model.Entity, fields []model.EntityField) string {
	columns := generatedModelColumnList(fields)
	placeholders := make([]string, 0, len(fields))
	for i := range fields {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	return "insert into " + generatedModelSQLTable(entity) + " (" + columns + ") values (" + strings.Join(placeholders, ", ") + ") returning " + columns
}

func generatedModelEntityService(entity *model.Entity) string {
	if entity != nil && entity.Package != nil && entity.Package.Service != nil {
		return strings.TrimSpace(entity.Package.Service.Name)
	}
	return ""
}

func generatedModelScanArgs(fields []model.EntityField, target string) string {
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		args = append(args, "&"+target+"."+field.Name)
	}
	return strings.Join(args, ", ")
}

func generatedModelFieldArgs(fields []model.EntityField, target string) string {
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		args = append(args, target+"."+field.Name)
	}
	return strings.Join(args, ", ")
}

func writeGeneratedModelTenantValueFunc(buf *strings.Builder, im *imports, entity *model.Entity, field model.EntityField, name string) {
	typeExpr := entityFieldTypeExpr(im, field)
	if model.GeneratedTenantFieldKind(field) == "uuid" {
		uuidPkg := im.use("uuid", "github.com/google/uuid")
		fmt.Fprintf(buf, "func %s(tenantID string) (%s, error) {\n", name, typeExpr)
		fmt.Fprintf(buf, "\ttenantUUID, err := %s.Parse(tenantID)\n", uuidPkg)
		buf.WriteString("\tif err != nil {\n")
		fmt.Fprintf(buf, "\t\tvar zero %s\n", typeExpr)
		fmt.Fprintf(buf, "\t\treturn zero, errs.B().Code(errs.InvalidArgument).Msg(%q).Cause(err).Err()\n", "generated "+entity.Name+" store requires valid tenant_id UUID")
		buf.WriteString("\t}\n")
		buf.WriteString("\treturn tenantUUID, nil\n")
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "func %s(tenantID string) (%s, error) {\n\treturn %s(tenantID), nil\n}\n\n", name, typeExpr, typeExpr)
}

func entityFieldTypeExpr(im *imports, field model.EntityField) string {
	if field.Type != nil {
		return im.typeExpr(field.Type)
	}
	return field.TypeExpr
}

func entityFieldPatchTypeExpr(im *imports, field model.EntityField) string {
	if ptr, ok := field.Type.(*types.Pointer); ok {
		return "*" + im.typeExpr(ptr.Elem())
	}
	return "*" + entityFieldTypeExpr(im, field)
}

func generatedModelDBStateName() string {
	return "sceneryModelStoreDB"
}

func generatedModelPoolFunc() string {
	return "sceneryModelStorePool"
}

func generatedModelKeyFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "Key"
}

func generatedModelFromCreateFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "FromCreate"
}

func generatedModelListQueryType(entity *model.Entity) string {
	return entity.Name + "ListQuery"
}

func generatedModelTenantFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "TenantID"
}

func generatedModelTenantValueFunc(entity *model.Entity) string {
	return "sceneryModel" + entity.Name + "TenantValue"
}

func generatedModelCreateType(entity *model.Entity) string {
	return entity.Name + "Create"
}

func generatedModelPatchType(entity *model.Entity) string {
	return entity.Name + "Patch"
}
