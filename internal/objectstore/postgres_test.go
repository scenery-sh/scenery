package objectstore

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pbrazdil/onlava/internal/testpostgres"
)

var postgresTest struct {
	once     sync.Once
	db       *testpostgres.Database
	err      error
	pool     *pgxpool.Pool
	poolErr  error
	poolOnce sync.Once
}

func TestMain(m *testing.M) {
	code := m.Run()
	if postgresTest.pool != nil {
		postgresTest.pool.Close()
	}
	if postgresTest.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = postgresTest.db.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

func postgresTestDatabaseURL(t *testing.T) string {
	t.Helper()
	url, err := ensurePostgresTestDatabase()
	if err != nil {
		t.Fatalf("PostgreSQL integration test setup failed; start Docker or set %s: %v", testpostgres.EnvDatabaseURL, err)
	}
	return url
}

func ensurePostgresTestDatabase() (string, error) {
	postgresTest.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		postgresTest.db, postgresTest.err = testpostgres.Start(ctx)
	})
	if postgresTest.err != nil {
		return "", postgresTest.err
	}
	if postgresTest.db == nil {
		return "", errors.New("testpostgres.Start returned nil database without error")
	}
	return postgresTest.db.URL, nil
}
