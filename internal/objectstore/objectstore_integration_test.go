package objectstore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresVerticalSlice(t *testing.T) {
	dsn := postgresTestDatabaseURL(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	store, err := Open(ctx, pool, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tenantKey := fmt.Sprintf("tenant_%d", time.Now().UnixNano())
	if err := cleanupPostgresTenant(ctx, pool, tenantKey); err != nil {
		t.Fatalf("pre-clean tenant %q: %v", tenantKey, err)
	}
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}

	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{
		TenantKey:    tenantKey,
		TenantName:   "Tenant",
		NameSingular: "company",
		NamePlural:   "companies",
	}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText, Searchable: true, SearchWeight: "A"}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{
		TenantKey:    tenantKey,
		Name:         "stage",
		Type:         FieldSelect,
		Searchable:   true,
		SearchWeight: "B",
		Options: []FieldOptionRequest{
			{Value: "lead", Label: "Lead"},
			{Value: "won", Label: "Won"},
		},
	}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "arr", Type: FieldNumeric}); err != nil {
		t.Fatalf("CreateField(arr): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "full_name", Type: FieldFullName, Searchable: true}); err != nil {
		t.Fatalf("CreateField(full_name): %v", err)
	}

	created, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{
		TenantKey: tenantKey,
		Values: Record{
			"name":      "Acme",
			"stage":     "lead",
			"arr":       42.5,
			"full_name": Record{"first_name": "Ada", "last_name": "Lovelace"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	recordID, _ := created.Record["id"].(string)
	if recordID == "" {
		t.Fatalf("created record missing id: %#v", created.Record)
	}
	if created.Event == nil || created.Event.Seq == 0 {
		t.Fatalf("created event missing seq: %#v", created.Event)
	}

	page, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"name", "stage", "arr", "full_name"},
			Filter: &Filter{Op: "contains", Field: "name", Value: "Ac"},
			Sort:   []Sort{{Field: "arr", Desc: true}},
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords: %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records len = %d, want 1: %#v", len(page.Records), page.Records)
	}
	if got := page.Records[0]["stage"]; got != "lead" {
		t.Fatalf("stage = %#v, want lead", got)
	}
	fullName, ok := page.Records[0]["full_name"].(Record)
	if !ok {
		if raw, ok := page.Records[0]["full_name"].(map[string]any); ok {
			fullName = Record(raw)
		}
	}
	if fullName["first_name"] != "Ada" || fullName["last_name"] != "Lovelace" {
		t.Fatalf("full_name = %#v", page.Records[0]["full_name"])
	}
	searchPage, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"name", "stage"},
			Filter: &Filter{Op: "search", Value: "lovelace"},
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords(search): %v", err)
	}
	if len(searchPage.Records) != 1 || searchPage.Records[0]["name"] != "Acme" {
		t.Fatalf("search records = %#v", searchPage.Records)
	}
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "search_documents")+` where tenant_id = $1`, created.Event.TenantID); got != 1 {
		t.Fatalf("search documents count = %d, want 1", got)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = store.ServeEvents(r.Context(), actor, w, r)
	}))
	defer server.Close()

	filterData, _ := json.Marshal(Filter{Op: "eq", Field: "stage", Value: "won"})
	streamURL := server.URL + "/events?tenant_key=" + url.QueryEscape(tenantKey) +
		"&object=company&query_id=won-companies&fields=name,stage&filter=" + url.QueryEscape(string(filterData))
	streamURL += "&after_seq=" + fmt.Sprint(created.Event.Seq)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if eventName, _, _, err := readSSEEvent(reader); err != nil || eventName != "ready" {
		t.Fatalf("first SSE event = %q, err %v; want ready", eventName, err)
	}

	updated, err := store.UpdateRecord(ctx, actor, "company", recordID, UpdateRecordRequest{
		TenantKey: tenantKey,
		Values:    Record{"stage": "won", "name": "Acme Labs"},
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if updated.Event == nil || updated.Event.Action != "updated" {
		t.Fatalf("updated event = %#v", updated.Event)
	}
	updatedSearch, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"name"},
			Filter: &Filter{Op: "search", Value: "labs"},
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords(search after update): %v", err)
	}
	if len(updatedSearch.Records) != 1 || updatedSearch.Records[0]["name"] != "Acme Labs" {
		t.Fatalf("updated search records = %#v", updatedSearch.Records)
	}

	eventName, eventData, _, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("read update SSE: %v", err)
	}
	if eventName != "data" {
		t.Fatalf("update SSE event name = %q, want data", eventName)
	}
	var live Event
	if err := json.Unmarshal([]byte(eventData), &live); err != nil {
		t.Fatalf("decode live event: %v\n%s", err, eventData)
	}
	if live.Seq != updated.Event.Seq || len(live.QueryIDs) != 1 || live.QueryIDs[0] != "won-companies" {
		t.Fatalf("live event = %#v, updated event = %#v", live, updated.Event)
	}
	if _, ok := live.After["arr"]; ok {
		t.Fatalf("live selected fields included arr: %#v", live.After)
	}
	cancel()

	replayURL := server.URL + "/events?tenant_key=" + url.QueryEscape(tenantKey) +
		"&object=company&query_id=replay&after_seq=" + fmt.Sprint(updated.Event.Seq-1) +
		"&filter=" + url.QueryEscape(string(filterData))
	replayCtx, replayCancel := context.WithCancel(ctx)
	defer replayCancel()
	replayReq, _ := http.NewRequestWithContext(replayCtx, http.MethodGet, replayURL, nil)
	replayResp, err := server.Client().Do(replayReq)
	if err != nil {
		t.Fatalf("open replay SSE: %v", err)
	}
	defer replayResp.Body.Close()
	replayReader := bufio.NewReader(replayResp.Body)
	eventName, eventData, _, err = readSSEEvent(replayReader)
	if err != nil {
		t.Fatalf("read replay SSE: %v", err)
	}
	if eventName != "data" {
		t.Fatalf("replay first event = %q, want data", eventName)
	}
	var replay Event
	if err := json.Unmarshal([]byte(eventData), &replay); err != nil {
		t.Fatalf("decode replay event: %v", err)
	}
	if replay.Seq != updated.Event.Seq {
		t.Fatalf("replay seq = %d, want %d", replay.Seq, updated.Event.Seq)
	}
	replayCancel()
	_ = replayResp.Body.Close()

	store.perms = rowFilterPermissions{filter: &Filter{Op: "eq", Field: "stage", Value: "won"}}
	permissionURL := server.URL + "/events?tenant_key=" + url.QueryEscape(tenantKey) +
		"&object=company&query_id=permission-filtered&fields=name,stage&after_seq=" + fmt.Sprint(updated.Event.Seq)
	permissionCtx, permissionCancel := context.WithCancel(ctx)
	defer permissionCancel()
	permissionReq, _ := http.NewRequestWithContext(permissionCtx, http.MethodGet, permissionURL, nil)
	permissionResp, err := server.Client().Do(permissionReq)
	if err != nil {
		t.Fatalf("open permission SSE: %v", err)
	}
	defer permissionResp.Body.Close()
	permissionReader := bufio.NewReader(permissionResp.Body)
	if eventName, _, _, err := readSSEEvent(permissionReader); err != nil || eventName != "ready" {
		t.Fatalf("permission first SSE event = %q, err %v; want ready", eventName, err)
	}
	movedOut, err := store.UpdateRecord(ctx, actor, "company", recordID, UpdateRecordRequest{
		TenantKey: tenantKey,
		Values:    Record{"stage": "lead"},
	})
	if err != nil {
		t.Fatalf("UpdateRecord(moved out): %v", err)
	}
	eventName, eventData, _, err = readSSEEvent(permissionReader)
	if err != nil {
		t.Fatalf("read permission SSE: %v", err)
	}
	if eventName != "data" {
		t.Fatalf("permission SSE event name = %q, want data", eventName)
	}
	var permissionLive Event
	if err := json.Unmarshal([]byte(eventData), &permissionLive); err != nil {
		t.Fatalf("decode permission event: %v", err)
	}
	if permissionLive.Seq != movedOut.Event.Seq || len(permissionLive.QueryIDs) != 1 || permissionLive.QueryIDs[0] != "permission-filtered" {
		t.Fatalf("permission live event = %#v, moved event = %#v", permissionLive, movedOut.Event)
	}
	permissionCancel()

	deleted, err := store.DeleteRecord(ctx, actor, "company", recordID, DeleteRecordRequest{TenantKey: tenantKey})
	if err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if deleted.Event == nil || deleted.Event.Action != "deleted" {
		t.Fatalf("delete event = %#v", deleted.Event)
	}
}

