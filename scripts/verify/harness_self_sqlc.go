package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	runWithFormat := func(format string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "generate", "sqlc", "--app-root", root, "-o", format)
		cmd.Env = append(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_HOME"), "SCENERY_AGENT_HOME="+filepath.Join(root, "agent-home"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return stdout.Bytes(), fmt.Errorf("SQLC generation: %w: stdout=%s stderr=%s", err, stdout.String(), stderr.String())
		}
		if format != "json" {
			return stdout.Bytes(), nil
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
	run := func() ([]byte, error) { return runWithFormat("json") }
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
	// A valid empty protobuf response is a successful plugin process with no
	// files. The complete output-set check must refuse it before retirement.
	plugin := filepath.Join(root, "empty-plugin")
	if err := write("empty-plugin", "#!/bin/sh\ncat >/dev/null\n"); err != nil {
		return proof, err
	}
	if err := os.Chmod(plugin, 0o755); err != nil {
		return proof, err
	}
	partialConfig := strings.Replace(files["sqlc.yaml"], "sql:\n", " - name: empty\n   process:\n     cmd: "+plugin+"\nsql:\n", 1)
	partialConfig += "   codegen:\n    - plugin: empty\n      out: db/plugin\n"
	if err := write("sqlc.yaml", partialConfig); err != nil {
		return proof, err
	}
	if err := write("app.scn", strings.Replace(files["app.scn"], "[\"db/gen\"]", "[\"db/gen\", \"db/plugin\"]", 1)); err != nil {
		return proof, err
	}
	if err := write("db/plugin/old.txt", "previous plugin output"); err != nil {
		return proof, err
	}
	receiptPath := filepath.Join(root, ".scenery/artifacts/sqlc.json")
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return proof, err
	}
	var receipt compiler.SQLCArtifactReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		return proof, err
	}
	receipt.Roots = append(receipt.Roots, "db/plugin")
	receipt.Files["db/plugin/old.txt"] = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("previous plugin output")))
	receiptBytes, err = json.Marshal(receipt)
	if err != nil {
		return proof, err
	}
	if err := os.WriteFile(receiptPath, receiptBytes, 0o644); err != nil {
		return proof, err
	}
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "SQLC produced no code output in declared root db/plugin") {
		return proof, fmt.Errorf("empty successful plugin did not fail the output-set check: %v", err)
	}
	unchangedReceipt, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(unchangedReceipt, receiptBytes) {
		return proof, fmt.Errorf("partial SQLC output changed ownership metadata")
	}
	pluginOutput, err := os.ReadFile(filepath.Join(root, "db/plugin/old.txt"))
	if err != nil || string(pluginOutput) != "previous plugin output" {
		return proof, fmt.Errorf("empty plugin retired its prior output")
	}
	assertions = append(assertions, "successful empty plugin refuses partial publication and preserves prior output")
	if err := write("sqlc.yaml", files["sqlc.yaml"]); err != nil {
		return proof, err
	}
	if err := write("app.scn", files["app.scn"]); err != nil {
		return proof, err
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	pluginOutput, err = os.ReadFile(filepath.Join(root, "db/plugin/old.txt"))
	if err != nil || string(pluginOutput) != "previous plugin output" {
		return proof, fmt.Errorf("revoked managed root deleted its old receipt output")
	}
	assertions = append(assertions, "revoked managed root retains an unchanged previously owned file")
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
	// Moving the Go output also retains paths now included as authored Go input.
	if err := write("app.scn", strings.Replace(files["app.scn"], "[\"db/gen\"]", "[\"db/new\"]", 1)); err != nil {
		return proof, err
	}
	if err := write("sqlc.yaml", strings.Replace(files["sqlc.yaml"], "out: db/gen", "out: db/new", 1)); err != nil {
		return proof, err
	}
	if _, err := run(); err != nil {
		return proof, err
	}
	retained, err := os.ReadFile(query)
	if err != nil || !bytes.Equal(retained, after) {
		return proof, fmt.Errorf("output move removed revoked authored Go input")
	}
	assertions = append(assertions, "output move preserves former managed Go files and the published source identity")
	// The staged tool must resolve relative paths from the selected config's
	// directory, matching the read-only plan for either config filename.
	if err := write("app.scn", strings.Replace(files["app.scn"], "[\"db/gen\"]", "[\"config/gen\"]", 1)); err != nil {
		return proof, err
	}
	for path, contents := range map[string]string{
		"config/schema.sql": files["db/schema.sql"],
		"config/query.sql":  files["db/query.sql"],
	} {
		if err := write(path, contents); err != nil {
			return proof, err
		}
	}
	nestedConfig := "version: \"2\"\nsql:\n - engine: sqlite\n   schema: schema.sql\n   queries: query.sql\n   gen:\n     go:\n       package: db\n       out: gen\n"
	for _, name := range []string{"sqlc.yaml", "codegen.yaml"} {
		if err := write("config/"+name, nestedConfig); err != nil {
			return proof, err
		}
		if err := write(".scenery.json", strings.Replace(files[".scenery.json"], `"provider":"sqlc"`, `"provider":"sqlc","config":"config/`+name+`"`, 1)); err != nil {
			return proof, err
		}
		if _, err := run(); err != nil {
			return proof, err
		}
		generated, err := os.ReadFile(filepath.Join(root, "config/gen/query.sql.go"))
		if err != nil || !bytes.Contains(generated, []byte("ListItems")) {
			return proof, fmt.Errorf("nested SQLC config %s did not publish in its declared root: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(root, "gen")); !os.IsNotExist(err) {
			return proof, fmt.Errorf("nested SQLC config %s wrote into the workspace root", name)
		}
	}
	assertions = append(assertions, "nested default and custom SQLC configs resolve inputs and outputs from their own directory")
	if err := write("app.scn", strings.Replace(files["app.scn"], "[\"db/gen\"]", "[\"config/gen\", \"config/plugin-gen\"]", 1)); err != nil {
		return proof, err
	}
	localConfig := strings.Replace(nestedConfig, "sql:\n", "plugins:\n - name: local\n   process:\n     cmd: ./tools/plugin\n     format: json\nsql:\n", 1) + "   codegen:\n    - plugin: local\n      out: plugin-gen\n"
	if err := write("config/codegen.yaml", localConfig); err != nil {
		return proof, err
	}
	for _, output := range []struct{ encoded, contents string }{
		{"bG9jYWwgcGx1Z2luCg==", "local plugin\n"},
		{"bG9jYWwgcGx1Z2luIHR3bwo=", "local plugin two\n"},
	} {
		plugin := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"files\":[{\"name\":\"plugin.txt\",\"contents\":\"" + output.encoded + "\"}]}'\n"
		if err := write("config/tools/plugin", plugin); err != nil {
			return proof, err
		}
		if err := os.Chmod(filepath.Join(root, "config/tools/plugin"), 0o755); err != nil {
			return proof, err
		}
		if _, err := run(); err != nil {
			return proof, err
		}
		data, err := os.ReadFile(filepath.Join(root, "config/plugin-gen/plugin.txt"))
		if err != nil || string(data) != output.contents {
			return proof, fmt.Errorf("captured config-relative plugin did not refresh output: %q %v", data, err)
		}
	}
	assertions = append(assertions, "config-relative process plugin runs from captured executable bytes when CLI starts outside the app")
	assertions = append(assertions, "editing a local process plugin refreshes its generated output")
	beforeReceipt, err := os.ReadFile(receiptPath)
	if err != nil {
		return proof, err
	}
	// The staged executable removes the origin's permission after capture.
	// Its bytes remain unchanged, so only the pre-commit mode check can refuse it.
	for _, format := range []string{"human", "json"} {
		origin := filepath.Join(root, "config/tools/plugin")
		plugin := "#!/bin/sh\nchmod 0644 '" + origin + "'\ncat >/dev/null\nprintf '%s' '{\"files\":[{\"name\":\"plugin.txt\",\"contents\":\"cmVmdXNlZA==\"}]}'\n"
		if err := write("config/tools/plugin", plugin); err != nil {
			return proof, err
		}
		if err := os.Chmod(origin, 0o755); err != nil {
			return proof, err
		}
		_, err := runWithFormat(format)
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 || format == "json" && !strings.Contains(err.Error(), "SCN8002") {
			return proof, fmt.Errorf("%s SQLC did not report a revision conflict for lost executable permission: %v", format, err)
		}
		receipt, err := os.ReadFile(receiptPath)
		if err != nil || !bytes.Equal(receipt, beforeReceipt) {
			return proof, fmt.Errorf("%s refusal changed SQLC ownership metadata", format)
		}
		output, err := os.ReadFile(filepath.Join(root, "config/plugin-gen/plugin.txt"))
		if err != nil || string(output) != "local plugin two\n" {
			return proof, fmt.Errorf("%s refusal changed SQLC output", format)
		}
	}
	assertions = append(assertions, "human and JSON generation refuse executable-mode loss before changing output or ownership metadata")
	proof["assertions"] = assertions
	proof["assertion_count"] = len(assertions)
	proof["proof"] = "passed"
	return proof, nil
}
