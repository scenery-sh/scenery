package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type refusingConnector struct{}

func (refusingConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("no server")
}
func (refusingConnector) Driver() driver.Driver { return nil }

func TestSetupConnectionsShareOnePoolPerDatabaseAcrossGoroutines(t *testing.T) {
	t.Parallel()
	var opens atomic.Int64
	connections := newDBSetupConnections()
	connections.open = func(context.Context, string) (*sql.DB, error) {
		opens.Add(1)
		return sql.OpenDB(refusingConnector{}), nil
	}
	connections.retained["retained"] = true
	pools := make([]*sql.DB, 16)
	var wg sync.WaitGroup
	for index := range pools {
		wg.Go(func() {
			database, err := connections.Open(t.Context(), "retained")
			if err != nil {
				t.Error(err)
			}
			pools[index] = database
		})
	}
	wg.Wait()
	for _, pool := range pools {
		if pool == nil || pool != pools[0] {
			t.Fatal("concurrent opens of one database returned different pools")
		}
	}
	if connections.opened != 1 || connections.reuses != len(pools)-1 || opens.Load() < 1 {
		t.Fatalf("opened %d, reused %d, dialed %d", connections.opened, connections.reuses, opens.Load())
	}
	// A migration-only pool is closed when released; a retained one stays.
	for range 8 {
		wg.Go(func() {
			if _, err := connections.Open(t.Context(), "migration"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := errors.Join(connections.ReleaseMigration("migration"), connections.ReleaseMigration("retained"), connections.ReleaseMigration("absent")); err != nil {
		t.Fatal(err)
	}
	if len(connections.databases) != 1 || connections.databases["retained"] == nil {
		t.Fatalf("pools after release = %v", connections.databases)
	}
	if err := connections.Close(); err != nil || len(connections.databases) != 0 {
		t.Fatalf("close: %v, %d pools left", err, len(connections.databases))
	}
}
