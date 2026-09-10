package build

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"

	"scenery.sh/internal/machine"
)

// CandidateIdentity is build evidence, not proof of a serving process. The
// accompanying helper binds individual HTTP responses to this exact generation.
type CandidateIdentity struct {
	ContractRevision          string           `json:"contractRevision"`
	ImplementationRevision    string           `json:"implementationRevision"`
	BuildInputDigest          string           `json:"buildInputDigest"`
	Target                    string           `json:"target"`
	SpecRevision              string           `json:"specRevision"`
	Producer                  machine.Producer `json:"producer"`
	FrameworkSourceDigest     string           `json:"frameworkSourceDigest"`
	FrameworkExecutableDigest string           `json:"frameworkExecutableDigest"`
}

//go:embed runtime_identity.ts
var runtimeVerificationModule []byte

func RuntimeVerificationModuleDigest() string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(runtimeVerificationModule))
}

func RuntimeVerificationModuleSource() string { return string(runtimeVerificationModule) }

// VerifyCurrentCandidate reads the existing workspace without regenerating,
// compiling Go, or publishing build state. Both authored and consumed inputs
// must still match; a stale cache is a failure, never authority to claim parity.
func VerifyCurrentCandidate(ctx context.Context, root string, cfg app.Config, snapshot func() (*SourceSnapshot, error)) (CandidateIdentity, error) {
	workspace, err := workspaceDir(root, cfg.Name)
	if err != nil {
		return CandidateIdentity{}, err
	}
	state, err := loadBuildState(workspace)
	if err != nil {
		return CandidateIdentity{}, err
	}
	if err := verifyCurrentSourceStateWithSnapshot(root, workspace, state, snapshot); err != nil {
		return CandidateIdentity{}, err
	}
	contract, err := compiler.Compile(root)
	if err != nil {
		return CandidateIdentity{}, err
	}
	if !contract.Valid() {
		return CandidateIdentity{}, fmt.Errorf("current application declaration is invalid")
	}
	target, err := compiler.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		return CandidateIdentity{}, err
	}
	bundle, err := ReadRuntimeBundle(root, target.Name)
	if err != nil {
		return CandidateIdentity{}, err
	}
	identity, err := VerifyCandidate(ctx, root, target.Name, RuntimeBundlePath(root, target.Name))
	if err != nil {
		return CandidateIdentity{}, err
	}
	if bundle.Application != contract.Manifest.Application.Name || bundle.ContractRevision != contract.Manifest.ContractRevision {
		return CandidateIdentity{}, fmt.Errorf("current declaration differs from the built candidate")
	}
	revisions, diagnostics := compiler.ComputeImplementationRevisions(contract, map[string]string{target.Name: bundle.BuildInput.Digest})
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return CandidateIdentity{}, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
		}
	}
	if revisions[target.Name] != bundle.ImplementationRevision {
		return CandidateIdentity{}, fmt.Errorf("current target differs from the built candidate")
	}
	// Reuse the producer's canonical input calculation, including dependency
	// modules, embeds and native files, while forbidding module-file updates.
	target.Context.BuildFlags = append(append([]string(nil), target.Context.BuildFlags...), "-mod=readonly")
	inputs, err := buildInputManifest(ctx, &Result{AppRoot: root, Dir: workspace, Target: &target})
	if err != nil {
		return CandidateIdentity{}, err
	}
	if inputs.Digest != bundle.BuildInput.Digest {
		return CandidateIdentity{}, fmt.Errorf("current build inputs differ from the candidate; wait for a successful runtime rebuild")
	}
	if err := verifyCurrentSourceStateWithSnapshot(root, workspace, state, snapshot); err != nil {
		return CandidateIdentity{}, err
	}
	currentBundle, err := ReadRuntimeBundle(root, target.Name)
	if err != nil {
		return CandidateIdentity{}, err
	}
	if !reflect.DeepEqual(currentBundle, bundle) {
		return CandidateIdentity{}, fmt.Errorf("runtime bundle changed during candidate inspection; retry")
	}
	return identity, nil
}

func verifyCurrentSourceState(root, workspace string, state buildState) error {
	return verifyCurrentSourceStateWithSnapshot(root, workspace, state, nil)
}

