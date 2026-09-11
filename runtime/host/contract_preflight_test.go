package host

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"scenery.sh/internal/storageconfig"
)

func TestRuntimePreflightChecksLinkedBundleAndSupervisorStorage(t *testing.T) {
	old := CurrentLinkedContractBundle()
	t.Cleanup(func() {
		linkedContractRevision, linkedImplementationRevision = old.ContractRevision, old.ImplementationRevision
		linkedBuildInputDigest, linkedGoTarget = old.BuildInputDigest, old.GoTarget
	})
	revision := "sha256:" + strings.Repeat("a", 64)
	linkedContractRevision, linkedImplementationRevision = revision, revision
	linkedBuildInputDigest, linkedGoTarget = revision, "development"
	t.Setenv(storageconfig.RuntimeConfigEnv, "")
	var output bytes.Buffer
	if err := WriteRuntimePreflight(&output, revision); err != nil {
		t.Fatal(err)
	}
	proof, err := DecodeRuntimePreflight(output.Bytes())
	if err != nil || proof.ContractRevision != revision || proof.BuildInputDigest != revision || proof.RuntimeABI != ContractRuntimeABI {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
	proof.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	invalid, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRuntimePreflight(invalid); err == nil {
		t.Fatal("different supervisor specification accepted")
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, `{"kind":"scenery.storage.runtime","stores":{}}`)
	output.Reset()
	if err := WriteRuntimePreflight(&output, revision); err == nil || output.Len() != 0 {
		t.Fatalf("invalid storage descriptor passed preflight: %v", err)
	}
}