type rowFilterPermissions struct {
	AllowAllPermissions
	filter *Filter
}

func (p rowFilterPermissions) RowFilter(context.Context, Actor, ObjectRef) (*Filter, error) {
	return p.filter, nil
}

func TestPostgresBootstrapIdempotent(t *testing.T) {
	dsn := postgresTestDatabaseURL(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	for i := 0; i < 3; i++ {
		if _, err := Open(ctx, pool, Options{}); err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
	}
}

func TestPostgresCreateObjectAndFieldAreIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_idempotent_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}

	firstObject, err := store.CreateObject(ctx, actor, CreateObjectRequest{
		TenantKey:    tenantKey,
		TenantName:   "Tenant",
		NameSingular: "company",
		NamePlural:   "companies",
	})
	if err != nil {
		t.Fatalf("CreateObject(first): %v", err)
	}
	secondObject, err := store.CreateObject(ctx, actor, CreateObjectRequest{
		TenantKey:    tenantKey,
		TenantName:   "Tenant",
		NameSingular: "company",
		NamePlural:   "companies",
	})
	if err != nil {
		t.Fatalf("CreateObject(second): %v", err)
	}
	if secondObject.ID != firstObject.ID || secondObject.TableName != firstObject.TableName {
		t.Fatalf("second object = %#v, want same identity as %#v", secondObject, firstObject)
	}
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "objects")+` where tenant_id = $1`, firstObject.TenantID); got != 1 {
		t.Fatalf("object count = %d, want 1", got)
	}

	firstField, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldText})
	if err != nil {
		t.Fatalf("CreateField(first): %v", err)
	}
	secondField, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldText})
	if err != nil {
		t.Fatalf("CreateField(second): %v", err)
	}
	if secondField.ID != firstField.ID || len(secondField.Columns) != len(firstField.Columns) || secondField.Columns[0].Name != firstField.Columns[0].Name {
		t.Fatalf("second field = %#v, want same identity as %#v", secondField, firstField)
	}
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "fields")+` where tenant_id = $1 and object_id = $2`, firstField.TenantID, firstField.ObjectID); got != 1 {
		t.Fatalf("field count = %d, want 1", got)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldNumeric}); err == nil || !strings.Contains(err.Error(), "already exists with type") {
		t.Fatalf("CreateField(incompatible) error = %v, want type mismatch", err)
	}

	if _, err := pool.Exec(ctx, `alter table `+qualifiedIdent(RecordsSchema, firstObject.TableName)+` drop column `+quoteIdent(firstField.Columns[0].Name)); err != nil {
		t.Fatalf("drop physical column: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldText}); err == nil || !strings.Contains(err.Error(), "physical schema drift") {
		t.Fatalf("CreateField(after drift) error = %v, want drift detection", err)
	}
}

