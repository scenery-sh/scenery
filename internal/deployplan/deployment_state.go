package deployplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
)

func recoverDeploymentJournals(ctx context.Context, root string, providers DeploymentProviderRegistry) error {
	directory := filepath.Join(root, ".scenery", "deployments", "journal")
	if _, err := confinedDeploymentPath(root, directory, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("internal: read deployment recovery journal: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("internal: read deployment recovery journal: %w", err)
		}
		if hasLegacyAPIVersion(data, "scenery.deployment-apply-journal/v1") {
			return legacyRecoveryStateError("deployment", path)
		}
		var journal deploymentApplyJournal
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&journal); err != nil || decoder.Decode(&struct{}{}) != io.EOF || machine.ValidateArtifactIdentity(journal.ArtifactIdentity, deploymentApplyJournalKind, deploymentJournalSchemaDescriptor, "re-plan") != nil || machine.ValidateArtifactIdentity(journal.Plan.ArtifactIdentity, deploymentPlanKind, deploymentPlanSchemaDescriptor, "re-plan") != nil || journal.Plan.PlanID == "" || deploymentPlanID(journal.Plan) != journal.Plan.PlanID {
			return fmt.Errorf("internal: invalid deployment recovery journal %s", entry.Name())
		}
		appliedPath := deploymentAppliedPlanPath(root, journal.Plan.PlanID)
		if _, exists, receiptErr := readDeploymentReceipt(root, appliedPath, journal.Plan); receiptErr != nil {
			return fmt.Errorf("internal: validate committed deployment receipt: %w", receiptErr)
		} else if exists {
			if err := removeDeploymentFile(root, path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("internal: remove committed deployment journal: %w", err)
			}
			continue
		}
		if journal.Committed {
			return fmt.Errorf("internal: committed deployment journal %s has no receipt", entry.Name())
		}
		if journal.RestoreState {
			if err := restoreDeploymentState(root, journal); err != nil {
				return fmt.Errorf("internal: recover deployment state: %w", err)
			}
		}
		for _, planIndex := range slices.Backward(journal.Applied) {

			if planIndex < 0 || planIndex >= len(journal.Plan.ProviderPlans) {
				return fmt.Errorf("internal: deployment recovery journal has invalid provider index")
			}
			providerPlan := journal.Plan.ProviderPlans[planIndex]
			adapter := lookupDeploymentProvider(providers, providerPlan.ProviderAddress, providerPlan.ProviderSource)
			recovery, ok := adapter.(DeploymentProviderRecovery)
			if !ok {
				return fmt.Errorf("capability_unavailable: provider %s cannot recover interrupted deployment", providerPlan.ProviderAddress)
			}
			if err := recovery.Rollback(ctx, providerPlan); err != nil {
				return fmt.Errorf("internal: recover provider %s: %w", providerPlan.ProviderAddress, err)
			}
		}
		if err := removeDeploymentFile(root, path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("internal: remove recovered deployment journal: %w", err)
		}
	}
	return nil
}

func restoreDeploymentState(root string, journal deploymentApplyJournal) error {
	path := deploymentStatePath(root, journal.Plan.DeploymentName)
	if journal.PreviousStateExists {
		return writeDeploymentFile(root, path, journal.PreviousState)
	}
	err := removeDeploymentFile(root, path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func deploymentJournalPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "deployments", "journal", name)
}

func writeDeploymentJournal(root, path string, journal deploymentApplyJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeDeploymentFile(root, path, append(data, '\n'))
}

