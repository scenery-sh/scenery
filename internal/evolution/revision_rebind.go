package evolution

import (
	"fmt"
	"sort"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

// RevisionRebind is additional migration evidence that maps one historical
// contract revision onto its current revision-scheme identity. It is valid
// only for the exact canonical contract projection named by ProjectionHash.
type RevisionRebind struct {
	FromSpecRevision     string `json:"from_spec_revision"`
	ToSpecRevision       string `json:"to_spec_revision"`
	FromContractRevision string `json:"from_contract_revision"`
	ToContractRevision   string `json:"to_contract_revision"`
	ProjectionHash       string `json:"contract_projection_hash"`
	Reason               string `json:"reason"`
	Digest               string `json:"digest"`
}

func revisionRebindDigest(rebind RevisionRebind) string {
	rebind.Digest = ""
	encoded, _ := spec.MarshalCanonical(rebind)
	return byteDigest(append([]byte("scenery.revision-rebind\x00"), encoded...))
}

func NewRevisionRebind(fromSpec, fromContract string, current *Manifest, reason string) (RevisionRebind, error) {
	if current == nil || fromSpec == "" || fromContract == "" || reason == "" {
		return RevisionRebind{}, fmt.Errorf("revision rebind requires historical identity, current manifest, and reason")
	}
	rebind := RevisionRebind{
		FromSpecRevision: fromSpec, ToSpecRevision: current.SpecRevision,
		FromContractRevision: fromContract, ToContractRevision: current.ContractRevision,
		ProjectionHash: graph.ContractProjectionHash(current), Reason: reason,
	}
	rebind.Digest = revisionRebindDigest(rebind)
	return rebind, nil
}

func validRevisionRebind(rebind RevisionRebind, current *Manifest) bool {
	return current != nil && rebind.FromSpecRevision != "" && rebind.ToSpecRevision == current.SpecRevision &&
		rebind.FromContractRevision != "" && rebind.ToContractRevision == current.ContractRevision &&
		rebind.ProjectionHash == graph.ContractProjectionHash(current) && rebind.Reason != "" &&
		rebind.Digest == revisionRebindDigest(rebind)
}

func validRevisionRebinds(current *Manifest, candidates []RevisionRebind) []RevisionRebind {
	var result []RevisionRebind
	for _, candidate := range candidates {
		if validRevisionRebind(candidate, current) {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FromContractRevision < result[j].FromContractRevision })
	return result
}

func rebindContractRevision(from string, current *Manifest, candidates []RevisionRebind) (string, *RevisionRebind) {
	if current != nil && from == current.ContractRevision {
		return from, nil
	}
	for _, rebind := range validRevisionRebinds(current, candidates) {
		if rebind.FromContractRevision == from {
			copy := rebind
			return rebind.ToContractRevision, &copy
		}
	}
	return from, nil
}