func TestPostgresCreateIndexAndCursorPagination(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_index_cursor_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldSelect, Options: []FieldOptionRequest{{Value: "lead"}, {Value: "won"}}}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	nullableFalse := false
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "arr", Type: FieldNumeric, Nullable: &nullableFalse}); err != nil {
		t.Fatalf("CreateField(arr): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "tags", Type: FieldMultiSelect}); err != nil {
		t.Fatalf("CreateField(tags): %v", err)
	}

	btree, err := store.CreateIndex(ctx, actor, "company", CreateIndexRequest{
		TenantKey: tenantKey,
		Name:      "company_stage_arr",
		Fields: []IndexField{
			{Field: "stage"},
			{Field: "arr", Desc: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateIndex(btree): %v", err)
	}
	if btree.PhysicalName == "" || btree.Method != IndexMethodBTree || len(btree.Fields) != 2 {
		t.Fatalf("btree index = %#v", btree)
	}
	if _, err := store.CreateIndex(ctx, actor, "company", CreateIndexRequest{
		TenantKey: tenantKey,
		Name:      "company_tags",
		Method:    IndexMethodGIN,
		Fields:    []IndexField{{Field: "tags"}},
	}); err != nil {
		t.Fatalf("CreateIndex(gin): %v", err)
	}
	indexes, err := store.ListIndexes(ctx, actor, "company", ListIndexesRequest{TenantKey: tenantKey})
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(indexes) != 2 {
		t.Fatalf("indexes len = %d, want 2: %#v", len(indexes), indexes)
	}
	state, err := store.loadState(ctx, tenantKey, "company")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if err := store.verifyIndex(ctx, pool, state.Object.TableName, btree.PhysicalName); err != nil {
		t.Fatalf("verifyIndex: %v", err)
	}
	if got := countRows(t, pool, `
		select count(*)
		from `+qualifiedIdent(MetadataSchema, "schema_migrations")+` m
		join `+qualifiedIdent(MetadataSchema, "tenants")+` t on t.id = m.tenant_id
		where t.key = $1 and m.status = 'applied' and m.ddl::text like '%create index%'
	`, tenantKey); got < 2 {
		t.Fatalf("index migration count = %d, want at least 2", got)
	}

	for i, item := range []struct {
		name  string
		stage string
		arr   float64
	}{
		{name: "Acme", stage: "won", arr: 10},
		{name: "Beta", stage: "won", arr: 20},
		{name: "Core", stage: "won", arr: 30},
	} {
		resp, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{
			TenantKey: tenantKey,
			Values: Record{
				"name":  item.name,
				"stage": item.stage,
				"arr":   item.arr,
				"tags":  []string{"customer", fmt.Sprintf("rank_%d", i)},
			},
		})
		if err != nil {
			t.Fatalf("CreateRecord(%s): %v", item.name, err)
		}
		if resp.Record["id"] == "" {
			t.Fatalf("created record missing id: %#v", resp.Record)
		}
	}
	first, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"name", "arr"},
			Filter: &Filter{Op: "eq", Field: "stage", Value: "won"},
			Sort:   []Sort{{Field: "arr"}},
			Limit:  2,
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords(first): %v", err)
	}
	if len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, want 2 records and next cursor", first)
	}
	if first.Records[0]["name"] != "Acme" || first.Records[1]["name"] != "Beta" {
		t.Fatalf("first page records = %#v", first.Records)
	}
	second, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"name", "arr"},
			Filter: &Filter{Op: "eq", Field: "stage", Value: "won"},
			Sort:   []Sort{{Field: "arr"}},
			Limit:  2,
			Cursor: first.NextCursor,
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords(second): %v", err)
	}
	if len(second.Records) != 1 || second.NextCursor != "" || second.Records[0]["name"] != "Core" {
		t.Fatalf("second page = %#v, want Core and no next cursor", second)
	}
	if _, err := store.QueryRecords(ctx, actor, "company", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Sort:   []Sort{{Field: "name"}},
			Limit:  2,
			Cursor: first.NextCursor,
		},
	}); err == nil || !strings.Contains(err.Error(), "cursor sort shape") {
		t.Fatalf("QueryRecords(cursor mismatch) error = %v, want cursor sort shape mismatch", err)
	}
}

