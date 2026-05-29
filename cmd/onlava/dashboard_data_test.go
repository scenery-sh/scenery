package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pbrazdil/onlava/internal/datainspect"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/objectstore"
	"github.com/pbrazdil/onlava/internal/testpostgres"
)

func TestDashboardDataRPC(t *testing.T) {
	t.Parallel()

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer setupCancel()
	db, err := testpostgres.Start(setupCtx)
	if err != nil {
		t.Fatalf("PostgreSQL dashboard data test setup failed; start Docker or set %s: %v", testpostgres.EnvDatabaseURL, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := db.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate PostgreSQL testcontainer: %v", err)
		}
	})

	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".env"), []byte("DATABASE_URL="+db.URL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(db.URL)
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig: %v", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := objectstore.Open(ctx, pool, objectstore.Options{})
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	tenantKey := fmt.Sprintf("dashboard_data_tenant_%d", time.Now().UnixNano())
	actor := objectstore.Actor{ID: "test-user"}
	if _, err := store.CreateObject(ctx, actor, objectstore.CreateObjectRequest{
		TenantKey:    tenantKey,
		TenantName:   "Dashboard Data Tenant",
		NameSingular: "company",
		NamePlural:   "companies",
	}); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", objectstore.CreateFieldRequest{
		TenantKey: tenantKey,
		Name:      "name",
		Type:      objectstore.FieldText,
	}); err != nil {
		t.Fatalf("CreateField(name): %v", err)
	}
	if _, err := store.CreateField(ctx, actor, "company", objectstore.CreateFieldRequest{
		TenantKey: tenantKey,
		Name:      "stage",
		Type:      objectstore.FieldSelect,
		Options: []objectstore.FieldOptionRequest{
			{Value: "lead", Label: "Lead"},
			{Value: "won", Label: "Won"},
		},
	}); err != nil {
		t.Fatalf("CreateField(stage): %v", err)
	}
	if _, err := store.CreateRecord(ctx, actor, "company", objectstore.CreateRecordRequest{
		TenantKey: tenantKey,
		Values: objectstore.Record{
			"name":  "Acme",
			"stage": "won",
		},
	}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	server := newTestDashboardServer(t)
	if err := server.supervisor.store.UpsertApp(ctx, devdash.AppRecord{
		ID:        "app-test",
		Name:      "app-test",
		Root:      appRoot,
		Running:   true,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}

	inspectResult, err := server.dispatchRPC(ctx, "data/inspect", mustJSON(t, map[string]any{
		"app_id":     "app-test",
		"tenant_key": tenantKey,
	}))
	if err != nil {
		t.Fatalf("data/inspect: %v", err)
	}
	inspectPayload, ok := inspectResult.(datainspect.Response)
	if !ok {
		t.Fatalf("inspect result type = %T", inspectResult)
	}
	if len(inspectPayload.Tenants) != 1 || inspectPayload.Tenants[0].Key != tenantKey {
		t.Fatalf("inspect tenants = %#v", inspectPayload.Tenants)
	}
	if len(inspectPayload.Objects) != 1 || inspectPayload.Objects[0].Name != "company" {
		t.Fatalf("inspect objects = %#v", inspectPayload.Objects)
	}

	queryResult, err := server.dispatchRPC(ctx, "data/query-records", mustJSON(t, map[string]any{
		"app_id":     "app-test",
		"tenant_key": tenantKey,
		"object":     "company",
		"query": map[string]any{
			"select": []string{"name", "stage"},
			"filter": map[string]any{"op": "eq", "field": "stage", "value": "won"},
			"limit":  10,
		},
	}))
	if err != nil {
		t.Fatalf("data/query-records: %v", err)
	}
	page, ok := queryResult.(*objectstore.RecordPage)
	if !ok {
		t.Fatalf("query result type = %T", queryResult)
	}
	if len(page.Records) != 1 || page.Records[0]["name"] != "Acme" {
		encoded, _ := json.Marshal(page)
		t.Fatalf("query page = %s", encoded)
	}

	outboxResult, err := server.dispatchRPC(ctx, "data/outbox-events", mustJSON(t, map[string]any{
		"app_id":     "app-test",
		"tenant_key": tenantKey,
		"object":     "company",
		"limit":      5,
	}))
	if err != nil {
		t.Fatalf("data/outbox-events: %v", err)
	}
	events, ok := outboxResult.([]dataOutboxEventRecord)
	if !ok {
		t.Fatalf("outbox result type = %T", outboxResult)
	}
	if len(events) == 0 {
		t.Fatal("expected outbox events")
	}
	if events[0].TenantKey != tenantKey || events[0].Object != "company" {
		t.Fatalf("outbox event = %#v", events[0])
	}
}
