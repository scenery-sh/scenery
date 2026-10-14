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

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

// Exercise the actual CLI and SQLC tool, including changed membership and
// ownership-safe repair. Fault injection remains in ordinary in-process tests.
func runHarnessSQLCLifecycle(ctx context.Context, repoRoot, root string) (proof map[string]any, returnErr error) {
	proof = map[string]any{"assertions": []string{}, "cleanup_ok": false}
	if _, err := exec.LookPath("sqlc"); err != nil {
		return proof, fmt.Errorf("SQLC lifecycle probe requires sqlc: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return proof, err
	}
	defer func() {
		err := os.RemoveAll(root)
		if err == nil {
			if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
				err = fmt.Errorf("SQLC probe root still exists")
			}
		}
		proof["cleanup_ok"] = err == nil
		returnErr = errors.Join(returnErr, err)
	}()
	write := func(relative, contents string) error {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(contents), 0o644)
	}
	files := map[string]string{
		".scenery.json":             `{"name":"sqlc-probe","id":"sqlc-probe","envs":{"local":{"default":true}},"generators":{"sqlc":{"provider":"sqlc"}}}`,
		"app.scn":                   "application \"sqlc_probe\" {}\nworkspace {\n managed_generated_roots = [\"db/gen\"]\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"**/*.go\"]\n  revision_exclude = []\n }\n}\n",
		"sqlc.yaml":                 "version: \"2\"\nplugins:\n - name: unused\n   process:\n     cmd: scenery-unused-sqlc-plugin-must-not-exist\nsql:\n - engine: sqlite\n   schema: db/schema.sql\n   queries: db/query.sql\n   gen:\n     go:\n       package: db\n       out: db/gen\n",
		"db/schema.sql":             "create table items (id integer primary key, label text not null);\n",
		"db/query.sql":              "-- name: ListItems :many\nselect id, label from items;\n",
		"db/gen/handwritten_gen.go": "package db\nconst authored = true\n",
	}
	for path, contents := range files {
		if err := write(path, contents); err != nil {
			return proof, err
		}
	}
	var assertions []string
	run := func() ([]byte, error) {
		cmd := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "generate", "sqlc", "--app-root", root, "-o", "json")
		cmd.Env = append(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_HOME"), "SCENERY_AGENT_HOME="+filepath.Join(root, "agent-home"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return stdout.Bytes(), fmt.Errorf("SQLC generation: %w: stdout=%s stderr=%s", err, stdout.String(), stderr.String())
		}
		var envelope struct {
			OK                bool   `json:"ok"`
			WorkspaceRevision string `json:"workspace_revision"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || !envelope.OK || envelope.WorkspaceRevision == "" {
			return stdout.Bytes(), fmt.Errorf("SQLC JSON channel/revision mismatch: %s", stdout.String())
		}
		// Match the CLI's canonical root, including macOS's temporary-dir alias.
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return stdout.Bytes(), err
		}
		current, err := compiler.Compile(canonicalRoot)
		if err != nil || !current.Valid() || current.WorkspaceRevision != envelope.WorkspaceRevision {
			return stdout.Bytes(), fmt.Errorf("SQLC response does not identify the published workspace: %v", err)
		}
		return stdout.Bytes(), nil
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	assertions = append(assertions, "actual CLI and SQLC publish one revision-bound JSON response")
	assertions = append(assertions, "unused process plugin does not require an installed binary")
	// Model upgrade from existing SQLC output without private ownership metadata.
	if err := os.Remove(filepath.Join(root, ".scenery/artifacts/sqlc.json")); err != nil {
		return proof, err
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	assertions = append(assertions, "identical existing output is adopted with a valid post-publication JSON revision")
	query := filepath.Join(root, "db/gen/query.sql.go")
	before, err := os.ReadFile(query)
	if err != nil {
		return proof, err
	}
	if err := write("db/query.sql", "-- name: ReadItem :one\nselect id, label from items where id = ?;\n"); err != nil {
		return proof, err
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	changed, err := os.ReadFile(query)
	if err != nil || bytes.Equal(changed, before) || !bytes.Contains(changed, []byte("ReadItem")) {
		return proof, fmt.Errorf("SQLC query edit did not refresh generated code")
	}
	assertions = append(assertions, "authored query edit changes generated code without cache deletion")
	if err := os.Remove(query); err != nil {
		return proof, err
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	restored, err := os.ReadFile(query)
	if err != nil || !bytes.Equal(restored, changed) {
		return proof, fmt.Errorf("deleted SQLC output was not repaired")
	}
	assertions = append(assertions, "deleted descriptor-owned output is repaired")
	authored, err := os.ReadFile(filepath.Join(root, "db/gen/handwritten_gen.go"))
	if err != nil || string(authored) != files["db/gen/handwritten_gen.go"] {
		return proof, fmt.Errorf("SQLC generation changed an unrelated authored file")
	}
	assertions = append(assertions, "authored _gen.go file in managed root remains unchanged")
	if err := write("db/gen/query.sql.go", "package db\nconst userEdit = true\n"); err != nil {
		return proof, err
	}
	if _, err := run(); err == nil {
		return proof, fmt.Errorf("SQLC accepted tampered output ownership")
	}
	after, err := os.ReadFile(query)
	if err != nil || string(after) != "package db\nconst userEdit = true\n" {
		return proof, fmt.Errorf("SQLC refusal overwrote tampered output")
	}
	assertions = append(assertions, "tampered output is refused without overwriting bytes")
	proof["assertions"] = assertions
	proof["assertion_count"] = len(assertions)
	proof["proof"] = "passed"
	return proof, nil
}