func TestPostgresRelationFieldsAndQueries(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_relation_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject(company): %v", err)
	}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "deal", NamePlural: "deals"}); err != nil {
		t.Fatalf("CreateObject(deal): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(company.name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldSelect, Options: []FieldOptionRequest{{Value: "customer"}, {Value: "lead"}}}); err != nil {
		t.Fatalf("CreateField(company.stage): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "deal", CreateFieldRequest{TenantKey: tenantKey, Name: "title", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(deal.title): %v", err)
	}
	companyField, err := store.CreateField(ctx, actor, "deal", CreateFieldRequest{
		TenantKey:      tenantKey,
		Name:           "company",
		Type:           FieldRelation,
		RelationObject: "company",
		Relation:       RelationSettings{Kind: RelationManyToOne, OnDelete: RelationDeleteRestrict},
	})
	if err != nil {
		t.Fatalf("CreateField(deal.company): %v", err)
	}
	relatedField, err := store.CreateField(ctx, actor, "deal", CreateFieldRequest{
		TenantKey:      tenantKey,
		Name:           "related_companies",
		Type:           FieldRelation,
		RelationObject: "company",
		Relation:       RelationSettings{Kind: RelationManyToMany},
	})
	if err != nil {
		t.Fatalf("CreateField(deal.related_companies): %v", err)
	}
	if relationKindForField(companyField) != RelationManyToOne || companyField.RelationObjectID == "" || len(companyField.Columns) != 1 {
		t.Fatalf("many-to-one relation field = %#v", companyField)
	}
	if relationKindForField(relatedField) != RelationManyToMany || len(relatedField.Columns) != 0 || stringSetting(relatedField.Settings, "join_table_name") == "" {
		t.Fatalf("many-to-many relation field = %#v", relatedField)
	}
	dealState, err := store.loadState(ctx, tenantKey, "deal")
	if err != nil {
		t.Fatalf("loadState(deal): %v", err)
	}
	if err := store.verifyRelationField(ctx, pool, dealState.Object.TableName, dealState.Fields["company"]); err != nil {
		t.Fatalf("verify many-to-one relation: %v", err)
	}
	if err := store.verifyRelationField(ctx, pool, dealState.Object.TableName, dealState.Fields["related_companies"]); err != nil {
		t.Fatalf("verify many-to-many relation: %v", err)
	}

	company, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{
		TenantKey: tenantKey,
		Values: Record{
			"name":  "Acme",
			"stage": "customer",
		},
	})
	if err != nil {
		t.Fatalf("CreateRecord(company): %v", err)
	}
	companyID, _ := company.Record["id"].(string)
	if companyID == "" {
		t.Fatalf("company id missing: %#v", company.Record)
	}
	if _, err := store.CreateRecord(ctx, actor, "deal", CreateRecordRequest{
		TenantKey: tenantKey,
		Values: Record{
			"title":   "Expansion",
			"company": "00000000-0000-0000-0000-000000000999",
		},
	}); err == nil {
		t.Fatalf("CreateRecord(deal invalid FK) succeeded, want foreign key error")
	}
	if _, err := store.CreateRecord(ctx, actor, "deal", CreateRecordRequest{
		TenantKey: tenantKey,
		Values: Record{
			"title":   "Expansion",
			"company": companyID,
		},
	}); err != nil {
		t.Fatalf("CreateRecord(deal): %v", err)
	}
	page, err := store.QueryRecords(ctx, actor, "deal", QueryRecordsRequest{
		TenantKey: tenantKey,
		Query: Query{
			Select: []string{"title", "company.name"},
			Filter: &Filter{Op: "eq", Field: "company.stage", Value: "customer"},
			Sort:   []Sort{{Field: "company.name"}},
			Limit:  10,
		},
	})
	if err != nil {
		t.Fatalf("QueryRecords(relation path): %v", err)
	}
	if len(page.Records) != 1 || page.Records[0]["title"] != "Expansion" || page.Records[0]["company.name"] != "Acme" {
		t.Fatalf("relation query page = %#v", page)
	}
}

