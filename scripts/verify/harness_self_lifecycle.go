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

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/workspacetx"
)

// Exercise real nested-client publication and edits in the ownership-to-staging
// interval. Pure plan/precondition cases stay in ordinary unit tests.
func runHarnessLifecycleBoundaries(ctx context.Context, repoRoot, root string) (proof map[string]any, returnErr error) {
	proof = map[string]any{"proof": "pending", "cleanup_ok": false}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return proof, err
	}
	defer func() {
		err := os.RemoveAll(root)
		if err == nil {
			if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
				err = fmt.Errorf("lifecycle fixture remains after cleanup")
			}
		}
		proof["cleanup_ok"] = err == nil
		returnErr = errors.Join(returnErr, err)
	}()
	write := func(path, data string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(data), 0o644)
	}
	source := `application "nested_clients" {}
workspace { managed_generated_roots = ["clients/generated", "shared/scenerycontract", "race"] }
go_module "application" {
 root = "."
 import_path = "example.test/lifecycle"
}
module "shared" { source = "./shared" }
http_gateway "api" {
 exposure = "internet"
 base_path = "/"
 cors = std.cors.none
 trusted_proxies = std.trusted_proxies.none
 forwarded = std.forwarded_headers.reject
}
typescript_client "public_api" {
 gateways = [http_gateway.api]
 package = "@example/public"
 module = "esm"
 runtime = "fetch"
 output_root = "clients/generated"
}
typescript_client "admin_api" {
 gateways = [http_gateway.api]
 package = "@example/admin"
 module = "esm"
 runtime = "fetch"
 output_root = "clients/generated/admin"
}
`
	if err := write(filepath.Join(root, "app.scn"), source); err != nil {
		return proof, err
	}
	if err := write(filepath.Join(root, "go.mod"), "module example.test/lifecycle\n\ngo 1.27.0\n"); err != nil {
		return proof, err
	}
	if err := write(filepath.Join(root, "shared/package.scn"), `package "shared" {
 go_contract { import_path = "example.test/lifecycle/shared" }
}
record "item" {
 field "id" { type = string }
}
export "item" { value = record.item }
`); err != nil {
		return proof, err
	}
	if err := write(filepath.Join(root, ".scenery.json"), `{"name":"nested-clients","id":"nested-clients","envs":{"local":{"default":true}}}`); err != nil {
		return proof, err
	}
	for _, args := range [][]string{{"inspect", "generators"}, {"generate"}, {"generate", "--check"}} {
		cmd := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), append(args, "--app-root", root, "-o", "json")...)
		cmd.Env = append(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_HOME"), "SCENERY_AGENT_HOME="+filepath.Join(root, "agent-home"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return proof, fmt.Errorf("nested client %v: %w: stdout=%s stderr=%s", args, err, stdout.String(), stderr.String())
		}
		if args[0] == "inspect" {
			var envelope struct {
				Data struct {
					Generators []struct {
						ID      string   `json:"id"`
						Outputs []string `json:"outputs"`
					} `json:"generators"`
				} `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				return proof, err
			}
			found := false
			for _, generator := range envelope.Data.Generators {
				if generator.ID == "go.render" {
					for _, output := range generator.Outputs {
						found = found || output == "shared/scenerycontract/types.gen.go"
					}
				}
			}
			if !found {
				return proof, fmt.Errorf("generator inspection omitted selected Go files")
			}
		}
	}
	for _, output := range []string{"clients/generated", "clients/generated/admin"} {
		for _, file := range []string{"client.ts", "scenery.typescript-client-generated.json"} {
			if _, err := os.Stat(filepath.Join(root, output, file)); err != nil {
				return proof, err
			}
		}
	}
	proof["nested_clients"] = "inspect, actual two-root publication and check-only succeed"
	goPath := filepath.Join(root, "shared/scenerycontract/contract.gen.go")
	authored := "package scenerycontract\nconst editorChange = true\n"
	if err := write(goPath, authored); err != nil {
		return proof, err
	}
	clientPath := filepath.Join(root, "clients/generated/client.ts")
	if err := os.Remove(clientPath); err != nil {
		return proof, err
	}
	if _, err := generate.GenerateTypeScriptClients(root, "", false); err != nil {
		return proof, fmt.Errorf("all-client library refresh touched unrelated Go ownership: %w", err)
	}
	goBytes, err := os.ReadFile(goPath)
	if err != nil || string(goBytes) != authored {
		return proof, fmt.Errorf("all-client library refresh changed Go bytes")
	}
	if _, err := os.Stat(clientPath); err != nil {
		return proof, err
	}
	proof["typescript_only"] = "empty-selector library refresh repairs missing client output and preserves edited Go contracts"
	var assertions []string
	for _, name := range []string{"replace", "retire", "appear", "no-op"} {
		path := filepath.Join(root, "race/output.go")
		before := "verified output"
		expected := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(before)))
		if name == "appear" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return proof, err
			}
			expected = workspacetx.AbsentDigest
		} else if err := write(path, before); err != nil {
			return proof, err
		}
		metadataPath := filepath.Join(root, "race/a-receipt.json")
		if err := write(metadataPath, "old receipt"); err != nil {
			return proof, err
		}
		desired := "generated replacement"
		if name == "no-op" {
			desired = "editor bytes"
		}
		err := workspacetx.PublishContext(ctx, root, func() ([]workspacetx.File, error) {
			// The caller verified expected before this edit; the transaction must
			// retain that evidence instead of adopting the editor's new bytes.
			if err := write(path, "editor bytes"); err != nil {
				return nil, err
			}
			return []workspacetx.File{{Path: "race/a-receipt.json", Bytes: []byte("new receipt")}, {Path: "race/output.go", Bytes: []byte(desired), Remove: name == "retire", ExpectedBeforeDigest: expected}}, nil
		})
		if err == nil || !strings.Contains(err.Error(), "changed after ownership verification") {
			return proof, fmt.Errorf("late %s edit accepted: %v", name, err)
		}
		for file, expected := range map[string]string{path: "editor bytes", metadataPath: "old receipt"} {
			data, err := os.ReadFile(file)
			if err != nil || string(data) != expected {
				return proof, fmt.Errorf("late %s edit or receipt was overwritten: %s", name, file)
			}
		}
		if err := workspacetx.RecoverOrReject(root, workspacetx.NormalRead); err != nil {
			return proof, err
		}
		assertions = append(assertions, name+" preserves editor bytes and original metadata")
	}
	proof["publication_edits"] = assertions
	proof["proof"] = "passed"
	return proof, nil
}
