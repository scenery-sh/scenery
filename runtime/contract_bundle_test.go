package runtime

import (
	"strings"
	"testing"
)

func TestVerifyLinkedContractBundleFailsClosed(t *testing.T) {
	old := CurrentLinkedContractBundle()
	t.Cleanup(func() {
		linkedContractRevision = old.ContractRevision
		linkedImplementationRevision = old.ImplementationRevision
		linkedBuildInputDigest = old.BuildInputDigest
		linkedGoTarget = old.GoTarget
	})
	revision := "sha256:" + strings.Repeat("a", 64)
	linkedContractRevision, linkedImplementationRevision = revision, revision
	linkedBuildInputDigest, linkedGoTarget = revision, "development"
	if err := VerifyLinkedContractBundle(revision); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLinkedContractBundle("sha256:" + strings.Repeat("b", 64)); err == nil {
		t.Fatal("contract mismatch was accepted")
	}
}
