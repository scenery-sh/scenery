package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"scenery.sh/internal/envpolicy"
)

// SQLC output that Scenery owns leaves authored source membership, so the
// build has to take it from the checkout. Build an application that imports
// such a package, then regenerate it without the query the application calls
// and require the next build to fail on exactly that.
func runHarnessSQLCOwnedOutputBuild(ctx context.Context, repoRoot, root string) (proof map[string]any, returnErr error) {
	proof = map[string]any{"cleanup_ok": false}
	if _, err := exec.LookPath("sqlc"); err != nil {
		return proof, fmt.Errorf("SQLC build probe requires sqlc: %w", err)
	}
	defer func() {
		err := os.RemoveAll(root)
		proof["cleanup_ok"] = err == nil
		returnErr = errors.Join(returnErr, err)
	}()
	app := filepath.Join(root, "app")
	write := func(relative, contents string) error {
		path := filepath.Join(app, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(contents), 0o644)
	}
	fixture := filepath.Join(repoRoot, "testdata/apps/storage-basic")
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn", "service/tasks/storage-probe.task.go"} {
		data, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			return proof, err
		}
		contents := string(data)
		switch name {
		case "go.mod":
			contents = strings.ReplaceAll(contents, "=> ../../..", "=> "+filepath.ToSlash(repoRoot))
		case "app.scn":
			contents = strings.Replace(contents, `"internal/scenerygen",`, `"internal/scenerygen", "service/dbgen",`, 1)
		case ".scenery.json":
			var config map[string]any
			if err := json.Unmarshal(data, &config); err != nil {
				return proof, err
			}
			config["id"], config["generators"] = "sqlc-build-probe", map[string]any{"sqlc": map[string]any{"provider": "sqlc"}}
			encoded, err := json.Marshal(config)
			if err != nil {
				return proof, err
			}
			contents = string(encoded)
		}
		if err := write(name, contents); err != nil {
			return proof, err
		}
	}
	for name, contents := range map[string]string{
		"sqlc.yaml":             "version: \"2\"\nsql:\n - engine: sqlite\n   schema: db/schema.sql\n   queries: db/query.sql\n   gen:\n     go:\n       package: dbgen\n       out: service/dbgen\n",
		"db/schema.sql":         "create table items (id integer primary key, label text not null);\n",
		"db/query.sql":          "-- name: ListItems :many\nselect id, label from items;\n",
		"service/uses_dbgen.go": "package service\n\nimport \"example.com/storagebasic/service/dbgen\"\n\nvar _ = (*dbgen.Queries).ListItems\n",
	} {
		if err := write(name, contents); err != nil {
			return proof, err
		}
	}
	cli := func(args ...string) error {
		cmd := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), append(args, "--app-root", app, "-o", "json")...)
		cmd.Env = append(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_HOME", "GOWORK", "GOFLAGS"), "SCENERY_AGENT_HOME="+filepath.Join(root, "agent-home"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("scenery %s: %w: %s", strings.Join(args, " "), err, tailString(firstNonEmpty(stdout.String(), stderr.String()), 4096))
		}
		return nil
	}
	build := func() error {
		return cli("build", "--target", "development", "--output", filepath.Join(root, "application"))
	}
	if err := cli("generate", "sqlc"); err != nil {
		return proof, err
	}
	if _, err := os.Stat(filepath.Join(app, ".scenery/artifacts/sqlc.json")); err != nil {
		return proof, fmt.Errorf("SQLC generation left no ownership receipt: %w", err)
	}
	if err := build(); err != nil {
		return proof, fmt.Errorf("an application importing owned SQLC output did not build: %w", err)
	}
	proof["owned_output_builds"] = true
	queries := "-- name: ListItems :many\nselect id, label from items;\n"
	for _, step := range []struct {
		query string
		built bool
	}{{"-- name: ReadItem :one\nselect id, label from items where id = ?;\n", false}, {queries, true}} {
		if err := write("db/query.sql", step.query); err != nil {
			return proof, err
		}
		if err := cli("generate", "sqlc"); err != nil {
			return proof, err
		}
		err := build()
		if step.built && err != nil {
			return proof, fmt.Errorf("restored SQLC output did not build: %w", err)
		}
		if !step.built && (err == nil || !strings.Contains(err.Error(), "ListItems")) {
			return proof, fmt.Errorf("regenerated SQLC output did not reach the next build: %v", err)
		}
	}
	proof["regenerated_output_reaches_next_build"] = true
	return proof, nil
}
