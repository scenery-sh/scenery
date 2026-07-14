package evolution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"scenery.sh/internal/spec"
)

func renameReceiptDigest(receipt RenameReceipt) string {
	receipt.Digest = ""
	b, _ := spec.MarshalCanonical(receipt)
	return byteDigest(append([]byte("scenery.rename-receipt\x00"), b...))
}

// RenameReceiptDigest returns the canonical digest bound to a semantic rename
// receipt. Protocol adapters use it when accepting explicit receipts.
func RenameReceiptDigest(receipt RenameReceipt) string { return renameReceiptDigest(receipt) }

func validRenameReceipt(receipt RenameReceipt, baseRevision, targetRevision string) bool {
	return receipt.From != "" && receipt.To != "" &&
		receipt.BaseContractRevision == baseRevision && receipt.TargetContractRevision == targetRevision &&
		receipt.Digest != "" && receipt.Digest == renameReceiptDigest(receipt)
}

func ValidRenameReceipts(base, target *Manifest, receipts []RenameReceipt) []RenameReceipt {
	return ValidRenameReceiptsWithRebinds(base, target, receipts, nil)
}

func ValidRenameReceiptsWithRebinds(base, target *Manifest, receipts []RenameReceipt, rebinds []RevisionRebind) []RenameReceipt {
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
		valid := validRenameReceipt(receipt, baseRevision, targetRevision)
		if !valid && receipt.From != "" && receipt.To != "" && receipt.Digest == renameReceiptDigest(receipt) {
			mappedBase, baseEvidence := rebindContractRevision(receipt.BaseContractRevision, base, rebinds)
			mappedTarget, targetEvidence := rebindContractRevision(receipt.TargetContractRevision, target, rebinds)
			valid = baseEvidence != nil && targetEvidence != nil && mappedBase == baseRevision && mappedTarget == targetRevision
		}
		if !valid {
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

func LoadRenameEvidence(path string) ([]RenameReceipt, []RevisionRebind, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, nil, err
	}
	var envelope struct {
		Renames []RenameReceipt  `json:"rename_receipts"`
		Rebinds []RevisionRebind `json:"revision_rebinds"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode rename evidence: %w", err)
	}
	return envelope.Renames, envelope.Rebinds, nil
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
	receipts, rebinds, err := LoadAppliedRenameEvidence(root)
	if err != nil {
		return nil, err
	}
	return ValidRenameReceiptsWithRebinds(base, target, receipts, rebinds), nil
}

func LoadAppliedRenameEvidence(root string) ([]RenameReceipt, []RevisionRebind, error) {
	directory := filepath.Join(root, ".scenery", "changes", "applied")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	var receipts []RenameReceipt
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		loaded, loadErr := LoadRenameReceipts(filepath.Join(directory, entry.Name()))
		if loadErr != nil {
			return nil, nil, loadErr
		}
		receipts = append(receipts, loaded...)
	}
	rebinds, err := loadRevisionRebinds(filepath.Join(root, ".scenery", "changes", "revision-rebinds"))
	if err != nil {
		return nil, nil, err
	}
	return receipts, rebinds, nil
}

func loadRevisionRebinds(directory string) ([]RevisionRebind, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []RevisionRebind
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		var rebind RevisionRebind
		if decodeErr := json.Unmarshal(data, &rebind); decodeErr != nil {
			return nil, fmt.Errorf("decode revision rebind %s: %w", entry.Name(), decodeErr)
		}
		result = append(result, rebind)
	}
	return result, nil
}