func verifyCurrentSourceStateWithSnapshot(root, workspace string, state buildState, snapshot func() (*SourceSnapshot, error)) error {
	if state.Version != buildStateVersion || state.SourceFingerprint == "" || state.BuildFingerprint == "" {
		return fmt.Errorf("current build state is missing; build or start the application first")
	}
	var currentSnapshot *SourceSnapshot
	var err error
	if state.GraphFingerprint != "" {
		if snapshot == nil {
			return fmt.Errorf("runtime candidate requires a current runtime source snapshot")
		}
		currentSnapshot, err = snapshot()
		if err != nil {
			return err
		}
	}
	source, err := currentAppSourceFingerprintWithSnapshot(root, currentSnapshot)
	if err != nil {
		return err
	}
	if source != state.SourceFingerprint {
		return fmt.Errorf("current authored source differs from the candidate; wait for a successful runtime rebuild")
	}
	fingerprint, err := workspaceBuildFingerprint(workspace, state.GoBuildFlags, sourceFilesFromStamps(state.SourceStamps), state.GeneratedFiles)
	if err != nil {
		return err
	}
	if fingerprint != state.BuildFingerprint {
		return fmt.Errorf("candidate workspace inputs changed (built=%s current=%s); rebuild the application", state.BuildFingerprint, fingerprint)
	}
	if _, err := os.Stat(filepath.Join(workspace, workspaceBinaryName(root, state.BuildFingerprint))); err != nil {
		return fmt.Errorf("candidate executable is missing: %w", err)
	}
	current, err := loadBuildState(workspace)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, state) {
		return fmt.Errorf("build state changed during candidate inspection; retry")
	}
	return nil
}

var identityDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// VerifyCandidate validates the current bundle and selected producer in Go,
// keeping serialization, schema and source verification out of consumer apps.
func VerifyCandidate(ctx context.Context, root, target, descriptorPath string) (CandidateIdentity, error) {
	selection, err := ReadFrameworkSelection(root)
	if err != nil {
		return CandidateIdentity{}, err
	}
	if err := VerifyFrameworkSelection(ctx, selection); err != nil {
		return CandidateIdentity{}, err
	}
	bundle, err := ReadRuntimeBundleFile(descriptorPath, target)
	if err != nil {
		return CandidateIdentity{}, err
	}
	return candidateIdentity(bundle, selection)
}

func candidateIdentity(bundle RuntimeBundleDescriptor, selection FrameworkSelection) (CandidateIdentity, error) {
	if err := machine.ValidateArtifactIdentity(bundle.ArtifactIdentity, runtimeBundleKind, runtimeBundleSchemaDescriptor, "rebuild"); err != nil {
		return CandidateIdentity{}, err
	}
	manifest := bundle.BuildInput
	if manifest == nil {
		return CandidateIdentity{}, fmt.Errorf("candidate lacks build-input manifest")
	}
	if err := machine.ValidateArtifactIdentity(manifest.ArtifactIdentity, buildInputKind, buildInputSchemaDescriptor, "rebuild"); err != nil {
		return CandidateIdentity{}, err
	}
	if bundle.Target == "" || manifest.Target != bundle.Target || !identityDigest.MatchString(bundle.ContractRevision) || !identityDigest.MatchString(bundle.ImplementationRevision) {
		return CandidateIdentity{}, fmt.Errorf("candidate runtime identity is incomplete")
	}
	entries := make(map[string]string, len(manifest.Entries))
	previous := ""
	for _, entry := range manifest.Entries {
		if entry.Identity <= previous || !identityDigest.MatchString(entry.Digest) {
			return CandidateIdentity{}, fmt.Errorf("invalid or unordered build-input entry")
		}
		previous = entry.Identity
		entries[entry.Identity] = entry.Digest
	}
	// The producer participates in the digest. Never re-stamp a retained artifact.
	if !reflect.DeepEqual(manifest.Producer, machine.RuntimeProducer()) || !reflect.DeepEqual(bundle.Producer, manifest.Producer) {
		return CandidateIdentity{}, fmt.Errorf("candidate belongs to another producer")
	}
	if newBuildInputManifest(manifest.Target, entries).Digest != manifest.Digest {
		return CandidateIdentity{}, fmt.Errorf("build-input manifest digest does not match its contents")
	}
	if entries["framework/scenery.sh/source"] != selection.Source.Digest || entries["producer/scenery-cli/executable"] != selection.ExecutableDigest {
		return CandidateIdentity{}, fmt.Errorf("candidate was built with another framework producer")
	}
	return CandidateIdentity{bundle.ContractRevision, bundle.ImplementationRevision, manifest.Digest, bundle.Target, bundle.SpecRevision, bundle.Producer, selection.Source.Digest, selection.ExecutableDigest}, nil
}

// WriteRuntimeVerificationModule publishes only bytes embedded in this producer.
func WriteRuntimeVerificationModule(path string) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("verification module must be a regular file")
	}
	return writeBundleFile(path, runtimeVerificationModule)
}
