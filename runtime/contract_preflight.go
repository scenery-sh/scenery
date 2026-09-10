package runtime

import (
	"encoding/json"
	"io"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/storageconfig"
)

// RuntimePreflightFlag selects the generated binary's read-only handshake. It
// runs before SQL/auth initialization, composition registration and workers.
const RuntimePreflightFlag = "--scenery-runtime-preflight"

const runtimePreflightKind = "scenery.runtime-preflight"
const runtimePreflightSchema = `{"identity":"artifact","contract_revision":"digest","implementation_revision":"digest","build_input_digest":"digest","go_target":"string","runtime_abi":"string"}`

type RuntimePreflight struct {
	machine.ArtifactIdentity
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	GoTarget               string `json:"go_target"`
	RuntimeABI             string `json:"runtime_abi"`
}

// WriteRuntimePreflight validates the linked contract and supervisor-provided
// storage descriptor without opening a listener or starting application work.
// Application packages must also keep their Go init functions free of writes.
func WriteRuntimePreflight(w io.Writer, contractRevision string) error {
	if err := VerifyLinkedContractBundle(contractRevision); err != nil {
		return err
	}
	if _, _, err := storageconfig.LoadRuntimeConfigValue(envpolicy.Get(storageconfig.RuntimeConfigEnv)); err != nil {
		return err
	}
	bundle := CurrentLinkedContractBundle()
	return json.NewEncoder(w).Encode(RuntimePreflight{
		ArtifactIdentity: machine.NewArtifactIdentity(runtimePreflightKind, runtimePreflightSchema),
		ContractRevision: bundle.ContractRevision, ImplementationRevision: bundle.ImplementationRevision,
		BuildInputDigest: bundle.BuildInputDigest, GoTarget: bundle.GoTarget, RuntimeABI: ContractRuntimeABI,
	})
}

func DecodeRuntimePreflight(data []byte) (RuntimePreflight, error) {
	var result RuntimePreflight
	err := machine.DecodeArtifact(data, &result, &result.ArtifactIdentity, runtimePreflightKind, runtimePreflightSchema, "rebuild the application with the selected Scenery CLI")
	return result, err
}
