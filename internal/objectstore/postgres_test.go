package objectstore

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/testpostgres"
)

var postgresTest struct {
	once sync.Once
	db   *testpostgres.Database
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if postgresTest.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = postgresTest.db.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

func postgresTestDatabaseURL(t *testing.T) string {
	t.Helper()
	postgresTest.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		postgresTest.db, postgresTest.err = testpostgres.Start(ctx)
	})
	if postgresTest.err != nil {
		t.Fatalf("PostgreSQL integration test setup failed; start Docker or set %s: %v", testpostgres.EnvDatabaseURL, postgresTest.err)
	}
	return postgresTest.db.URL
}
