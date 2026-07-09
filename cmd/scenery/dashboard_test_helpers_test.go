package main

import (
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

func newTestDashboardServer(t *testing.T) *dashboardServer {
	t.Helper()

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	supervisor := &devSupervisor{
		cfg:         app.Config{Name: "app-test"},
		store:       store,
		reportToken: "test-token",
	}
	return newDashboardServer(supervisor, "")
}
