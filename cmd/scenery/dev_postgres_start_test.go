package main

import (
	"context"
	"errors"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
)

func TestPostgresEarlyStartRequiresManagedLocalSupply(t *testing.T) {
	for _, scenario := range []string{"no-sql", "external", "remote-durable", "unmanaged"} {
		t.Run(scenario, func(t *testing.T) {
			contract := &compiler.Result{Manifest: &compiler.Manifest{}, ContractStatus: "valid"}
			s := &devSupervisor{root: t.TempDir(), worktreeRootPaths: &localagent.WorktreePaths{}, invocationEnvironment: []string{}}
			if scenario != "no-sql" {
				contract.SQLRequirements = compiler.SQLRequirements{{Kind: compiler.SQLDataSource, Name: "books", Schema: "books", Lifecycle: "managed"}}
			}
			switch scenario {
			case "external":
				s.invocationEnvironment = []string{"DATABASE_URL=postgres://localhost/books"}
			case "remote-durable":
				contract.SQLRequirements[0].Kind = compiler.SQLDurable
				s.invocationEnvironment = []string{"SCENERY_DURABLE_ENDPOINT=http://localhost:8123"}
			case "unmanaged":
				contract.SQLRequirements[0].Lifecycle = "external"
			}
			attempt, err := s.beginRetainedPostgresStart(context.Background(), contract)
			if attempt != nil {
				attempt.release()
				t.Fatal("selection reached retained managed startup")
			}
			if (err != nil) != (scenario == "unmanaged") {
				t.Fatalf("SQL supply validation returned %v", err)
			}
		})
	}
}

func TestPostgresStartAttemptJoinsCancellation(t *testing.T) {
	started := make(chan struct{})
	finished := false
	attempt := beginPostgresStart(context.Background(), func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		finished = true
		return ctx.Err()
	})
	<-started
	attempt.release()
	if !finished || !errors.Is(attempt.wait(), context.Canceled) {
		t.Fatal("release did not cancel and join retained PostgreSQL startup")
	}
	attempt.release()
	var absent *postgresStartAttempt
	absent.release()
	if err := absent.wait(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStartAttemptPreservesFailure(t *testing.T) {
	want := errors.New("owned server failed")
	attempt := beginPostgresStart(context.Background(), func(context.Context) error { return want })
	defer attempt.release()
	if err := attempt.wait(); !errors.Is(err, want) {
		t.Fatalf("startup failure = %v", err)
	}
}
