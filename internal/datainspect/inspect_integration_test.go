package datainspect

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pbrazdil/onlava/internal/objectstore"
	"github.com/pbrazdil/onlava/internal/testpostgres"
)

func TestBuildWithPostgres(t *testing.T) {
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer setupCancel()
	db, err := testpostgres.Start(setupCtx)
	if err != nil {
		t.Fatalf("PostgreSQL inspect test setup failed; start Docker or set %s: %v", testpostgres.EnvDatabaseURL, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := db.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate PostgreSQL testcontainer: %v", err)
		}
	})

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, db.URL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := objectstore.Open(ctx, pool, objectstore.Options{})
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	tenantKey := fmt.Sprintf("inspect_tenant_%d", time.Now().UnixNano())
	actor := objectstore.Actor{ID: "inspector"}
	if _, err := store.CreateObject(ctx, actor, objectstore.CreateObjectRequest{
		TenantKey:    tenantKey,
		TenantName:   "Inspect Tenant",
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
		t.Fatalf("CreateField: %v", err)
	}
	if _, err := store.CreateIndex(ctx, actor, "company", objectstore.CreateIndexRequest{
		TenantKey: tenantKey,
		Name:      "company_name",
		Fields:    []objectstore.IndexField{{Field: "name"}},
	}); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	resp, err := Build(ctx, Options{DatabaseURL: db.URL, TenantKey: tenantKey, ObjectName: "company"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if resp.SchemaVersion != schemaVersion {
		t.Fatalf("schema version = %q, want %q", resp.SchemaVersion, schemaVersion)
	}
	if len(resp.Tenants) != 1 || resp.Tenants[0].Key != tenantKey {
		t.Fatalf("tenants = %#v", resp.Tenants)
	}
	if len(resp.Objects) != 1 || resp.Objects[0].Name != "company" || len(resp.Objects[0].Fields) != 1 {
		t.Fatalf("objects = %#v", resp.Objects)
	}
	if len(resp.Objects[0].Indexes) != 1 || resp.Objects[0].Indexes[0].Name != "company_name" || !resp.Objects[0].Indexes[0].Physical.Exists || resp.Objects[0].Indexes[0].Physical.Drift {
		t.Fatalf("indexes = %#v", resp.Objects[0].Indexes)
	}
	if resp.Objects[0].PhysicalTable == "" || resp.Objects[0].Fields[0].Columns[0] == "" {
		t.Fatalf("missing physical names: %#v", resp.Objects[0])
	}
	if resp.Outbox.LatestSeq == 0 {
		t.Fatalf("outbox latest seq = 0, want metadata events")
	}
}
