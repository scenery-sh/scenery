package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

// externalShared proves external SQL supply as environment configuration
// (plan 0205, D14): the environment's sql.database_url is one database for
// every worktree of the application, so development `up` refuses it in each
// worktree and managed destruction rejects it, while an inherited
// DATABASE_URL never selects a database.
func (p *worktreeRuntimeProbe) externalShared(source, provider string) error {
	return p.scenario("A13", "a configured sql.database_url is one external database for every worktree: development up refuses it and managed destruction rejects it", func(e map[string]any) (resultErr error) {
		const environment = "local"
		// An unroutable look-alike in every command's environment.
		const ambient = "postgres://ambient:ambient@203.0.113.13:5432/ambient"
		before, err := p.record(provider)
		if err != nil {
			return err
		}
		// The provider's app database stands in for an externally owned server.
		// Its credential reaches the CLI only on stdin; never argv, evidence or output.
		dsn := worktreePostgresURL(before.Postgres, postgresname.DatabaseNameFor(before.AppID, before.AppRoot))
		roots := []string{filepath.Join(p.root, "external-a"), filepath.Join(p.root, "external-b")}
		if p.rootEnv == nil {
			p.rootEnv = map[string][]string{}
		}
		for _, root := range roots {
			if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-"+filepath.Base(root), root); err != nil {
				return err
			}
			p.roots = append(p.roots, root)
			p.rootEnv[root] = []string{"DATABASE_URL=" + ambient}
		}
		status, _, err := p.dbServerStatus(roots[0])
		if err != nil {
			return err
		}
		if status.Scope != "worktree" || status.Status != "absent" {
			return fmt.Errorf("an inherited DATABASE_URL selected SQL supply: scope %q, status %q", status.Scope, status.Status)
		}
		store, err := appconfig.OpenStore(p.home, before.AppID)
		if err != nil {
			return err
		}
		backend, err := appconfig.DefaultSecretBackend(store)
		if err == nil {
			err = backend.Ready(p.ctx)
		}
		if err != nil {
			return fmt.Errorf("sql.database_url is a secret, which this host cannot store: %w", err)
		}
		restored := false
		defer func() {
			// Later rows start development runtimes of this environment, and no
			// secret version outlives the probe in the host's secret store.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if !restored {
				_, err := p.runWithContext(ctx, roots[1], p.binary, "config", "unset", appconfig.SQLDatabaseURLKey, "--env", environment, "--app-root", roots[1], "-o", "json")
				resultErr = errors.Join(resultErr, err)
			}
			removed, err := removeSecretVersions(ctx, backend, environment)
			e["secret_versions_removed"] = removed
			resultErr = errors.Join(resultErr, err)
		}()
		output, err := p.runInput(p.ctx, roots[0], []byte(dsn), p.binary, "config", "set", appconfig.SQLDatabaseURLKey, "--env", environment, "--stdin", "--app-root", roots[0], "-o", "json")
		if err != nil {
			return err
		}
		outputs := [][]byte{output}
		for _, root := range roots {
			// Set in one worktree, the value reaches every worktree; none runs it
			// as its development database or allocates a managed one instead.
			output, err := p.refusal(root, "scenery config unset sql.database_url --env "+environment, "up", "--detach", "--wait", "ready")
			outputs = append(outputs, output)
			if err != nil {
				return err
			}
			if _, err := p.record(root); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("refused development up left worktree authority: %v", err)
			}
			status, output, err := p.dbServerStatus(root)
			outputs = append(outputs, output)
			if err != nil {
				return err
			}
			if status.Scope != "external" || status.Status != "external" || status.URL != postgresdb.RedactURL(dsn) {
				return fmt.Errorf("%s did not resolve the environment's external database", filepath.Base(root))
			}
			for _, args := range [][]string{{"db", "reset", "--yes"}, {"db", "drop", "--yes"}, {"prune", "--db", "--older-than", "1ns"}, {"down", "--db"}} {
				output, err := p.refusal(root, "sql.database_url is external", args...)
				outputs = append(outputs, output)
				if err != nil {
					return err
				}
			}
		}
		if leak := configurationSecretLeak(before.Postgres.Password, outputs, store.Dir()); leak != "" {
			return fmt.Errorf("the configured credential appeared in %s", leak)
		}
		if _, err := p.run(roots[1], p.binary, "config", "unset", appconfig.SQLDatabaseURLKey, "--env", environment, "--app-root", roots[1], "-o", "json"); err != nil {
			return err
		}
		restored = true
		if status, _, err := p.dbServerStatus(roots[0]); err != nil || status.Scope != "worktree" {
			return fmt.Errorf("unset did not restore managed SQL supply: %v", err)
		}
		after, err := p.record(provider)
		if err != nil || !sameWorktreeCluster(before, after) {
			return fmt.Errorf("external configuration changed provider authority: %v", err)
		}
		session, err := p.liveSession(provider)
		if err != nil {
			return err
		}
		if _, err := p.verify(provider, detachedDevResult{Session: session}, "persisted"); err != nil {
			return err
		}
		e["ambient_database_url_ignored"], e["configured_by"] = true, "config set sql.database_url --env "+environment+" --stdin"
		e["development_up_refused"], e["same_external_database"], e["managed_clusters_allocated"] = len(roots), true, 0
		e["refused"] = []string{"db reset", "db drop", "prune --db", "down --db"}
		e["unset_restored_managed_supply"], e["provider_rows_verified"] = true, true
		return nil
	})
}

// refusal runs a Scenery command of root that must fail as one SCN8003
// precondition whose message contains want.
func (p *worktreeRuntimeProbe) refusal(root, want string, args ...string) ([]byte, error) {
	output, err := p.run(root, p.binary, slices.Concat(args, []string{"--app-root", root, "-o", "json"})...)
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 3 {
		return output, fmt.Errorf("%s was not refused as a precondition: %v", strings.Join(args, " "), err)
	}
	envelope, err := machine.Decode[graph.Diagnostic](output, currentMachineSpecRevision())
	if err != nil {
		return output, err
	}
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" || !strings.Contains(envelope.Diagnostics[0].Message, want) {
		return output, fmt.Errorf("%s refusal did not name %q: %+v", strings.Join(args, " "), want, envelope.Diagnostics)
	}
	return output, nil
}

func (p *worktreeRuntimeProbe) dbServerStatus(root string) (dbServerStatusResponse, []byte, error) {
	var status dbServerStatusResponse
	output, err := p.run(root, p.binary, "db", "server", "status", "--app-root", root, "-o", "json")
	if err == nil {
		err = decodeCLIJSON(output, &status)
	}
	return status, output, err
}

// removeSecretVersions deletes every secret version that the probe's private
// store indexes for environment and proves that none remain.
func removeSecretVersions(ctx context.Context, backend appconfig.SecretBackend, environment string) (int, error) {
	versions, err := backend.Versions(ctx, environment)
	if err != nil {
		return 0, err
	}
	removed := 0
	for key, list := range versions {
		for _, version := range list {
			if err := backend.Remove(ctx, environment, key, version); err != nil {
				return removed, err
			}
			removed++
		}
	}
	if remaining, err := backend.Versions(ctx, environment); err != nil || len(remaining) != 0 {
		return removed, fmt.Errorf("secret versions remain after cleanup: %d keys: %v", len(remaining), err)
	}
	return removed, nil
}
