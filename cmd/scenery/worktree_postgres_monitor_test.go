package main

import (
	"strings"
	"testing"

	"scenery.sh/internal/postgresdb"
)

func TestWorktreeDatabaseTargetTracksOnlyManagedEndpoint(t *testing.T) {
	t.Parallel()
	supervisor := &devSupervisor{}
	database := postgresdb.Database{Source: postgresdb.SourceManaged, AppRoot: "/app", ResourceID: "owned-resource", URL: "postgres://user:private-password@127.0.0.1:54321/app"}
	supervisor.rememberWorktreeDatabase(database, "app")
	first := supervisor.postgresTarget
	if first == nil || first.Endpoint != "127.0.0.1:54321" || first.AppRoot != "/app" || first.AppID != "app" || first.ResourceID != "owned-resource" {
		t.Fatal("managed target did not retain exact non-secret connection identity")
	}
	database.URL = strings.ReplaceAll(database.URL, ":54321/", ":54322/")
	supervisor.rememberWorktreeDatabase(database, "app")
	if supervisor.postgresTarget == first || first.Endpoint != "127.0.0.1:54321" || supervisor.postgresTarget.Endpoint != "127.0.0.1:54322" {
		t.Fatal("target update mutated a monitor's immutable observation")
	}
	database.Source = postgresdb.SourceExternal
	supervisor.rememberWorktreeDatabase(database, "app")
	if supervisor.postgresTarget != nil {
		t.Fatal("external database entered managed lifecycle monitoring")
	}
}
