package evolution

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/spec"
)

var errChangeReceiptExists = errors.New("change receipt already exists")

const requiredChangeCapability = "scenery.agent-mutation"

func normalizeApplyCapabilities(plan ChangePlan, options ApplyOptions) ApplyOptions {
	if len(options.GrantedCapabilities) == 0 && options.Caller == "local" {
		options.GrantedCapabilities = canonicalStrings(plan.Capabilities)
		if len(options.GrantedCapabilities) == 0 {
			options.GrantedCapabilities = []string{requiredChangeCapability}
		}
	}
	options.GrantedCapabilities = canonicalStrings(options.GrantedCapabilities)
	return options
}

func validatePlanCapabilities(plan ChangePlan, options ApplyOptions) error {
	granted := map[string]bool{}
	for _, capability := range canonicalStrings(options.GrantedCapabilities) {
		granted[capability] = true
	}
	for _, required := range canonicalStrings(plan.RequiredCapabilities) {
		if !granted[required] {
			return fmt.Errorf("permission_denied: required capability %s is unavailable", required)
		}
	}
	for _, claimed := range canonicalStrings(plan.Capabilities) {
		if !granted[claimed] {
			return fmt.Errorf("permission_denied: plan capability %s is not granted", claimed)
		}
	}
	return nil
}