func TestPostgresSavedViews(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_view_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldSelect, Options: []FieldOptionRequest{{Value: "lead"}, {Value: "won"}}}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	store.perms = denyWriteObjectPermissions{}
	if _, err := store.CreateView(ctx, actor, "company", CreateViewRequest{
		TenantKey: tenantKey,
		Name:      "denied_view",
		Columns:   []string{"name"},
	}); err == nil || !strings.Contains(err.Error(), "write denied") {
		t.Fatalf("CreateView(write denied) error = %v, want write denied", err)
	}
	store.perms = AllowAllPermissions{}
	if _, err := store.CreateView(ctx, actor, "company", CreateViewRequest{
		TenantKey:  tenantKey,
		Name:       "won_companies",
		Columns:    []string{"name", "stage"},
		Filter:     &Filter{Op: "eq", Field: "stage", Value: "won"},
		Sort:       []Sort{{Field: "name"}},
		Limit:      10,
		Visibility: ViewVisibilityShared,
	}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}
	if _, err := store.CreateView(ctx, actor, "company", CreateViewRequest{
		TenantKey: tenantKey,
		Name:      "bad_view",
		Columns:   []string{"missing"},
	}); err == nil {
		t.Fatalf("CreateView(invalid) succeeded, want validation error")
	}
	for _, item := range []struct {
		name  string
		stage string
	}{
		{name: "Acme", stage: "won"},
		{name: "Beta", stage: "lead"},
	} {
		if _, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{
			TenantKey: tenantKey,
			Values: Record{
				"name":  item.name,
				"stage": item.stage,
			},
		}); err != nil {
			t.Fatalf("CreateRecord(%s): %v", item.name, err)
		}
	}
	views, err := store.ListViews(ctx, actor, "company", ListViewsRequest{TenantKey: tenantKey})
	if err != nil {
		t.Fatalf("ListViews: %v", err)
	}
	if len(views) != 1 || views[0].Name != "won_companies" || views[0].Visibility != ViewVisibilityShared {
		t.Fatalf("views = %#v", views)
	}
	page, err := store.QueryView(ctx, actor, "company", "won_companies", QueryViewRequest{TenantKey: tenantKey})
	if err != nil {
		t.Fatalf("QueryView: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0]["name"] != "Acme" {
		t.Fatalf("QueryView page = %#v", page)
	}
	store.perms = denyWriteObjectPermissions{}
	if _, err := store.UpdateView(ctx, actor, "company", "won_companies", UpdateViewRequest{
		TenantKey: tenantKey,
		Columns:   []string{"name"},
	}); err == nil || !strings.Contains(err.Error(), "write denied") {
		t.Fatalf("UpdateView(write denied) error = %v, want write denied", err)
	}
	store.perms = AllowAllPermissions{}
	updated, err := store.UpdateView(ctx, actor, "company", "won_companies", UpdateViewRequest{
		TenantKey: tenantKey,
		Name:      "all_companies",
		Columns:   []string{"name"},
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("UpdateView: %v", err)
	}
	if updated.Name != "all_companies" || len(updated.Columns) != 1 {
		t.Fatalf("updated view = %#v", updated)
	}
	store.perms = denyWriteObjectPermissions{}
	if err := store.DeleteView(ctx, actor, "company", "all_companies", DeleteViewRequest{TenantKey: tenantKey}); err == nil || !strings.Contains(err.Error(), "write denied") {
		t.Fatalf("DeleteView(write denied) error = %v, want write denied", err)
	}
	store.perms = AllowAllPermissions{}
	if err := store.DeleteView(ctx, actor, "company", "all_companies", DeleteViewRequest{TenantKey: tenantKey}); err != nil {
		t.Fatalf("DeleteView: %v", err)
	}
	views, err = store.ListViews(ctx, actor, "company", ListViewsRequest{TenantKey: tenantKey})
	if err != nil {
		t.Fatalf("ListViews(after delete): %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("views after delete = %#v", views)
	}
}

func TestPostgresConcurrentCreatesAreIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_concurrent_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}

	objectIDs := runConcurrent(t, 8, func() (string, error) {
		obj, err := store.CreateObject(ctx, actor, CreateObjectRequest{
			TenantKey:    tenantKey,
			TenantName:   "Tenant",
			NameSingular: "company",
			NamePlural:   "companies",
		})
		if err != nil {
			return "", err
		}
		return obj.ID, nil
	})
	if got := uniqueStrings(objectIDs); len(got) != 1 {
		t.Fatalf("object ids = %#v, want one unique id", got)
	}
	objectID := objectIDs[0]
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "objects")+` where id = $1`, objectID); got != 1 {
		t.Fatalf("object count = %d, want 1", got)
	}

	fieldIDs := runConcurrent(t, 8, func() (string, error) {
		field, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldText})
		if err != nil {
			return "", err
		}
		return field.ID, nil
	})
	if got := uniqueStrings(fieldIDs); len(got) != 1 {
		t.Fatalf("field ids = %#v, want one unique id", got)
	}
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "fields")+` where object_id = $1`, objectID); got != 1 {
		t.Fatalf("field count = %d, want 1", got)
	}
}

