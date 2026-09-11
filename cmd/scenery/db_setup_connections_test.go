package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestDatabaseSetupConnectionsHaveInvocationOwnership(t *testing.T) {
	opened, closed := 0, 0
	opener := func(ctx context.Context, _ string) (*sql.DB, error) {
		opened++
		database := sql.OpenDB(setupTestConnector{closed: &closed})
		if err := database.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		return database, nil
	}
	connections := newDBSetupConnections()
	connections.open = opener
	first, err := connections.Open(t.Context(), "schema-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := connections.Open(t.Context(), "schema-a")
	if err != nil || first != second || opened != 1 {
		t.Fatalf("same setup endpoint did not reuse its pool: opened=%d err=%v", opened, err)
	}
	store, err := connections.SeedStore(t.Context(), "schema-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil || closed != 0 {
		t.Fatalf("seed borrower closed the setup owner's pool: closed=%d err=%v", closed, err)
	}
	if _, err := connections.Open(t.Context(), "schema-b"); err != nil || opened != 2 {
		t.Fatal("different endpoint/schema authority shared a pool")
	}
	if err := connections.ReleaseMigration("schema-b"); err != nil || closed != 1 {
		t.Fatal("migration-only pool remained idle until the end of setup")
	}
	connections.retained["schema-a"] = true
	if err := connections.ReleaseMigration("schema-a"); err != nil || closed != 1 {
		t.Fatal("migration released a pool reserved for following seed work")
	}
	if err := connections.Close(); err != nil || closed != 2 {
		t.Fatalf("setup did not close every pool: closed=%d err=%v", closed, err)
	}
	next := newDBSetupConnections()
	next.open = opener
	if _, err := next.Open(t.Context(), "schema-a"); err != nil || opened != 3 {
		t.Fatal("later setup reused a retained connection")
	}
	if err := next.Close(); err != nil || closed != 3 {
		t.Fatalf("later setup cleanup: closed=%d err=%v", closed, err)
	}
}

type setupTestConnector struct{ closed *int }

func (c setupTestConnector) Connect(context.Context) (driver.Conn, error) {
	return setupTestConn(c), nil
}
func (setupTestConnector) Driver() driver.Driver { return setupTestDriver{} }

type setupTestDriver struct{}

func (setupTestDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type setupTestConn struct{ closed *int }

func (c setupTestConn) Close() error                      { *c.closed++; return nil }
func (setupTestConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not needed") }
func (setupTestConn) Begin() (driver.Tx, error)           { return nil, errors.New("not needed") }
