package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/scn"
)

const SQLCReceiptPath = ".scenery/artifacts/sqlc.json"
const sqlcReceiptDescriptor = "scenery.sqlc-generated{kind:string,schema_revision:sha256,owner:string,input_revision:sha256,tools:map[string]sha256,roots:[]path,files:map[path]sha256}"

// SQLCArtifactReceipt records exact publication ownership. Classification and
// publication share this decoder; invalid receipts never exclude authored input.
type SQLCArtifactReceipt struct {
	Kind           string            `json:"kind"`
	SchemaRevision string            `json:"schema_revision"`
	Owner          string            `json:"owner"`
	InputRevision  string            `json:"input_revision"`
	Tools          map[string]string `json:"tools"`
	Roots          []string          `json:"roots"`
	Files          map[string]string `json:"files"`
}

func SQLCReceiptSchemaRevision() string {
	sum := sha256.Sum256([]byte(sqlcReceiptDescriptor))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReadSQLCArtifactReceipt returns an empty file set for a missing receipt and
// refuses malformed or foreign ownership before any caller uses its claims.
func ReadSQLCArtifactReceipt(root string) (SQLCArtifactReceipt, error) {
	path := filepath.Join(root, filepath.FromSlash(SQLCReceiptPath))
	if err := scn.RejectPathSymlinks(root, path); err != nil && !os.IsNotExist(err) {
		return SQLCArtifactReceipt{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return SQLCArtifactReceipt{Files: map[string]string{}}, nil
	}
	if err != nil {
		return SQLCArtifactReceipt{}, err
	}
	var receipt SQLCArtifactReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return receipt, fmt.Errorf("failed_precondition: SQLC output ownership receipt is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return receipt, fmt.Errorf("failed_precondition: SQLC output ownership receipt has trailing data")
	}
	if receipt.Kind != "scenery.sqlc-generated" || receipt.SchemaRevision != SQLCReceiptSchemaRevision() || receipt.Owner != root || receipt.Files == nil || !canonicalSQLCDigest(receipt.InputRevision) || !canonicalSQLCDigest(receipt.Tools["sqlc"]) {
		return receipt, fmt.Errorf("failed_precondition: SQLC output ownership receipt does not match this workspace")
	}
	for _, digest := range receipt.Tools {
		if !canonicalSQLCDigest(digest) {
			return receipt, fmt.Errorf("failed_precondition: invalid SQLC tool identity")
		}
	}
	for path, digest := range receipt.Files {
		if !safeSQLCReceiptPath(path) || !canonicalSQLCDigest(digest) || !sqlcReceiptPathInRoots(path, receipt.Roots) {
			return receipt, fmt.Errorf("failed_precondition: unsafe SQLC output ownership receipt")
		}
	}
	for _, root := range receipt.Roots {
		if !safeSQLCReceiptPath(root) {
			return receipt, fmt.Errorf("failed_precondition: unsafe SQLC output root")
		}
	}
	return receipt, nil
}

func canonicalSQLCDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		return false
	}
	decoded, err := hex.DecodeString(digest[7:])
	return err == nil && hex.EncodeToString(decoded) == digest[7:]
}
func safeSQLCReceiptPath(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../") && !strings.HasPrefix(path, ".scenery/") && !strings.Contains(path, "\\")
}
func sqlcReceiptPathInRoots(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
