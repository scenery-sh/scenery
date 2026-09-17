package compiler

import (
	"strconv"
	"testing"
)

// Adapter digests are retained by contract revision, which hashes the same
// resource projection; a retained digest equals a fresh computation and the
// retained set stays bounded.
func TestGeneratedAdapterDigestsAreRetainedByContractRevision(t *testing.T) {
	result := func(revision, handler string) *Result {
		return &Result{Manifest: &Manifest{ContractRevision: revision, Resources: []Resource{
			{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"handler": handler}},
		}}}
	}
	for index := range adapterDigestLimit + 2 {
		revision := "sha256:adapter-test-" + strconv.Itoa(index)
		current := result(revision, "Get"+strconv.Itoa(index))
		fresh := computeGeneratedApplicationAdapterDigest(current)
		if first, second := generatedApplicationAdapterDigest(current), generatedApplicationAdapterDigest(current); first != fresh || second != fresh {
			t.Fatalf("revision %s: retained digests %s, %s; fresh %s", revision, first, second, fresh)
		}
	}
	adapterDigests.Lock()
	retained := len(adapterDigests.values)
	adapterDigests.Unlock()
	if retained > adapterDigestLimit {
		t.Fatalf("retained %d adapter digests, limit %d", retained, adapterDigestLimit)
	}
	if uncached := result("", "Other"); generatedApplicationAdapterDigest(uncached) != computeGeneratedApplicationAdapterDigest(uncached) {
		t.Fatal("a result without a contract revision used a retained digest")
	}
}
