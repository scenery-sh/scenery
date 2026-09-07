package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"scenery.sh/internal/envpolicy"
)

func runHarnessGoWorkCutover(ctx context.Context, repoRoot, root string) (map[string]any, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	bin := filepath.Join(root, "cutover")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "scripts/cutover-go-work.go")
	build.Dir = repoRoot
	build.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build one-time cutover script: %w\n%s", err, output)
	}
	digest := func(data []byte) string { sum := sha256.Sum256(data); return "sha256:" + hex.EncodeToString(sum[:]) }
	var cases []map[string]any
	for _, name := range []string{"exclusive", "merge", "edited", "wrong-app", "corrupt-receipt", "missing-receipt", "duplicate-block"} {
		appRoot := filepath.Join(root, name)
		ownerPath := filepath.Join(appRoot, ".scenery/editor/go-work-owner.json")
		if err := os.MkdirAll(filepath.Dir(ownerPath), 0o700); err != nil {
			return nil, err
		}
		userBytes := []byte("go 1.27.0\n\nuse .\n// user-owned sentinel\r\n")
		managed := []byte("// scenery:begin managed editor contracts\nuse /retired/scenery/editor/module\n// scenery:end managed editor contracts\n")
		workBytes := append([]byte(nil), userBytes...)
		mode := "exclusive"
		ownedDigest := digest(workBytes)
		if name == "merge" || name == "duplicate-block" {
			mode = "merge"
			workBytes = append(workBytes, managed...)
			ownedDigest = digest(managed)
		}
		owner := map[string]string{"path": "go.work", "mode": mode, "digest": ownedDigest, "application": appRoot, "generator": "scenery.editor-workspace", "spec_revision": "old-spec", "contract_revision": "old-contract"}
		if name == "edited" {
			workBytes = append(workBytes, []byte("// manual edit\n")...)
		}
		if name == "duplicate-block" {
			workBytes = append(workBytes, managed...)
		}
		if name == "wrong-app" {
			owner["application"] = root
		}
		ownerBytes, err := json.Marshal(owner)
		if err != nil {
			return nil, err
		}
		if name == "corrupt-receipt" {
			ownerBytes = []byte("{broken")
		}
		if name != "missing-receipt" {
			if err := os.WriteFile(ownerPath, ownerBytes, 0o600); err != nil {
				return nil, err
			}
		}
		workPath := filepath.Join(appRoot, "go.work")
		if err := os.WriteFile(workPath, workBytes, 0o644); err != nil {
			return nil, err
		}
		sumBytes := []byte("user-owned integrity sentinel\n")
		if err := os.WriteFile(filepath.Join(appRoot, "go.work.sum"), sumBytes, 0o644); err != nil {
			return nil, err
		}
		valid := name == "exclusive" || name == "merge"
		if valid {
			preview := exec.CommandContext(ctx, bin, "--app-root", appRoot)
			output, err := preview.CombinedOutput()
			if err != nil {
				return nil, fmt.Errorf("preview %s: %w\n%s", name, err, output)
			}
			current, err := os.ReadFile(workPath)
			if err != nil || !bytes.Equal(current, workBytes) {
				return nil, fmt.Errorf("preview changed %s workfile", name)
			}
		}
		apply := exec.CommandContext(ctx, bin, "--app-root", appRoot, "--apply")
		output, applyErr := apply.CombinedOutput()
		if (applyErr == nil) != valid {
			return nil, fmt.Errorf("cutover %s validity differs: %v\n%s", name, applyErr, output)
		}
		current, readErr := os.ReadFile(workPath)
		switch name {
		case "exclusive":
			if !os.IsNotExist(readErr) {
				return nil, fmt.Errorf("exclusive workfile was not removed")
			}
		case "merge":
			if readErr != nil || !bytes.Equal(current, userBytes) {
				return nil, fmt.Errorf("merged cutover did not preserve exact user bytes")
			}
		default:
			if readErr != nil || !bytes.Equal(current, workBytes) {
				return nil, fmt.Errorf("unverified cutover changed %s", name)
			}
		}
		current, readErr = os.ReadFile(filepath.Join(appRoot, "go.work.sum"))
		if readErr != nil || !bytes.Equal(current, sumBytes) {
			return nil, fmt.Errorf("cutover changed unauthenticated go.work.sum")
		}
		if name != "missing-receipt" {
			current, readErr = os.ReadFile(ownerPath)
			if readErr != nil || !bytes.Equal(current, ownerBytes) {
				return nil, fmt.Errorf("cutover changed retained ownership evidence")
			}
		}
		cases = append(cases, map[string]any{"case": name, "applied": valid, "output": string(output), "user_bytes_preserved": true})
	}
	return map[string]any{"proof": "explicit_verified_workfile_cutover_and_conflict_preservation", "cases": cases}, nil
}
