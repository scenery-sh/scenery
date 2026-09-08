package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/postgresname"
)

func (p *worktreeRuntimeProbe) externalShared(source, provider string) error {
	return p.scenario("A13", "equal explicit external DSNs stay shared and reject managed destruction", func(e map[string]any) error {
		before, err := p.record(provider)
		if err != nil {
			return err
		}
		dsn := worktreePostgresURL(before.Postgres, postgresname.DatabaseNameFor(before.AppID, before.AppRoot))
		for _, name := range []string{"external-a", "external-b"} {
			root := filepath.Join(p.root, name)
			if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-"+name, root); err != nil {
				return err
			}
			p.roots = append(p.roots, root)
			// A test-owned dotenv supplies the explicit external capability.
			// Never put this credential in argv, evidence, or command output.
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL="+dsn+"\n"), 0o600); err != nil {
				return err
			}
			runtime, err := p.up(root)
			if err != nil {
				return err
			}
			if _, err := p.verify(root, runtime, "persisted"); err != nil {
				return err
			}
			record, err := p.record(root)
			if err != nil || record.Postgres != nil {
				return fmt.Errorf("external runtime allocated managed PostgreSQL authority: %v", err)
			}
			output, err := p.run(root, p.binary, "db", "server", "status", "--app-root", root, "-o", "json")
			if err != nil || !bytes.Contains(output, []byte(`"scope": "external"`)) && !bytes.Contains(output, []byte(`"scope":"external"`)) {
				return fmt.Errorf("external status did not report external scope: %v", err)
			}
			if bytes.Contains(output, []byte(before.Postgres.Password)) {
				return fmt.Errorf("external status exposed a credential")
			}
			if _, err := p.run(root, p.binary, "down", "--app-root", root, "-o", "json"); err != nil {
				return err
			}
			for _, args := range [][]string{
				{"db", "reset", "--yes"},
				{"db", "drop", "--yes"},
				{"prune", "--db", "--older-than", "1ns"},
			} {
				args = append(args, "--app-root", root, "-o", "json")
				output, rejected := p.run(root, p.binary, args...)
				if rejected == nil || !bytes.Contains(bytes.ToLower(output), []byte("external")) {
					return fmt.Errorf("external %s did not refuse managed destruction", args[0])
				}
			}
		}
		after, err := p.record(provider)
		if err != nil || !sameWorktreeCluster(before, after) {
			return fmt.Errorf("external runtimes changed provider authority: %v", err)
		}
		session, err := p.liveSession(provider)
		if err != nil {
			return err
		}
		if _, err := p.verify(provider, detachedDevResult{Session: session}, "persisted"); err != nil {
			return err
		}
		e["equal_external_dsn"], e["shared_rows_verified"], e["managed_clusters_allocated"] = true, true, 0
		e["refused"] = []string{"reset", "drop", "prune"}
		return nil
	})
}