func acquireDeploymentApplyLock(root string) (func(), error) {
	directory := filepath.Join(root, ".scenery", "deployments")
	if _, err := confinedDeploymentPath(root, directory, true); err != nil {
		return nil, fmt.Errorf("failed_precondition: deployment state directory is unsafe: %w", err)
	}
	path := filepath.Join(directory, "apply.lock")
	var encoded []byte
	for range 3 {
		if _, err := confinedDeploymentPath(root, path, true); err != nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock is unsafe: %w", err)
		}
		var err error
		if _, statErr := os.Lstat(path); statErr == nil {
			err = os.ErrExist
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		} else {
			if encoded == nil {
				owner := localagent.CurrentOwner("deployment-apply")
				lock := deploymentApplyLock{ArtifactIdentity: machine.NewArtifactIdentity(deploymentApplyLockKind, deploymentLockSchemaDescriptor), Owner: owner}
				encoded, _ = json.Marshal(lock)
			}
			err = writeExclusiveSyncedFile(path, append(encoded, '\n'), 0o600)
		}
		if err == nil {
			// The lock is live coordination state, not a crash-recovery marker.
			// Its contents are synced so any surviving entry remains parseable,
			// but the directory entry need not survive: provider effects begin only
			// after their recovery journal is durable.
			owned := true
			return func() {
				if owned {
					_ = removeDeploymentFileUnsynced(root, path)
					owned = false
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		data, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock cannot be read: %w", readErr)
		}
		if hasLegacyAPIVersion(data, "scenery.deployment-apply-lock/v1") {
			return nil, legacyRecoveryStateError("deployment", path)
		}
		var existing deploymentApplyLock
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&existing); decodeErr != nil || decoder.Decode(&struct{}{}) != io.EOF || machine.ValidateArtifactIdentity(existing.ArtifactIdentity, deploymentApplyLockKind, deploymentLockSchemaDescriptor, "retry") != nil || existing.Owner.PID <= 0 {
			return nil, fmt.Errorf("failed_precondition: deployment apply lock is invalid")
		}
		if localagent.VerifyOwner(existing.Owner) == nil {
			return nil, fmt.Errorf("failed_precondition: deployment apply is active in process %d", existing.Owner.PID)
		}
		if err := removeDeploymentFile(root, path); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("failed_precondition: deployment apply lock is contended")
}

func writeDeploymentFile(root, path string, data []byte) error {
	target, err := confinedDeploymentPath(root, path, true)
	if err != nil {
		return fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("failed_precondition: deployment target is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteSynced(target, data, 0o600)
}

func readDeploymentFile(root, path string) ([]byte, bool, error) {
	target, err := confinedDeploymentPath(root, path, false)
	if err != nil {
		return nil, false, fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("failed_precondition: deployment target is not a regular file")
	}
	if err := rejectPathSymlinks(root, filepath.Dir(target)); err != nil {
		return nil, false, fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func readDeploymentReceipt(root, path string, plan DeploymentPlan) (DeploymentReceipt, bool, error) {
	encoded, exists, err := readDeploymentFile(root, path)
	if err != nil {
		return DeploymentReceipt{}, false, err
	}
	if !exists {
		return DeploymentReceipt{}, false, nil
	}
	var receipt DeploymentReceipt
	if err := machine.DecodeArtifact(encoded, &receipt, &receipt.ArtifactIdentity, deploymentReceiptKind, deploymentReceiptSchemaDescriptor, "re-apply"); err != nil {
		return DeploymentReceipt{}, true, fmt.Errorf("failed_precondition: deployment receipt is invalid: %w", err)
	}
	if err := validateDeploymentReceipt(plan, receipt); err != nil {
		return DeploymentReceipt{}, true, fmt.Errorf("failed_precondition: deployment receipt is invalid: %w", err)
	}
	return receipt, true, nil
}

func validateDeploymentReceipt(plan DeploymentPlan, receipt DeploymentReceipt) error {
	if receipt.PlanID != plan.PlanID || !isCanonicalSHA256Digest(receipt.PlanID) {
		return fmt.Errorf("plan identity mismatch")
	}
	if receipt.Application == "" || receipt.Application != plan.Application {
		return fmt.Errorf("application mismatch")
	}
	if receipt.Deployment == "" || receipt.Deployment != plan.Deployment {
		return fmt.Errorf("deployment mismatch")
	}
	if receipt.WorkspaceRevision != plan.BaseWorkspaceRevision || !isCanonicalSHA256Digest(receipt.WorkspaceRevision) {
		return fmt.Errorf("workspace revision mismatch")
	}
	if receipt.ContractRevision != plan.ContractRevision || !isCanonicalSHA256Digest(receipt.ContractRevision) {
		return fmt.Errorf("contract revision mismatch")
	}
	if !canonicalRevisionMap(receipt.ImplementationRevision) || !reflect.DeepEqual(receipt.ImplementationRevision, plan.ImplementationRevision) {
		return fmt.Errorf("implementation revision mismatch")
	}
	if receipt.DeploymentRevision != plan.DeploymentRevision || !isCanonicalSHA256Digest(receipt.DeploymentRevision) {
		return fmt.Errorf("deployment revision mismatch")
	}
	expectedDigests := deploymentProviderPlanDigests(plan.ProviderPlans)
	if !reflect.DeepEqual(receipt.ProviderPlanDigests, expectedDigests) {
		return fmt.Errorf("provider plan digests mismatch")
	}
	for index, digest := range receipt.ProviderPlanDigests {
		if !isCanonicalSHA256Digest(digest) || (index > 0 && receipt.ProviderPlanDigests[index-1] >= digest) {
			return fmt.Errorf("provider plan digests are not canonical")
		}
	}
	if receipt.AppliedAt.IsZero() {
		return fmt.Errorf("applied_at is missing")
	}
	return nil
}

func removeDeploymentFile(root, path string) error {
	return removeDeploymentFileWithSync(root, path, true)
}

func removeDeploymentFileUnsynced(root, path string) error {
	return removeDeploymentFileWithSync(root, path, false)
}

func removeDeploymentFileWithSync(root, path string, sync bool) error {
	target, err := confinedDeploymentPath(root, path, false)
	if err != nil {
		return fmt.Errorf("failed_precondition: deployment path is unsafe: %w", err)
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return err
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("failed_precondition: deployment target is not a regular file")
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	if !sync {
		return nil
	}
	return syncDirectory(filepath.Dir(target))
}

func confinedDeploymentPath(root, path string, createParent bool) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escape")
	}
	target := filepath.Join(absoluteRoot, relative)
	if createParent {
		if err := mkdirAllDeploymentPath(absoluteRoot, filepath.Dir(target)); err != nil {
			return "", err
		}
		if err := rejectPathSymlinks(absoluteRoot, filepath.Dir(target)); err != nil {
			return "", err
		}
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("path contains symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func mkdirAllDeploymentPath(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escape")
	}
	current := root
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("path contains non-directory or symlink")
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
