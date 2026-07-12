package vnext

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	issuedChangePlan     = "change"
	issuedDeploymentPlan = "deployment"
)

func retainIssuedPlan(root, kind, planID string, plan any) error {
	path, err := issuedPlanPath(root, kind, planID)
	if err != nil {
		return err
	}
	canonical, err := MarshalCanonical(plan)
	if err != nil {
		return fmt.Errorf("retain issued plan: %w", err)
	}
	if stored, readErr := os.ReadFile(path); readErr == nil {
		if bytes.Equal(bytes.TrimSpace(stored), canonical) {
			return nil
		}
		return fmt.Errorf("internal: issued plan identity collision")
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return atomicWriteSynced(path, append(canonical, '\n'), 0o600)
}

func requireIssuedPlan(root, kind, planID string, plan any) error {
	path, err := issuedPlanPath(root, kind, planID)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("failed_precondition: issued plan is unavailable")
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("failed_precondition: issued plan record is invalid")
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	canonical, err := MarshalCanonical(plan)
	if err != nil || !bytes.Equal(bytes.TrimSpace(stored), canonical) {
		return fmt.Errorf("permission_denied: supplied plan differs from the issued plan")
	}
	return nil
}

func issuedPlanPath(root, kind, planID string) (string, error) {
	if !isCanonicalSHA256Digest(planID) || kind == "" || strings.Trim(kind, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return "", fmt.Errorf("failed_precondition: issued plan identity is invalid")
	}
	relative := filepath.ToSlash(filepath.Join(".scenery", "plans", "issued", kind, strings.TrimPrefix(planID, "sha256:")+".json"))
	return confinedPath(root, relative)
}