func TestPostgresFailedMigrationIsRecordedAndRetryCanSucceed(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_failed_migration_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{TenantKey: tenantKey, Values: Record{"name": "Acme"}}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	nullableFalse := false
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "required", Type: FieldText, Nullable: &nullableFalse}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("CreateField(required not null) error = %v, want DDL failure", err)
	}
	if got := countRows(t, pool, `
		select count(*)
		from `+qualifiedIdent(MetadataSchema, "schema_migrations")+` m
		join `+qualifiedIdent(MetadataSchema, "tenants")+` t on t.id = m.tenant_id
		where t.key = $1 and m.status = 'failed' and m.error <> ''
	`, tenantKey); got == 0 {
		t.Fatalf("failed migration count = 0, want at least 1")
	}

	nullableTrue := true
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "required", Type: FieldText, Nullable: &nullableTrue}); err != nil {
		t.Fatalf("CreateField(required retry nullable): %v", err)
	}
}

func TestPostgresServeEventsHeartbeat(t *testing.T) {
	oldInterval := sseHeartbeatInterval
	sseHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = oldInterval })

	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_heartbeat_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	afterSeq := countRows(t, pool, `select coalesce(max(seq), 0) from `+qualifiedIdent(MetadataSchema, "outbox_events"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = store.ServeEvents(r.Context(), actor, w, r)
	}))
	defer server.Close()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamURL := server.URL + "/events?tenant_key=" + url.QueryEscape(tenantKey) + "&object=company&query_id=heartbeat&after_seq=" + fmt.Sprint(afterSeq)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if eventName, _, _, err := readSSEEvent(reader); err != nil || eventName != "ready" {
		t.Fatalf("first SSE event = %q, err %v; want ready", eventName, err)
	}

	lines := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errs <- err
				return
			}
			if strings.HasPrefix(strings.TrimSpace(line), ": heartbeat") {
				lines <- line
				return
			}
		}
	}()
	select {
	case <-lines:
	case err := <-errs:
		t.Fatalf("read heartbeat: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat")
	}
}

