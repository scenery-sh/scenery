package postgresdb

import (
	"context"
	"errors"
	"testing"
)

func TestBuildHasRowsQuery(t *testing.T) {
	got := buildHasRowsQuery([]string{`"orders"."items"`, `"billing"."invoices"`})
	want := `SELECT EXISTS (SELECT 1 FROM "orders"."items" LIMIT 1) OR EXISTS (SELECT 1 FROM "billing"."invoices" LIMIT 1)`
	if got != want {
		t.Fatalf("buildHasRowsQuery = %q, want %q", got, want)
	}
}

func TestFastResetDatabaseRejectsNilOrNonResettableState(t *testing.T) {
	if err := FastResetDatabase(context.Background(), nil, nil); !errors.Is(err, ErrDatabaseNotResettable) {
		t.Fatalf("nil state err = %v, want ErrDatabaseNotResettable", err)
	}
	if err := FastResetDatabase(context.Background(), nil, &ResettableDatabaseState{Resettable: false}); !errors.Is(err, ErrDatabaseNotResettable) {
		t.Fatalf("non-resettable state err = %v, want ErrDatabaseNotResettable", err)
	}
}

func TestTerminateDatabaseBackendsRequiresName(t *testing.T) {
	if err := TerminateDatabaseBackends(context.Background(), nil, "   "); err == nil {
		t.Fatal("expected error for blank database name")
	}
}
