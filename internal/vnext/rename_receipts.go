package vnext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func validRenameReceipt(receipt RenameReceipt, baseRevision, targetRevision string) bool {
	return receipt.From != "" && receipt.To != "" &&
		receipt.BaseContractRevision == baseRevision && receipt.TargetContractRevision == targetRevision &&
		receipt.Digest != "" && receipt.Digest == renameReceiptDigest(receipt)
}

func ValidRenameReceipts(base, target *Manifest, receipts []RenameReceipt) []RenameReceipt {
	baseRevision, targetRevision := "", ""
	if base != nil {
		baseRevision = base.ContractRevision
	}
	if target != nil {
		targetRevision = target.ContractRevision
	}
	seen := map[string]bool{}
	result := make([]RenameReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if !validRenameReceipt(receipt, baseRevision, targetRevision) {
			continue
		}
		key := receipt.From + "\x00" + receipt.To + "\x00" + receipt.Digest
		if !seen[key] {
			seen[key] = true
			result = append(result, receipt)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].From != result[j].From {
			return result[i].From < result[j].From
		}
		return result[i].To < result[j].To
	})
	return result
}

func LoadRenameReceipts(path string) ([]RenameReceipt, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var receipts []RenameReceipt
	if err := json.Unmarshal(b, &receipts); err == nil {
		return receipts, nil
	}
	var envelope struct {
		Renames []RenameReceipt `json:"rename_receipts"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, fmt.Errorf("decode rename receipts: %w", err)
	}
	return envelope.Renames, nil
}

func LoadAppliedRenameReceipts(root string, base, target *Manifest) ([]RenameReceipt, error) {
	directory := filepath.Join(root, ".scenery", "changes", "applied")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipts []RenameReceipt
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		loaded, loadErr := LoadRenameReceipts(filepath.Join(directory, entry.Name()))
		if loadErr != nil {
			return nil, loadErr
		}
		receipts = append(receipts, loaded...)
	}
	return ValidRenameReceipts(base, target, receipts), nil
}
