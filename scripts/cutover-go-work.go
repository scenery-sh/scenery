//go:build ignore

// One-time, explicit migration of the retired Scenery editor workfile.
// Run from the Scenery checkout; this is not part of the CLI/runtime protocol.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/scn"
)

type oldWorkOwner struct {
	Path             string `json:"path"`
	Mode             string `json:"mode,omitempty"`
	Digest           string `json:"digest"`
	PreviousDigest   string `json:"previous_digest,omitempty"`
	Application      string `json:"application"`
	Generator        string `json:"generator"`
	SpecRevision     string `json:"spec_revision"`
	ContractRevision string `json:"contract_revision"`
}

func main() {
	root := flag.String("app-root", "", "exact application root owning the retired workfile")
	apply := flag.Bool("apply", false, "apply the verified cutover; default only checks and previews")
	flag.Parse()
	if *root == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/cutover-go-work.go --app-root /absolute/app [--apply]")
		os.Exit(2)
	}
	if err := cutover(*root, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "Cutover stopped:", err)
		fmt.Fprintln(os.Stderr, "Review go.work and .scenery/editor/go-work-owner.json manually. Never delete go.work.sum or shared caches based on this record.")
		os.Exit(1)
	}
}

func cutover(appRoot string, apply bool) error {
	root, err := filepath.Abs(appRoot)
	if err != nil {
		return err
	}
	ownerPath := filepath.Join(root, ".scenery/editor/go-work-owner.json")
	workPath := filepath.Join(root, "go.work")
	ownerBytes, _, err := regularBytes(root, ownerPath)
	if err != nil {
		return fmt.Errorf("read ownership evidence: %w", err)
	}
	var owner oldWorkOwner
	decoder := json.NewDecoder(bytes.NewReader(ownerBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("ownership record contains trailing data")
	}
	if owner.Path != "go.work" || owner.Application != root || owner.Generator != "scenery.editor-workspace" {
		return fmt.Errorf("ownership record does not identify this exact application's go.work")
	}
	before, mode, err := regularBytes(root, workPath)
	if errors.Is(err, os.ErrNotExist) && (owner.Mode == "exclusive" || owner.Mode == "") {
		fmt.Println("go.work is already absent; no changes made. Ownership evidence and go.work.sum are preserved.")
		return nil
	}
	if err != nil {
		return err
	}
	var after []byte
	remove := false
	switch owner.Mode {
	case "", "exclusive":
		if digest(before) != owner.Digest && (owner.PreviousDigest == "" || digest(before) != owner.PreviousDigest) {
			return fmt.Errorf("exclusive go.work digest differs from ownership evidence")
		}
		tracked := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", "go.work")
		if tracked.Run() == nil {
			return fmt.Errorf("go.work is tracked; review the publishing/workspace decision manually")
		}
		remove = true
	case "merge":
		begin := []byte("// scenery:begin managed editor contracts\n")
		end := []byte("// scenery:end managed editor contracts\n")
		if bytes.Count(before, begin) != 1 || bytes.Count(before, end) != 1 {
			return fmt.Errorf("expected exactly one managed block with exact markers")
		}
		start, finish := bytes.Index(before, begin), bytes.Index(before, end)
		if finish < start {
			return fmt.Errorf("managed block markers are out of order")
		}
		finish += len(end)
		if start > 0 && before[start-1] != '\n' {
			return fmt.Errorf("managed block does not begin on a line boundary")
		}
		if digest(before[start:finish]) != owner.Digest {
			return fmt.Errorf("managed block digest differs from ownership evidence")
		}
		after = append(append([]byte(nil), before[:start]...), before[finish:]...)
	default:
		return fmt.Errorf("unknown ownership mode %q", owner.Mode)
	}
	if !apply {
		fmt.Printf("Verified %s ownership. --apply will remove only %s; go.work.sum, owner evidence, Git ignores and global caches remain unchanged.\n", owner.Mode, map[bool]string{true: "the exclusive go.work", false: "the exact managed block"}[remove])
		return nil
	}
	// The operator must stop old Scenery/editor writers before this one-time
	// operation. Recheck both inputs immediately before the bounded replacement.
	currentOwner, _, err := regularBytes(root, ownerPath)
	if err != nil || !bytes.Equal(ownerBytes, currentOwner) {
		return fmt.Errorf("ownership evidence changed during cutover")
	}
	current, _, err := regularBytes(root, workPath)
	if err != nil || !bytes.Equal(before, current) {
		return fmt.Errorf("go.work changed during cutover")
	}
	if remove {
		err = os.Remove(workPath)
	} else {
		err = atomicfile.Write(workPath, after, mode.Perm(), atomicfile.Options{SyncFile: true, SyncDir: true})
	}
	if err != nil {
		return err
	}
	fmt.Println("Verified cutover applied. go.work.sum, ownership evidence, Git ignores and global caches were preserved. Run the candidate scenery generate --target contracts before raw Go tooling.")
	return nil
}

func regularBytes(root, path string) ([]byte, os.FileMode, error) {
	if err := scn.RejectPathSymlinks(root, path); err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, info.Mode(), err
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
