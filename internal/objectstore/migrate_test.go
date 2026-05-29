package objectstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWithMigrationTxRetriesDeadlockFromMigrationBody(t *testing.T) {
	db := &migrationRetryDB{}
	store := &Store{
		db: db,
		now: func() time.Time {
			return time.Unix(0, 0).UTC()
		},
	}

	attempts := 0
	err := store.withMigrationTx(context.Background(),
		"tenant-one",
		"tenant-1",
		"object-1",
		"migration-1",
		1,
		2,
		[]string{`alter table "onlava_data_records"."company" add column "name" text`},
		"",
		func(pgxTx) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("apply field migration: %w", &pgconn.PgError{Code: "40P01", Message: "deadlock detected"})
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("withMigrationTx() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(db.txs) != 2 {
		t.Fatalf("transactions = %d, want 2", len(db.txs))
	}
	first := db.txs[0]
	if first.committed {
		t.Fatal("first deadlocked attempt committed; want full rollback")
	}
	if !first.rolledBack {
		t.Fatal("first deadlocked attempt was not rolled back")
	}
	if containsSQL(first.execs, "rollback to savepoint") {
		t.Fatalf("deadlocked attempt used migration savepoint rollback: %#v", first.execs)
	}
	second := db.txs[1]
	if !second.committed {
		t.Fatal("second attempt did not commit")
	}
	if !containsSQL(second.execs, "release savepoint") {
		t.Fatalf("second attempt did not release migration savepoint: %#v", second.execs)
	}
}

func TestWithMigrationTxUsesDeterministicFieldDDLLockOrder(t *testing.T) {
	db := &migrationRetryDB{}
	store := &Store{
		db: db,
		now: func() time.Time {
			return time.Unix(0, 0).UTC()
		},
	}

	err := store.withMigrationTx(context.Background(),
		"tenant-one",
		"tenant-1",
		"object-1",
		"migration-1",
		1,
		2,
		[]string{`alter table "onlava_data_records"."company" add column "name" text`},
		"",
		func(pgxTx) error { return nil },
	)
	if err != nil {
		t.Fatalf("withMigrationTx() error = %v", err)
	}
	if len(db.txs) != 1 {
		t.Fatalf("transactions = %d, want 1", len(db.txs))
	}
	got := db.txs[0].advisoryLockKeys()
	want := []int64{
		advisoryLockKey("objectstore", metadataBootstrapLockName),
		advisoryLockKey("objectstore", tenantSchemaMigrationLockName, "tenant-one"),
		advisoryLockKey("objectstore", tenantRecordSchemaLockName, "tenant-one"),
		advisoryLockKey("objectstore", objectSchemaMigrationLockName, "tenant-1", "object-1"),
	}
	if !equalInt64Slices(got, want) {
		t.Fatalf("advisory lock keys = %#v, want %#v", got, want)
	}
	if got[1] == advisoryLockKey("objectstore", tenantSchemaMigrationLockName, "tenant-two") {
		t.Fatalf("tenant schema lock key is not tenant-scoped")
	}
	if got[2] == advisoryLockKey("objectstore", tenantRecordSchemaLockName, "tenant-two") {
		t.Fatalf("tenant record schema lock key is not tenant-scoped")
	}
}

func TestEnsureTenantUsesDeterministicDDLLockOrder(t *testing.T) {
	db := &migrationRetryDB{}
	now := time.Unix(1, 0).UTC()
	store := &Store{
		db: db,
		now: func() time.Time {
			return now
		},
	}

	tenant, err := store.EnsureTenant(context.Background(), "tenant_one", "Tenant One")
	if err != nil {
		t.Fatalf("EnsureTenant() error = %v", err)
	}
	if tenant.Key != "tenant_one" || tenant.Name != "Tenant One" {
		t.Fatalf("tenant = %#v", tenant)
	}
	if len(db.txs) != 1 {
		t.Fatalf("transactions = %d, want 1", len(db.txs))
	}
	got := db.txs[0].advisoryLockKeys()
	want := []int64{
		advisoryLockKey("objectstore", metadataBootstrapLockName),
		advisoryLockKey("objectstore", tenantSchemaMigrationLockName, "tenant_one"),
	}
	if !equalInt64Slices(got, want) {
		t.Fatalf("advisory lock keys = %#v, want %#v", got, want)
	}
	if firstInsert := firstSQLIndex(db.txs[0].execs, "insert into"); firstInsert < len(want) {
		t.Fatalf("tenant row was upserted before DDL locks were acquired: %#v", db.txs[0].execs)
	}
}

func equalInt64Slices(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsSQL(stmts []string, fragment string) bool {
	for _, stmt := range stmts {
		if strings.Contains(strings.ToLower(stmt), fragment) {
			return true
		}
	}
	return false
}

func firstSQLIndex(stmts []string, fragment string) int {
	fragment = strings.ToLower(fragment)
	for i, stmt := range stmts {
		if strings.Contains(strings.ToLower(stmt), fragment) {
			return i
		}
	}
	return -1
}

type migrationRetryDB struct {
	txs []*migrationRetryTx
}

func (db *migrationRetryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("unexpected migrationRetryDB Exec")
}

func (db *migrationRetryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected migrationRetryDB Query")
}

func (db *migrationRetryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("unexpected migrationRetryDB QueryRow")
}

func (db *migrationRetryDB) Begin(context.Context) (pgx.Tx, error) {
	tx := &migrationRetryTx{}
	db.txs = append(db.txs, tx)
	return tx, nil
}

type migrationRetryTx struct {
	execs      []string
	execArgs   [][]any
	committed  bool
	rolledBack bool
}

func (tx *migrationRetryTx) Begin(context.Context) (pgx.Tx, error) {
	panic("unexpected migrationRetryTx Begin")
}

func (tx *migrationRetryTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *migrationRetryTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

func (tx *migrationRetryTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("unexpected migrationRetryTx CopyFrom")
}

func (tx *migrationRetryTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("unexpected migrationRetryTx SendBatch")
}

func (tx *migrationRetryTx) LargeObjects() pgx.LargeObjects {
	panic("unexpected migrationRetryTx LargeObjects")
}

func (tx *migrationRetryTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("unexpected migrationRetryTx Prepare")
}

func (tx *migrationRetryTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, sql)
	tx.execArgs = append(tx.execArgs, append([]any(nil), args...))
	return pgconn.CommandTag{}, nil
}

func (tx *migrationRetryTx) advisoryLockKeys() []int64 {
	var keys []int64
	for i, sql := range tx.execs {
		if !strings.Contains(sql, "pg_advisory_xact_lock") {
			continue
		}
		if len(tx.execArgs[i]) != 1 {
			continue
		}
		key, ok := tx.execArgs[i][0].(int64)
		if !ok {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func (tx *migrationRetryTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected migrationRetryTx Query")
}

func (tx *migrationRetryTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.execs = append(tx.execs, sql)
	tx.execArgs = append(tx.execArgs, append([]any(nil), args...))
	if strings.Contains(sql, `insert into `+qualifiedIdent(MetadataSchema, "tenants")) {
		return migrationRetryRow{
			tenant: &Tenant{
				ID:        fmt.Sprint(args[0]),
				Key:       fmt.Sprint(args[1]),
				Name:      fmt.Sprint(args[2]),
				CreatedAt: args[3].(time.Time),
				UpdatedAt: args[3].(time.Time),
			},
		}
	}
	return migrationRetryRow{version: 1}
}

type migrationRetryRow struct {
	version int64
	tenant  *Tenant
}

func (row migrationRetryRow) Scan(dest ...any) error {
	if row.tenant != nil {
		if len(dest) != 5 {
			return fmt.Errorf("Scan destination count = %d, want 5", len(dest))
		}
		id, ok := dest[0].(*string)
		if !ok {
			return fmt.Errorf("Scan destination 0 has type %T, want *string", dest[0])
		}
		key, ok := dest[1].(*string)
		if !ok {
			return fmt.Errorf("Scan destination 1 has type %T, want *string", dest[1])
		}
		name, ok := dest[2].(*string)
		if !ok {
			return fmt.Errorf("Scan destination 2 has type %T, want *string", dest[2])
		}
		createdAt, ok := dest[3].(*time.Time)
		if !ok {
			return fmt.Errorf("Scan destination 3 has type %T, want *time.Time", dest[3])
		}
		updatedAt, ok := dest[4].(*time.Time)
		if !ok {
			return fmt.Errorf("Scan destination 4 has type %T, want *time.Time", dest[4])
		}
		*id = row.tenant.ID
		*key = row.tenant.Key
		*name = row.tenant.Name
		*createdAt = row.tenant.CreatedAt
		*updatedAt = row.tenant.UpdatedAt
		return nil
	}
	if len(dest) != 1 {
		return fmt.Errorf("Scan destination count = %d, want 1", len(dest))
	}
	version, ok := dest[0].(*int64)
	if !ok {
		return fmt.Errorf("Scan destination has type %T, want *int64", dest[0])
	}
	*version = row.version
	return nil
}

func (tx *migrationRetryTx) Conn() *pgx.Conn {
	return nil
}
