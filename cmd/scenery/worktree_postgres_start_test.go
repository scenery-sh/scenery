package main

import (
	"context"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestWorktreePostgresEarlyStartKeepsAllocationBoundary(t *testing.T) {
	ctx := context.Background()
	r, docker := testWorktreePostgresResolver(t)
	if err := r.startRetained(ctx); err != nil || docker.writes != 0 {
		t.Fatalf("early startup provisioned a new server: %v", err)
	}
	original, err := r.ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.stop(ctx); err != nil {
		t.Fatal(err)
	}
	writes := docker.writes
	container := docker.container
	docker.container = nil
	if err := r.startRetained(ctx); err != nil || docker.writes != writes {
		t.Fatalf("early startup recreated a missing container: %v", err)
	}
	docker.container = container
	if err := r.startRetained(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	if !docker.container.Running || docker.writes != writes+1 || record.Postgres.InstanceID != original.InstanceID || record.Postgres.Password != original.Password || record.Postgres.SystemID != original.SystemID {
		t.Fatal("early startup did not retain the existing allocation and credentials")
	}
	if err := r.startRetained(ctx); err != nil || docker.writes != writes+1 {
		t.Fatalf("running owned server was restarted: %v", err)
	}
}

func TestWorktreePostgresEarlyStartRequiresVerifiedReadyState(t *testing.T) {
	for _, scenario := range []string{"pending", "wrong-daemon", "wrong-cluster"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, docker := testWorktreePostgresResolver(t)
			if _, err := r.ensure(ctx); err != nil {
				t.Fatal(err)
			}
			if err := r.stop(ctx); err != nil {
				t.Fatal(err)
			}
			before, err := r.load()
			if err != nil {
				t.Fatal(err)
			}
			writes := docker.writes
			switch scenario {
			case "pending":
				before.Postgres.Phase = "pending"
				op, err := r.beginOperation()
				if err != nil {
					t.Fatal(err)
				}
				if err := op.SaveRecord(before); err != nil {
					t.Fatal(err)
				}
				if err := op.Close(); err != nil {
					t.Fatal(err)
				}
			case "wrong-daemon":
				docker.id = "foreign-daemon"
			case "wrong-cluster":
				r.probe = func(context.Context, *localagent.WorktreePostgres) (string, error) { return "foreign-cluster", nil }
			}
			err = r.startRetained(ctx)
			if scenario == "pending" {
				if err != nil || docker.writes != writes {
					t.Fatalf("pending provisioning was changed: %v", err)
				}
			} else if err == nil {
				t.Fatal("unverified retained endpoint was accepted")
			}
			if scenario == "wrong-daemon" && docker.writes != writes {
				t.Fatal("foreign Docker daemon was mutated")
			}
			after, err := r.load()
			if err != nil || after.Postgres.SystemID != before.Postgres.SystemID || after.Postgres.Port != before.Postgres.Port {
				t.Fatal("failed readiness changed retained identity")
			}
		})
	}
}
