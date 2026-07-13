package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

const (
	runtimeBundleKind             = "scenery.runtime-bundle"
	runtimeBundleSchemaDescriptor = machine.ExactSchemaRevision("sha256:6e30118e507d6c984dd95f474b687d57f122dc14cfb8f92a60e17a3651899ba5")
)

type RuntimeBundleDescriptor struct {
	machine.ArtifactIdentity
	ArtifactKind           string              `json:"artifact_kind"`
	Application            string              `json:"application"`
	Target                 string              `json:"target"`
	ContractRevision       string              `json:"contract_revision"`
	ImplementationRevision string              `json:"implementation_revision"`
	BuildInput             *BuildInputManifest `json:"build_input_manifest"`
	ResolvedGoTarget       map[string]any      `json:"resolved_go_target"`
	RuntimeABI             string              `json:"runtime_abi"`
}

func prepareRuntimeBundle(ctx context.Context, result *Result) error {
	if result.Contract == nil || result.Target == nil {
		return nil
	}
	manifest, err := buildInputManifest(ctx, result)
	if err != nil {
		return err
	}
	revisions, diagnostics := compiler.ComputeImplementationRevisions(result.Contract, map[string]string{result.Target.Name: manifest.Digest})
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
		}
	}
	implementationRevision := revisions[result.Target.Name]
	if implementationRevision == "" {
		return fmt.Errorf("implementation_revision is unavailable for Go target %s", result.Target.Name)
	}
	result.BuildInput = manifest
	result.ImplementationRevisions = revisions
	result.GoBuildFlags = withRuntimeBundleLinkerMetadata(result.GoBuildFlags, map[string]string{
		"scenery.sh/runtime.linkedContractRevision":       result.Contract.Manifest.ContractRevision,
		"scenery.sh/runtime.linkedImplementationRevision": implementationRevision,
		"scenery.sh/runtime.linkedBuildInputDigest":       manifest.Digest,
		"scenery.sh/runtime.linkedGoTarget":               result.Target.Name,
	})
	return nil
}

func writeRuntimeBundle(result *Result) error {
	if result.Contract == nil || result.Target == nil || result.BuildInput == nil {
		return nil
	}
	descriptor := RuntimeBundleDescriptor{
		ArtifactIdentity: machine.NewArtifactIdentity(runtimeBundleKind, runtimeBundleSchemaDescriptor), ArtifactKind: "go_runtime_bundle",
		Application: result.Contract.Manifest.Application.Name, Target: result.Target.Name,
		ContractRevision:       result.Contract.Manifest.ContractRevision,
		ImplementationRevision: result.ImplementationRevisions[result.Target.Name], BuildInput: result.BuildInput,
		ResolvedGoTarget: result.Target.Resolved, RuntimeABI: "scenery.go-runtime/v1",
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	return writeBundleFile(RuntimeBundlePath(result.AppRoot, result.Target.Name), append(data, '\n'))
}

func RuntimeBundlePath(appRoot, target string) string {
	return filepath.Join(appRoot, ".scenery", "build", "runtime", target+".json")
}

func ReadRuntimeBundle(appRoot, target string) (RuntimeBundleDescriptor, error) {
	data, err := os.ReadFile(RuntimeBundlePath(appRoot, target))
	if err != nil {
		return RuntimeBundleDescriptor{}, err
	}
	var descriptor RuntimeBundleDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return RuntimeBundleDescriptor{}, fmt.Errorf("invalid scenery.runtime-bundle descriptor; rebuild with the current Scenery CLI")
	}
	if err := machine.ValidateArtifactIdentity(descriptor.ArtifactIdentity, runtimeBundleKind, runtimeBundleSchemaDescriptor, "rebuild"); err != nil {
		return RuntimeBundleDescriptor{}, err
	}
	if descriptor.BuildInput != nil {
		if err := machine.ValidateArtifactIdentity(descriptor.BuildInput.ArtifactIdentity, buildInputKind, buildInputSchemaDescriptor, "rebuild"); err != nil {
			return RuntimeBundleDescriptor{}, err
		}
	}
	if descriptor.Target != target || descriptor.BuildInput == nil || descriptor.BuildInput.Target != target || descriptor.BuildInput.Digest == "" || descriptor.ImplementationRevision == "" {
		return RuntimeBundleDescriptor{}, fmt.Errorf("invalid scenery.runtime-bundle identity; rebuild with the current Scenery CLI")
	}
	return descriptor, nil
}

func withRuntimeBundleLinkerMetadata(flags []string, values map[string]string) []string {
	result := make([]string, 0, len(flags)+1)
	ldflags := ""
	for index := 0; index < len(flags); index++ {
		flag := flags[index]
		switch {
		case strings.HasPrefix(flag, "-ldflags="):
			ldflags += " " + strings.TrimPrefix(flag, "-ldflags=")
		case flag == "-ldflags" && index+1 < len(flags):
			index++
			ldflags += " " + flags[index]
		default:
			result = append(result, flag)
		}
	}
	keys := []string{
		"scenery.sh/runtime.linkedContractRevision", "scenery.sh/runtime.linkedImplementationRevision",
		"scenery.sh/runtime.linkedBuildInputDigest", "scenery.sh/runtime.linkedGoTarget",
	}
	for _, key := range keys {
		ldflags += " -X=" + key + "=" + values[key]
	}
	return append(result, "-ldflags="+strings.TrimSpace(ldflags))
}

func writeBundleFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".runtime-bundle-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
