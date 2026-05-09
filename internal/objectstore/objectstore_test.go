package objectstore

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateNameRejectsUnsafeIdentifiers(t *testing.T) {
	for _, name := range []string{
		"",
		"Company",
		"company-name",
		"company;drop_table",
		"1company",
		"select",
		strings.Repeat("a", 64),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateName("field", name); err == nil {
				t.Fatalf("validateName(%q) succeeded, want error", name)
			}
		})
	}
	if err := validateName("field", "company_name_1"); err != nil {
		t.Fatalf("validateName(valid) error = %v", err)
	}
}

func TestFieldColumnsMapping(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		fieldType FieldType
		want      []PhysicalColumn
	}{
		{
			name:      "text",
			id:        "11111111-1111-4111-8111-111111111111",
			fieldType: FieldText,
			want:      []PhysicalColumn{{Name: "text__111111111111", SQLType: "text", Nullable: true}},
		},
		{
			name:      "amount",
			id:        "22222222-2222-4222-8222-222222222222",
			fieldType: FieldCurrency,
			want: []PhysicalColumn{
				{Name: "amount_amount__222222222222", Part: "amount", SQLType: "numeric", Nullable: true},
				{Name: "amount_currency_code__222222222222", Part: "currency_code", SQLType: "text", Nullable: true},
			},
		},
		{
			name:      "name",
			id:        "33333333-3333-4333-8333-333333333333",
			fieldType: FieldFullName,
			want: []PhysicalColumn{
				{Name: "name_first_name__333333333333", Part: "first_name", SQLType: "text", Nullable: true},
				{Name: "name_last_name__333333333333", Part: "last_name", SQLType: "text", Nullable: true},
			},
		},
		{
			name:      "stage",
			id:        "44444444-4444-4444-8444-444444444444",
			fieldType: FieldSelect,
			want:      []PhysicalColumn{{Name: "stage__444444444444", SQLType: "text", Nullable: true}},
		},
		{
			name:      "tags",
			id:        "55555555-5555-4555-8555-555555555555",
			fieldType: FieldMultiSelect,
			want:      []PhysicalColumn{{Name: "tags__555555555555", SQLType: "text[]", Nullable: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fieldColumns(tt.name, tt.id, tt.fieldType, true)
			if err != nil {
				t.Fatalf("fieldColumns() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("fieldColumns() len = %d, want %d: %#v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("fieldColumns()[%d] = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDDLGenerationUsesRealColumns(t *testing.T) {
	tableName := "company__111111111111"
	create := createObjectTableDDL(tableName)
	for _, want := range []string{
		`create table "onlava_data_records"."company__111111111111"`,
		`id uuid primary key`,
		`tenant_id uuid not null`,
		`deleted_at timestamptz null`,
	} {
		if !strings.Contains(create, want) {
			t.Fatalf("createObjectTableDDL missing %q:\n%s", want, create)
		}
	}
	field := &Field{
		Name:       "stage",
		Type:       FieldSelect,
		IsNullable: true,
		Columns:    []PhysicalColumn{{Name: "stage__444444444444", SQLType: "text", Nullable: true}},
	}
	ddl := addFieldDDL(tableName, field)
	if len(ddl) != 1 || ddl[0] != `alter table "onlava_data_records"."company__111111111111" add column "stage__444444444444" text` {
		t.Fatalf("addFieldDDL = %#v", ddl)
	}
	state := testState()
	index := &Index{
		Name:         "company_stage_arr",
		PhysicalName: "company_stage_arr__aaaaaaaaaaaa",
		Method:       IndexMethodBTree,
		Fields: []IndexField{
			{Field: "stage"},
			{Field: "arr", Desc: true},
		},
	}
	indexDDL, err := createIndexDDL(tableName, index, state.Fields)
	if err != nil {
		t.Fatalf("createIndexDDL: %v", err)
	}
	wantIndex := `create index "company_stage_arr__aaaaaaaaaaaa" on "onlava_data_records"."company__111111111111" using btree ("stage__fieldstage" asc, "arr__fieldarr" desc)`
	if indexDDL != wantIndex {
		t.Fatalf("createIndexDDL = %q, want %q", indexDDL, wantIndex)
	}
	relationField := &Field{
		ID:      "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		Name:    "company",
		Type:    FieldRelation,
		Columns: []PhysicalColumn{{Name: "company__dddddddddddd", SQLType: "uuid", Nullable: true}},
	}
	relationDDL, err := relationFieldDDL(tableName, relationField, &relationConfig{
		Kind:     RelationManyToOne,
		Target:   &Object{TableName: "account__222222222222"},
		OnDelete: RelationDeleteRestrict,
	})
	if err != nil {
		t.Fatalf("relationFieldDDL: %v", err)
	}
	wantRelation := `alter table "onlava_data_records"."company__111111111111" add constraint "fk_company__dddddddddddd" foreign key ("company__dddddddddddd") references "onlava_data_records"."account__222222222222" (id) on delete restrict`
	if len(relationDDL) != 1 || relationDDL[0] != wantRelation {
		t.Fatalf("relationFieldDDL = %#v, want %q", relationDDL, wantRelation)
	}
}

func TestPhysicalNamesUseStableReadableSuffixes(t *testing.T) {
	objectID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if got, want := physicalTableName(objectID, "company"), "company__aaaaaaaaaaaa"; got != want {
		t.Fatalf("physicalTableName = %q, want %q", got, want)
	}
	fieldID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if got, want := physicalColumnName(fieldID, "stage", ""), "stage__bbbbbbbbbbbb"; got != want {
		t.Fatalf("physicalColumnName = %q, want %q", got, want)
	}
	if got, want := physicalColumnName(fieldID, "full_name", "first_name"), "full_name_first_name__bbbbbbbbbbbb"; got != want {
		t.Fatalf("physicalColumnName composite = %q, want %q", got, want)
	}
	indexID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if got, want := physicalIndexName(indexID, "company_stage_arr"), "company_stage_arr__cccccccccccc"; got != want {
		t.Fatalf("physicalIndexName = %q, want %q", got, want)
	}
}

func TestPhysicalNamesRespectPostgresIdentifierLength(t *testing.T) {
	longName := strings.Repeat("a", maxIdentifierLength)
	objectName := physicalTableName("cccccccc-cccc-4ccc-8ccc-cccccccccccc", longName)
	if len(objectName) > maxIdentifierLength {
		t.Fatalf("physical table name length = %d, want <= %d: %q", len(objectName), maxIdentifierLength, objectName)
	}
	columnName := physicalColumnName("dddddddd-dddd-4ddd-8ddd-dddddddddddd", longName, "currency_code")
	if len(columnName) > maxIdentifierLength {
		t.Fatalf("physical column name length = %d, want <= %d: %q", len(columnName), maxIdentifierLength, columnName)
	}
	if !strings.HasSuffix(objectName, "__cccccccccccc") || !strings.HasSuffix(columnName, "__dddddddddddd") {
		t.Fatalf("physical suffixes not preserved: table=%q column=%q", objectName, columnName)
	}
}

func TestValidateIndexFieldRejectsOpClass(t *testing.T) {
	field := &Field{
		Name:    "payload",
		Type:    FieldJSON,
		Columns: []PhysicalColumn{{Name: "payload__fieldpayload", SQLType: "jsonb", Nullable: true}},
	}
	err := validateIndexField(IndexMethodGIN, field, IndexField{
		Field:   "payload",
		OpClass: `jsonb_path_ops); drop table "onlava_data"."objects"; --`,
	})
	if err == nil || !strings.Contains(err.Error(), "opclass") {
		t.Fatalf("validateIndexField error = %v, want opclass rejection", err)
	}
}

func TestCompileQueryParameterizesValuesAndQuotesMetadataIdentifiers(t *testing.T) {
	state := testState()
	filter := &Filter{
		Op: "and",
		Filters: []Filter{
			{Op: "contains", Field: "name", Value: "Acme' OR true --"},
			{Op: "in", Field: "stage", Values: []any{"lead", "won"}},
		},
	}
	compiled, err := compileQuery(state, Query{
		Select: []string{"name", "stage"},
		Filter: filter,
		Sort:   []Sort{{Field: "name"}},
		Limit:  25,
	})
	if err != nil {
		t.Fatalf("compileQuery() error = %v", err)
	}
	if strings.Contains(compiled.SQL, "Acme") || strings.Contains(compiled.SQL, "lead") {
		t.Fatalf("compileQuery interpolated user values into SQL:\n%s", compiled.SQL)
	}
	for _, want := range []string{`"name"`, `"stage"`, `$2`, `$3`, `$4`, `limit $5`} {
		if !strings.Contains(compiled.SQL, want) {
			t.Fatalf("compileQuery SQL missing %q:\n%s", want, compiled.SQL)
		}
	}
	if len(compiled.Args) != 5 {
		t.Fatalf("compileQuery args len = %d, want 5: %#v", len(compiled.Args), compiled.Args)
	}
}

func TestCompileQueryAppliesKeysetCursor(t *testing.T) {
	state := testState()
	state.Fields["arr"].IsNullable = false
	cursor, err := encodeCursor(cursorPayload{
		Version:       1,
		Object:        "company",
		SchemaVersion: state.Object.SchemaVersion,
		Sort:          []Sort{{Field: "arr", Desc: true}, {Field: "id"}},
		Values:        []any{100, "record-1"},
	})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	compiled, err := compileQuery(state, Query{
		Select: []string{"name"},
		Sort:   []Sort{{Field: "arr", Desc: true}},
		Limit:  10,
		Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("compileQuery() error = %v", err)
	}
	for _, want := range []string{
		`(r."arr__fieldarr" < $2)`,
		`(r."arr__fieldarr" = $2 and r.id::text > $3)`,
		`order by r."arr__fieldarr" desc, r.id::text asc`,
		`limit $4`,
	} {
		if !strings.Contains(compiled.SQL, want) {
			t.Fatalf("compileQuery SQL missing %q:\n%s", want, compiled.SQL)
		}
	}
	if compiled.Limit != 10 || len(compiled.CursorColumns) != 2 {
		t.Fatalf("compiled cursor metadata = %#v", compiled)
	}
}

func TestCompileQueryRejectsCursorOnNullableSortField(t *testing.T) {
	state := testState()
	cursor, err := encodeCursor(cursorPayload{
		Version:       1,
		Object:        "company",
		SchemaVersion: state.Object.SchemaVersion,
		Sort:          []Sort{{Field: "name"}, {Field: "id"}},
		Values:        []any{"Acme", "record-1"},
	})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	_, err = compileQuery(state, Query{Sort: []Sort{{Field: "name"}}, Cursor: cursor})
	if err == nil || !strings.Contains(err.Error(), "nullable sort field") {
		t.Fatalf("compileQuery error = %v, want nullable sort field rejection", err)
	}
}

func TestCompileQueryRejectsCursorShapeMismatch(t *testing.T) {
	state := testState()
	cursor, err := encodeCursor(cursorPayload{
		Version:       1,
		Object:        "company",
		SchemaVersion: state.Object.SchemaVersion,
		Sort:          []Sort{{Field: "name"}, {Field: "id"}},
		Values:        []any{"Acme", "record-1"},
	})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	_, err = compileQuery(state, Query{Sort: []Sort{{Field: "arr"}}, Cursor: cursor})
	if err == nil || !strings.Contains(err.Error(), "cursor sort shape") {
		t.Fatalf("compileQuery error = %v, want cursor sort shape mismatch", err)
	}
}

func TestCompileQuerySupportsManyToOneRelationPath(t *testing.T) {
	state := relationTestState()
	compiled, err := compileQuery(state, Query{
		Select: []string{"company.name"},
		Filter: &Filter{Op: "eq", Field: "company.stage", Value: "customer"},
		Sort:   []Sort{{Field: "company.name"}},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("compileQuery() error = %v", err)
	}
	for _, want := range []string{
		`to_jsonb((select t."name__fieldname" from "onlava_data_records"."company__target" t where t.tenant_id = r.tenant_id and t.id = r."company__fieldcompany" and t.deleted_at is null)) as "company.name"`,
		`(select t."stage__fieldstage" from "onlava_data_records"."company__target" t where t.tenant_id = r.tenant_id and t.id = r."company__fieldcompany" and t.deleted_at is null) = $2`,
		`order by (select t."name__fieldname" from "onlava_data_records"."company__target" t where t.tenant_id = r.tenant_id and t.id = r."company__fieldcompany" and t.deleted_at is null) asc, r.id::text asc`,
		`limit $3`,
	} {
		if !strings.Contains(compiled.SQL, want) {
			t.Fatalf("compileQuery SQL missing %q:\n%s", want, compiled.SQL)
		}
	}
	if len(compiled.Args) != 3 || compiled.Args[1] != "customer" {
		t.Fatalf("compileQuery args = %#v", compiled.Args)
	}
}

func TestCompileQueryRejectsInvalidOperatorForType(t *testing.T) {
	state := testState()
	_, err := compileQuery(state, Query{
		Filter: &Filter{Op: "contains", Field: "arr", Value: "1"},
	})
	if err == nil || !strings.Contains(err.Error(), "operator contains is not valid") {
		t.Fatalf("compileQuery error = %v, want invalid operator", err)
	}
}

func TestCompileQuerySearchUsesIndexedDocuments(t *testing.T) {
	state := testState()
	compiled, err := compileQuery(state, Query{
		Filter: &Filter{Op: "search", Value: "Acme Labs"},
		Limit:  25,
	})
	if err != nil {
		t.Fatalf("compileQuery() error = %v", err)
	}
	for _, want := range []string{
		`from "onlava_data"."search_documents" sd`,
		`sd.object_id = $2::uuid`,
		`websearch_to_tsquery('simple', $3::text)`,
		`limit $4`,
	} {
		if !strings.Contains(compiled.SQL, want) {
			t.Fatalf("compileQuery SQL missing %q:\n%s", want, compiled.SQL)
		}
	}
	if strings.Contains(compiled.SQL, "Acme") {
		t.Fatalf("compileQuery interpolated search value into SQL:\n%s", compiled.SQL)
	}
	if len(compiled.Args) != 4 || compiled.Args[1] != state.Object.ID || compiled.Args[2] != "Acme Labs" {
		t.Fatalf("compileQuery args = %#v", compiled.Args)
	}
}

func TestCompileQuerySearchRejectsObjectsWithoutSearchableFields(t *testing.T) {
	state := testState()
	state.Fields["name"].IsSearchable = false
	_, err := compileQuery(state, Query{Filter: &Filter{Op: "search", Value: "Acme"}})
	if err == nil || !strings.Contains(err.Error(), "no searchable fields") {
		t.Fatalf("compileQuery error = %v, want no searchable fields", err)
	}
}

func TestEventMatchingAgainstQuerySubscription(t *testing.T) {
	event := &Event{
		TenantID: "tenant-1",
		Object:   "company",
		Action:   "updated",
		Before:   Record{"stage": "lead", "name": "Old"},
		After:    Record{"stage": "won", "name": "New"},
	}
	sub := &liveSubscription{
		tenantID: "tenant-1",
		request: SubscriptionRequest{
			QueryID:        "won-companies",
			Object:         "company",
			Filter:         &Filter{Op: "eq", Field: "stage", Value: "won"},
			SelectedFields: []string{"stage"},
		},
	}
	deliver := eventForSubscription(event, sub)
	if deliver == nil {
		t.Fatal("eventForSubscription returned nil")
	}
	if got := deliver.QueryIDs; len(got) != 1 || got[0] != "won-companies" {
		t.Fatalf("query ids = %#v", got)
	}
	if _, ok := deliver.After["name"]; ok {
		t.Fatalf("selected field stripping kept name: %#v", deliver.After)
	}
	other := *sub
	other.request.Filter = &Filter{Op: "eq", Field: "stage", Value: "lost"}
	if got := eventForSubscription(event, &other); got != nil {
		t.Fatalf("eventForSubscription unrelated = %#v, want nil", got)
	}
}

func TestEventMatchingUsesBeforeAfterByAction(t *testing.T) {
	sub := &liveSubscription{
		tenantID: "tenant-1",
		state:    testState(),
		request: SubscriptionRequest{
			QueryID: "won-companies",
			Object:  "company",
			Filter:  &Filter{Op: "eq", Field: "stage", Value: "won"},
		},
	}
	tests := []struct {
		name  string
		event Event
		want  bool
	}{
		{
			name: "create matching after",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "created",
				After: Record{"stage": "won"}},
			want: true,
		},
		{
			name: "create nonmatching after",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "created",
				After: Record{"stage": "lead"}},
		},
		{
			name: "update moves into query",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "updated",
				Before: Record{"stage": "lead"}, After: Record{"stage": "won"}},
			want: true,
		},
		{
			name: "update moves out of query",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "updated",
				Before: Record{"stage": "won"}, After: Record{"stage": "lost"}},
			want: true,
		},
		{
			name: "update never matched",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "updated",
				Before: Record{"stage": "lead"}, After: Record{"stage": "lost"}},
		},
		{
			name: "delete previously matched",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "deleted",
				Before: Record{"stage": "won"}},
			want: true,
		},
		{
			name: "delete nonmatching before",
			event: Event{TenantID: "tenant-1", Object: "company", Action: "deleted",
				Before: Record{"stage": "lead"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eventForSubscription(&tt.event, sub) != nil
			if got != tt.want {
				t.Fatalf("eventForSubscription delivered = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventMatchingSupportsSearchFilters(t *testing.T) {
	sub := &liveSubscription{
		tenantID: "tenant-1",
		state:    testState(),
		request: SubscriptionRequest{
			QueryID: "search",
			Object:  "company",
			Filter:  &Filter{Op: "search", Value: "acme"},
		},
	}
	event := &Event{
		TenantID: "tenant-1",
		Object:   "company",
		Action:   "updated",
		Before:   Record{"name": "Beta"},
		After:    Record{"name": "Acme Labs"},
	}
	if got := eventForSubscription(event, sub); got == nil {
		t.Fatal("eventForSubscription search filter returned nil")
	}
	event.After = Record{"name": "Beta Labs"}
	if got := eventForSubscription(event, sub); got != nil {
		t.Fatalf("eventForSubscription nonmatching search = %#v, want nil", got)
	}
}

func TestParseSubscriptionRequestsUsesLastEventID(t *testing.T) {
	req := httptest.NewRequest("GET", "/events?tenant_key=test&object=company&query_id=q", nil)
	req.Header.Set("Last-Event-ID", "42")
	subs, afterSeq, err := parseSubscriptionRequests(req)
	if err != nil {
		t.Fatalf("parseSubscriptionRequests: %v", err)
	}
	if afterSeq != 42 || len(subs) != 1 || subs[0].AfterSeq != 42 {
		t.Fatalf("afterSeq=%d subs=%#v, want 42", afterSeq, subs)
	}
}

func TestLiveRouterFanoutFilteringAndUnsubscribe(t *testing.T) {
	router := newLiveRouter()
	wonSub := &liveSubscription{
		tenantID: "tenant-1",
		request: SubscriptionRequest{
			QueryID: "won",
			Object:  "company",
			Filter:  EQFilter("stage", "won"),
		},
	}
	leadSub := &liveSubscription{
		tenantID: "tenant-1",
		request: SubscriptionRequest{
			QueryID: "lead",
			Object:  "company",
			Filter:  EQFilter("stage", "lead"),
		},
	}
	unsubWon := router.subscribe(wonSub)
	unsubLead := router.subscribe(leadSub)
	defer unsubLead()

	router.publish(&Event{
		TenantID: "tenant-1",
		Object:   "company",
		Action:   "updated",
		Before:   Record{"stage": "lead"},
		After:    Record{"stage": "won"},
	})
	assertLiveEvent(t, wonSub.ch, "won")
	assertLiveEvent(t, leadSub.ch, "lead")

	unsubWon()
	select {
	case _, ok := <-wonSub.ch:
		if ok {
			t.Fatal("won subscription channel still open after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("unsubscribe did not close subscription channel")
	}
}

func TestLiveRouterPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	router := newLiveRouter()
	sub := &liveSubscription{
		tenantID: "tenant-1",
		request:  SubscriptionRequest{QueryID: "all", Object: "company"},
	}
	unsub := router.subscribe(sub)
	defer unsub()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < cap(sub.ch)+10; i++ {
			router.publish(&Event{TenantID: "tenant-1", Object: "company", Action: "created", After: Record{"id": i}})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on a full slow subscriber channel")
	}
}

func EQFilter(field string, value any) *Filter {
	return &Filter{Op: "eq", Field: field, Value: value}
}

func assertLiveEvent(t *testing.T, ch <-chan *Event, queryID string) {
	t.Helper()
	select {
	case event := <-ch:
		if event == nil || len(event.QueryIDs) != 1 || event.QueryIDs[0] != queryID {
			t.Fatalf("event = %#v, want query id %q", event, queryID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for query id %q", queryID)
	}
}

func testState() *metadataState {
	return &metadataState{
		Tenant: &Tenant{ID: "00000000-0000-0000-0000-000000000001", Key: "test"},
		Object: &Object{ID: "00000000-0000-0000-0000-000000000002", TenantID: "00000000-0000-0000-0000-000000000001", NameSingular: "company", TableName: "company__000000000000", SchemaVersion: 3},
		Fields: map[string]*Field{
			"name": {
				ID:           "field-name",
				Name:         "name",
				Type:         FieldText,
				IsNullable:   true,
				IsSearchable: true,
				SearchWeight: "A",
				Columns:      []PhysicalColumn{{Name: "name__fieldname", SQLType: "text", Nullable: true}},
			},
			"stage": {
				ID:         "field-stage",
				Name:       "stage",
				Type:       FieldSelect,
				IsNullable: true,
				Columns:    []PhysicalColumn{{Name: "stage__fieldstage", SQLType: "text", Nullable: true}},
			},
			"arr": {
				ID:         "field-arr",
				Name:       "arr",
				Type:       FieldNumeric,
				IsNullable: true,
				Columns:    []PhysicalColumn{{Name: "arr__fieldarr", SQLType: "numeric", Nullable: true}},
			},
		},
	}
}

func relationTestState() *metadataState {
	state := &metadataState{
		Tenant: &Tenant{ID: "00000000-0000-0000-0000-000000000001", Key: "test"},
		Object: &Object{ID: "00000000-0000-0000-0000-000000000010", TenantID: "00000000-0000-0000-0000-000000000001", NameSingular: "deal", TableName: "deal__source", SchemaVersion: 1},
		Fields: map[string]*Field{
			"company": {
				ID:               "field-company",
				Name:             "company",
				Type:             FieldRelation,
				RelationObjectID: "00000000-0000-0000-0000-000000000002",
				Settings:         map[string]any{"relation_kind": string(RelationManyToOne)},
				Columns:          []PhysicalColumn{{Name: "company__fieldcompany", SQLType: "uuid", Nullable: true}},
			},
		},
	}
	state.Relations = map[string]*relationTarget{
		"company": {
			Object: &Object{ID: "00000000-0000-0000-0000-000000000002", TenantID: state.Tenant.ID, NameSingular: "company", TableName: "company__target", SchemaVersion: 1},
			Fields: testState().Fields,
		},
	}
	return state
}
