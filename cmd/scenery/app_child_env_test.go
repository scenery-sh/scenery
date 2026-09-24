package main

import (
	"slices"
	"testing"
)

func TestMinimalAppProcessEnvKeepsOnlyProtocolsAndSceneryWiring(t *testing.T) {
	got := minimalAppProcessEnv([]string{
		"PATH=/usr/bin", "HOME=/home/a", "LC_ALL=C", "SCENERY_APP_ROOT=/app", "DATABASE_URL=postgres://configured/db",
		"JWT_SECRET=old", "AWS_SECRET_ACCESS_KEY=x", "NODE_OPTIONS=--require dotenv/config", "VITE_KEY=x",
		"REPORTS_DATABASE_URL=postgres://ambient/db", "NSRDB_PACK_ROOT=/Volumes/x", "malformed",
	})
	want := []string{"PATH=/usr/bin", "HOME=/home/a", "LC_ALL=C", "SCENERY_APP_ROOT=/app", "DATABASE_URL=postgres://configured/db"}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q, want %q", got, want)
	}
}