func TestPostgresTriggerBackedOutboxCapturesDirectSQL(t *testing.T) {
	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_trigger_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldSelect, Options: []FieldOptionRequest{{Value: "lead"}, {Value: "won"}}}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "full_name", Type: FieldFullName}); err != nil {
		t.Fatalf("CreateField(full_name): %v", err)
	}
	enabled, err := store.EnableOutboxTriggers(ctx, actor, tenantKey, "company")
	if err != nil {
		t.Fatalf("EnableOutboxTriggers: %v", err)
	}
	if !enabled.OutboxTriggersEnabled {
		t.Fatalf("enabled object = %#v, want outbox triggers enabled", enabled)
	}
	state, err := store.loadState(ctx, tenantKey, "company")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	present, err := store.outboxTriggerPresent(ctx, pool, state.Object.TableName, outboxTriggerName(state.Object.ID))
	if err != nil {
		t.Fatalf("outboxTriggerPresent: %v", err)
	}
	if !present {
		t.Fatalf("trigger %s not present on table %s", outboxTriggerName(state.Object.ID), state.Object.TableName)
	}

	nameColumn := testFieldColumn(t, state, "name", "")
	stageColumn := testFieldColumn(t, state, "stage", "")
	firstNameColumn := testFieldColumn(t, state, "full_name", "first_name")
	lastNameColumn := testFieldColumn(t, state, "full_name", "last_name")
	beforeSeq := countRows(t, pool, `select coalesce(max(seq), 0) from `+qualifiedIdent(MetadataSchema, "outbox_events"))
	recordID, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID: %v", err)
	}
	_, err = pool.Exec(ctx, `insert into `+qualifiedIdent(RecordsSchema, state.Object.TableName)+`
		(`+quoteIdentList([]string{"id", "tenant_id", "created_at", "updated_at", nameColumn, stageColumn, firstNameColumn, lastNameColumn})+`)
		values ($1, $2, now(), now(), $3, $4, $5, $6)
	`, recordID, state.Tenant.ID, "Acme", "lead", "Ada", "Lovelace")
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}
	events, err := store.eventsAfter(ctx, beforeSeq, map[string]bool{state.Tenant.ID: true}, 10)
	if err != nil {
		t.Fatalf("eventsAfter(insert): %v", err)
	}
	insertEvent := lastEventForRecord(events, recordID)
	if insertEvent == nil {
		t.Fatalf("insert event for record %s not found in %#v", recordID, events)
	}
	if insertEvent.Action != "created" || insertEvent.ActorID != "" || insertEvent.After["name"] != "Acme" || insertEvent.After["stage"] != "lead" {
		t.Fatalf("insert event = %#v", insertEvent)
	}
	fullName, ok := insertEvent.After["full_name"].(map[string]any)
	if !ok || fullName["first_name"] != "Ada" || fullName["last_name"] != "Lovelace" {
		t.Fatalf("insert full_name = %#v", insertEvent.After["full_name"])
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := setOutboxTxContext(ctx, tx, Actor{ID: "dbstudio"}, false); err != nil {
		t.Fatalf("setOutboxTxContext: %v", err)
	}
	if _, err := tx.Exec(ctx, `update `+qualifiedIdent(RecordsSchema, state.Object.TableName)+`
		set `+quoteIdent(stageColumn)+` = $1, updated_at = now()
		where id = $2
	`, "won", recordID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("direct update: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	events, err = store.eventsAfter(ctx, insertEvent.Seq, map[string]bool{state.Tenant.ID: true}, 10)
	if err != nil {
		t.Fatalf("eventsAfter(update): %v", err)
	}
	updateEvent := lastEventForRecord(events, recordID)
	if updateEvent == nil {
		t.Fatalf("update event for record %s not found in %#v", recordID, events)
	}
	if updateEvent.Action != "updated" || updateEvent.ActorID != "dbstudio" || updateEvent.Before["stage"] != "lead" || updateEvent.After["stage"] != "won" {
		t.Fatalf("update event = %#v", updateEvent)
	}
	if !stringInSlice(updateEvent.ChangedFields, "stage") {
		t.Fatalf("update changed fields = %#v, want stage", updateEvent.ChangedFields)
	}

	beforeExplicit := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "outbox_events")+` where tenant_id = $1`, state.Tenant.ID)
	created, err := store.CreateRecord(ctx, actor, "company", CreateRecordRequest{TenantKey: tenantKey, Values: Record{"name": "Explicit", "stage": "won"}})
	if err != nil {
		t.Fatalf("CreateRecord explicit: %v", err)
	}
	afterExplicit := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "outbox_events")+` where tenant_id = $1`, state.Tenant.ID)
	if afterExplicit != beforeExplicit+1 {
		t.Fatalf("explicit outbox count = %d, want %d", afterExplicit, beforeExplicit+1)
	}
	if got := countRows(t, pool, `select count(*) from `+qualifiedIdent(MetadataSchema, "outbox_events")+` where record_id = $1`, created.Record["id"]); got != 1 {
		t.Fatalf("explicit record outbox events = %d, want 1", got)
	}

	_, err = pool.Exec(ctx, `delete from `+qualifiedIdent(RecordsSchema, state.Object.TableName)+` where id = $1`, recordID)
	if err != nil {
		t.Fatalf("direct delete: %v", err)
	}
	events, err = store.eventsAfter(ctx, updateEvent.Seq, map[string]bool{state.Tenant.ID: true}, 20)
	if err != nil {
		t.Fatalf("eventsAfter(delete): %v", err)
	}
	deleteEvent := lastEventForRecord(events, recordID)
	if deleteEvent == nil || deleteEvent.Action != "deleted" || deleteEvent.Before["name"] != "Acme" {
		t.Fatalf("delete event = %#v", deleteEvent)
	}
}