// LoadIssuedChangePlan reads the exact plan retained by PlanChanges. The
// plan_id names both the file and the content identity; a caller cannot
// replace the retained plan with a recomputed variant.
func LoadIssuedChangePlan(root, planID string) (ChangePlan, error) {
	path, err := issuedPlanPath(root, IssuedChangePlan, planID)
	if err != nil {
		return ChangePlan{}, err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		if err := scn.RejectPathSymlinks(root, filepath.Dir(path)); err != nil {
			return ChangePlan{}, fmt.Errorf("failed_precondition: inspect issued plan path: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return ChangePlan{}, fmt.Errorf("failed_precondition: inspect issued plan: %w", statErr)
	}
	encoded, err := readStrictArtifactFile(path, "issued plan")
	if err != nil {
		return ChangePlan{}, err
	}
	var plan ChangePlan
	if err := decodeArtifactExact(encoded, &plan); err != nil {
		return ChangePlan{}, fmt.Errorf("failed_precondition: decode issued plan: %w", err)
	}
	canonical, err := spec.MarshalCanonical(plan)
	if err != nil {
		return ChangePlan{}, fmt.Errorf("failed_precondition: canonicalize issued plan: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(encoded), canonical) {
		return ChangePlan{}, fmt.Errorf("failed_precondition: issued plan is not canonical")
	}
	if err := validateLoadedChangePlan(plan, planID); err != nil {
		return ChangePlan{}, err
	}
	return plan, nil
}

// LoadAppliedChangeReceipt reads one durable receipt by plan ID. A missing
// receipt is reported as a failed precondition; callers that need to
// distinguish a first apply use the internal found result below.
func LoadAppliedChangeReceipt(root, planID string) (ChangeReceipt, error) {
	plan, err := LoadIssuedChangePlan(root, planID)
	if err != nil {
		return ChangeReceipt{}, err
	}
	receipt, found, err := loadAppliedChangeReceipt(root, planID)
	if err != nil {
		return ChangeReceipt{}, err
	}
	if !found {
		return ChangeReceipt{}, fmt.Errorf("failed_precondition: applied change receipt is unavailable")
	}
	if err := validateChangeReceiptAgainstPlan(receipt, plan); err != nil {
		return ChangeReceipt{}, err
	}
	return receipt, nil
}

func loadAppliedChangeReceiptForPlan(root string, plan ChangePlan) (ChangeReceipt, bool, error) {
	receipt, found, err := loadAppliedChangeReceipt(root, plan.PlanID)
	if err != nil || !found {
		return ChangeReceipt{}, found, err
	}
	if err := validateChangeReceiptAgainstPlan(receipt, plan); err != nil {
		return ChangeReceipt{}, false, err
	}
	return receipt, true, nil
}

func loadAppliedChangeReceipt(root, planID string) (ChangeReceipt, bool, error) {
	path, err := appliedChangeReceiptPath(root, planID)
	if err != nil {
		return ChangeReceipt{}, false, err
	}
	_, statErr := os.Lstat(path)
	if statErr == nil {
		if err := scn.RejectPathSymlinks(root, filepath.Dir(path)); err != nil {
			return ChangeReceipt{}, false, fmt.Errorf("failed_precondition: inspect applied change receipt path: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return ChangeReceipt{}, false, fmt.Errorf("failed_precondition: inspect applied change receipt: %w", statErr)
	}
	if os.IsNotExist(statErr) {
		return ChangeReceipt{}, false, nil
	}
	encoded, err := readStrictArtifactFile(path, "applied change receipt")
	if err != nil {
		return ChangeReceipt{}, false, err
	}
	var receipt ChangeReceipt
	if err := decodeArtifactExact(encoded, &receipt); err != nil {
		return ChangeReceipt{}, false, fmt.Errorf("failed_precondition: decode applied change receipt: %w", err)
	}
	canonical, err := spec.MarshalCanonical(receipt)
	if err != nil {
		return ChangeReceipt{}, false, fmt.Errorf("failed_precondition: canonicalize applied change receipt: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(encoded), canonical) {
		return ChangeReceipt{}, false, fmt.Errorf("failed_precondition: applied change receipt is not canonical")
	}
	if err := validateLoadedChangeReceipt(receipt, planID); err != nil {
		return ChangeReceipt{}, false, err
	}
	return receipt, true, nil
}

func validateLoadedChangePlan(plan ChangePlan, requestedPlanID string) error {
	if !graph.IsCanonicalSHA256Digest(requestedPlanID) || plan.PlanID != requestedPlanID {
		return fmt.Errorf("failed_precondition: issued change plan ID does not match requested plan_id")
	}
	if err := machine.ValidateArtifactIdentity(plan.ArtifactIdentity, changePlanKind, changePlanSchemaDescriptor, "re-plan"); err != nil {
		return fmt.Errorf("failed_precondition: invalid issued change plan identity: %w", err)
	}
	if strings.TrimSpace(plan.Application) == "" || strings.TrimSpace(plan.Caller) == "" || strings.TrimSpace(plan.ImplementationStatus) == "" || strings.TrimSpace(plan.DeploymentStatus) == "" || plan.ExpiresAt.IsZero() {
		return fmt.Errorf("failed_precondition: issued change plan is incomplete")
	}
	if !graph.IsCanonicalSHA256Digest(plan.BaseWorkspaceRevision) || !graph.IsCanonicalSHA256Digest(plan.PredictedWorkspaceRevision) || !graph.IsCanonicalSHA256Digest(plan.PredictedContractRevision) {
		return fmt.Errorf("failed_precondition: issued change plan has invalid revisions")
	}
	if plan.BaseContractRevision != nil && !graph.IsCanonicalSHA256Digest(*plan.BaseContractRevision) {
		return fmt.Errorf("failed_precondition: issued change plan has an invalid base contract revision")
	}
	if plan.OperationsDigest != semanticOperationsDigest(plan.Operations) {
		return fmt.Errorf("failed_precondition: issued change plan operations digest mismatch")
	}
	if plan.SemanticDiff.Kind != "" {
		if err := machine.ValidateArtifactIdentity(plan.SemanticDiff.ArtifactIdentity, semanticDiffKind, semanticDiffSchemaDescriptor, "re-plan"); err != nil {
			return fmt.Errorf("failed_precondition: issued change plan semantic diff identity is invalid: %w", err)
		}
		if plan.SemanticDiff.Digest != semanticDiffDigest(plan.SemanticDiff) {
			return fmt.Errorf("failed_precondition: issued change plan semantic diff digest mismatch")
		}
	}
	if changePlanID(plan) != requestedPlanID {
		return fmt.Errorf("failed_precondition: issued change plan identity mismatch")
	}
	return nil
}

func validateLoadedChangeReceipt(receipt ChangeReceipt, requestedPlanID string) error {
	if !graph.IsCanonicalSHA256Digest(requestedPlanID) || receipt.PlanID != requestedPlanID {
		return fmt.Errorf("failed_precondition: applied change receipt plan_id mismatch")
	}
	if err := machine.ValidateArtifactIdentity(receipt.ArtifactIdentity, changeReceiptKind, changeReceiptSchemaDescriptor, "recover the applied change"); err != nil {
		return fmt.Errorf("failed_precondition: invalid applied change receipt identity: %w", err)
	}
	if !graph.IsCanonicalSHA256Digest(receipt.WorkspaceRevision) || !graph.IsCanonicalSHA256Digest(receipt.ContractRevision) {
		return fmt.Errorf("failed_precondition: applied change receipt has invalid revisions")
	}
	if strings.TrimSpace(receipt.ImplementationStatus) == "" || strings.TrimSpace(receipt.DeploymentStatus) == "" {
		return fmt.Errorf("failed_precondition: applied change receipt has invalid revision status")
	}
	if !sortedUniqueStrings(receipt.Applied) {
		return fmt.Errorf("failed_precondition: applied change receipt paths are not sorted and unique")
	}
	for _, rename := range receipt.Renames {
		if rename.From == "" || rename.To == "" || !graph.IsCanonicalSHA256Digest(rename.BaseContractRevision) || !graph.IsCanonicalSHA256Digest(rename.TargetContractRevision) || rename.Digest != renameReceiptDigest(rename) {
			return fmt.Errorf("failed_precondition: applied change receipt contains invalid rename evidence")
		}
	}
	return nil
}

func validateChangeReceiptAgainstPlan(receipt ChangeReceipt, plan ChangePlan) error {
	if receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision || receipt.ContractRevision != plan.PredictedContractRevision {
		return fmt.Errorf("failed_precondition: applied change receipt revisions do not match the issued plan")
	}
	if receipt.ImplementationStatus != plan.ImplementationStatus || receipt.DeploymentStatus != plan.DeploymentStatus {
		return fmt.Errorf("failed_precondition: applied change receipt status does not match the issued plan")
	}
	expectedApplied := make([]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		expectedApplied = append(expectedApplied, edit.Path)
	}
	sort.Strings(expectedApplied)
	if !equalStrings(expectedApplied, receipt.Applied) {
		return fmt.Errorf("failed_precondition: applied change receipt file set does not match the issued plan")
	}
	if len(receipt.Renames) != len(plan.Renames) {
		return fmt.Errorf("failed_precondition: applied change receipt rename evidence does not match the issued plan")
	}
	for index := range plan.Renames {
		if receipt.Renames[index] != plan.Renames[index] {
			return fmt.Errorf("failed_precondition: applied change receipt rename evidence does not match the issued plan")
		}
	}
	return nil
}

func sortedUniqueStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func appliedChangeReceiptPath(root, planID string) (string, error) {
	if !graph.IsCanonicalSHA256Digest(planID) {
		return "", fmt.Errorf("failed_precondition: applied change receipt identity is invalid")
	}
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return confinedPath(root, filepath.ToSlash(filepath.Join(".scenery", "changes", "applied", name)))
}

func readStrictArtifactFile(path, family string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed_precondition: %s is unavailable", family)
		}
		return nil, fmt.Errorf("failed_precondition: inspect %s: %w", family, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failed_precondition: %s record is invalid", family)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed_precondition: read %s: %w", family, err)
	}
	return encoded, nil
}

func writeChangeReceiptOnce(root, planID string, data []byte) error {
	path, err := appliedChangeReceiptPath(root, planID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := scn.RejectPathSymlinks(root, filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errChangeReceiptExists
	} else if !os.IsNotExist(err) {
		return err
	}
	// Write a same-directory temporary file, then link it into the final name.
	// Link is atomic and never replaces an existing receipt. A crash before the
	// link leaves no commit marker, so workspacetx recovery can safely roll the
	// source back.
	temporary := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		if os.IsExist(err) {
			return errChangeReceiptExists
		}
		return err
	}
	removeTemporary = false
	_ = os.Remove(temporary)
	directory, err := os.Open(filepath.Dir(path))
	if err == nil {
		// The receipt is already visible and immutable. A directory fsync error
		// must not make the caller roll source edits back while leaving this
		// durable-looking commit marker behind; replay can recover the receipt.
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
