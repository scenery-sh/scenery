package runtime

import (
	"encoding/hex"
	"fmt"
	"strings"
)

var (
	linkedContractRevision       string
	linkedImplementationRevision string
	linkedBuildInputDigest       string
	linkedGoTarget               string
)

type LinkedContractBundle struct {
	ContractRevision       string
	ImplementationRevision string
	BuildInputDigest       string
	GoTarget               string
}

func VerifyLinkedContractBundle(contractRevision string) error {
	bundle := CurrentLinkedContractBundle()
	if bundle.ContractRevision != contractRevision {
		return fmt.Errorf("runtime bundle contract_revision mismatch: linked %q, generated %q", bundle.ContractRevision, contractRevision)
	}
	if !canonicalBundleRevision(bundle.ContractRevision) || !canonicalBundleRevision(bundle.ImplementationRevision) || !canonicalBundleRevision(bundle.BuildInputDigest) || strings.TrimSpace(bundle.GoTarget) == "" {
		return fmt.Errorf("runtime bundle implementation metadata is unavailable or invalid")
	}
	return nil
}

func CurrentLinkedContractBundle() LinkedContractBundle {
	return LinkedContractBundle{
		ContractRevision: linkedContractRevision, ImplementationRevision: linkedImplementationRevision,
		BuildInputDigest: linkedBuildInputDigest, GoTarget: linkedGoTarget,
	}
}

func canonicalBundleRevision(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