func TestPostgresTriggerBackedOutboxFeedsSSE(t *testing.T) {
	oldPollInterval := sseOutboxPollInterval
	sseOutboxPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { sseOutboxPollInterval = oldPollInterval })

	ctx := context.Background()
	pool, store := openPostgresStore(t)
	tenantKey := fmt.Sprintf("tenant_trigger_sse_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := cleanupPostgresTenant(context.Background(), pool, tenantKey); err != nil {
			t.Errorf("cleanup tenant %q: %v", tenantKey, err)
		}
	})
	actor := Actor{ID: "tester"}
	if _, err := store.CreateObject(ctx, actor, CreateObjectRequest{TenantKey: tenantKey, TenantName: "Tenant", NameSingular: "company", NamePlural: "companies"}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "name", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", CreateFieldRequest{TenantKey: tenantKey, Name: "stage", Type: FieldText}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	if _, err := store.EnableOutboxTriggers(ctx, actor, tenantKey, "company"); err != nil {
		t.Fatalf("EnableOutboxTriggers: %v", err)
	}
	state, err := store.loadState(ctx, tenantKey, "company")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	afterSeq := countRows(t, pool, `select coalesce(max(seq), 0) from `+qualifiedIdent(MetadataSchema, "outbox_events"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = store.ServeEvents(r.Context(), actor, w, r)
	}))
	defer server.Close()

	filterData, _ := json.Marshal(Filter{Op: "eq", Field: "stage", Value: "won"})
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streamURL := server.URL + "/events?tenant_key=" + url.QueryEscape(tenantKey) +
		"&object=company&query_id=triggered&fields=name,stage&filter=" + url.QueryEscape(string(filterData)) +
		"&after_seq=" + fmt.Sprint(afterSeq)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if eventName, _, _, err := readSSEEvent(reader); err != nil || eventName != "ready" {
		t.Fatalf("first SSE event = %q, err %v; want ready", eventName, err)
	}

	recordID, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID: %v", err)
	}
	_, err = pool.Exec(ctx, `insert into `+qualifiedIdent(RecordsSchema, state.Object.TableName)+`
		(`+quoteIdentList([]string{"id", "tenant_id", "created_at", "updated_at", testFieldColumn(t, state, "name", ""), testFieldColumn(t, state, "stage", "")})+`)
		values ($1, $2, now(), now(), $3, $4)
	`, recordID, state.Tenant.ID, "SSE Corp", "won")
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}
	eventName, eventData, _, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("read trigger SSE: %v", err)
	}
	if eventName != "data" {
		t.Fatalf("trigger SSE event = %q, want data", eventName)
	}
	var live Event
	if err := json.Unmarshal([]byte(eventData), &live); err != nil {
		t.Fatalf("decode live event: %v", err)
	}
	if live.RecordID != recordID || live.Action != "created" || live.After["stage"] != "won" || len(live.QueryIDs) != 1 || live.QueryIDs[0] != "triggered" {
		t.Fatalf("live trigger event = %#v", live)
	}
}

func openPostgresStore(t *testing.T) (*pgxpool.Pool, *Store) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, postgresTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := Open(ctx, pool, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return pool, store
}

func runConcurrent(t *testing.T, n int, fn func() (string, error)) []string {
	t.Helper()
	start := make(chan struct{})
	values := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			values[i], errs[i] = fn()
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent call %d: %v", i, err)
		}
	}
	return values
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count query failed: %v\n%s", err, query)
	}
	return count
}

func testFieldColumn(t *testing.T, state *metadataState, fieldName, part string) string {
	t.Helper()
	field := state.Fields[fieldName]
	if field == nil {
		t.Fatalf("field %s not found", fieldName)
	}
	for _, column := range field.Columns {
		if column.Part == part {
			return column.Name
		}
	}
	t.Fatalf("field %s part %q not found in %#v", fieldName, part, field.Columns)
	return ""
}

func lastEventForRecord(events []*Event, recordID string) *Event {
	var out *Event
	for _, event := range events {
		if event.RecordID == recordID {
			out = event
		}
	}
	return out
}

func stringInSlice(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type denyWriteObjectPermissions struct {
	AllowAllPermissions
}

func (denyWriteObjectPermissions) CanWriteObject(context.Context, Actor, ObjectRef) error {
	return errors.New("write denied")
}

func cleanupPostgresTenant(ctx context.Context, pool *pgxpool.Pool, tenantKey string) error {
	rows, err := pool.Query(ctx, `
		select o.table_name
		from `+qualifiedIdent(MetadataSchema, "objects")+` o
		join `+qualifiedIdent(MetadataSchema, "tenants")+` t on t.id = o.tenant_id
		where t.key = $1
		union
		select f.settings->>'join_table_name'
		from `+qualifiedIdent(MetadataSchema, "fields")+` f
		join `+qualifiedIdent(MetadataSchema, "tenants")+` t on t.id = f.tenant_id
		where t.key = $1 and coalesce(f.settings->>'join_table_name', '') <> ''
	`, tenantKey)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return err
	}
	defer rows.Close()
	var tableNames []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return err
		}
		tableNames = append(tableNames, tableName)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var tenantID string
	err = pool.QueryRow(ctx, `select id::text from `+qualifiedIdent(MetadataSchema, "tenants")+` where key = $1`, tenantKey).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err == nil && tenantID != "" {
		if _, err := pool.Exec(ctx, `delete from `+qualifiedIdent(MetadataSchema, "outbox_events")+` where tenant_id = $1`, tenantID); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `delete from `+qualifiedIdent(MetadataSchema, "schema_migrations")+` where tenant_id = $1`, tenantID); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `delete from `+qualifiedIdent(MetadataSchema, "tenants")+` where id = $1`, tenantID); err != nil {
			return err
		}
	}
	for _, tableName := range tableNames {
		if _, err := pool.Exec(ctx, `drop table if exists `+qualifiedIdent(RecordsSchema, tableName)+` cascade`); err != nil {
			return err
		}
	}
	return nil
}

func readSSEEvent(r *bufio.Reader) (eventName, data, id string, err error) {
	for {
		line, readErr := r.ReadString('\n')
		if readErr != nil {
			return "", "", "", readErr
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if eventName != "" || data != "" || id != "" {
				return eventName, data, id, nil
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data != "" {
				data += "\n"
			}
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
}
