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

	"scenery.sh/internal/vnext"
)

type VNextRuntimeBundleDescriptor struct {
	APIVersion             string                   `json:"api_version"`
	ArtifactKind           string                   `json:"artifact_kind"`
	Application            string                   `json:"application"`
	Target                 string                   `json:"target"`
	ContractRevision       string                   `json:"contract_revision"`
	ImplementationRevision string                   `json:"implementation_revision"`
	BuildInput             *VNextBuildInputManifest `json:"build_input_manifest"`
	ResolvedGoTarget       map[string]any           `json:"resolved_go_target"`
	RuntimeABI             string                   `json:"runtime_abi"`
}

func prepareVNextRuntimeBundle(ctx context.Context, result *Result) error {
	if result.VNextContract == nil || result.VNextTarget == nil {
		return nil
	}
	manifest, err := buildVNextInputManifest(ctx, result)
	if err != nil {
		return err
	}
	revisions, diagnostics := vnext.ComputeImplementationRevisions(result.VNextContract, map[string]string{result.VNextTarget.Name: manifest.Digest})
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
		}
	}
	implementationRevision := revisions[result.VNextTarget.Name]
	if implementationRevision == "" {
		return fmt.Errorf("implementation_revision is unavailable for Go target %s", result.VNextTarget.Name)
	}
	result.VNextBuildInput = manifest
	result.ImplementationRevisions = revisions
	result.GoBuildFlags = withRuntimeBundleLinkerMetadata(result.GoBuildFlags, map[string]string{
		"scenery.sh/runtime.linkedContractRevision":       result.VNextContract.Manifest.ContractRevision,
		"scenery.sh/runtime.linkedImplementationRevision": implementationRevision,
		"scenery.sh/runtime.linkedBuildInputDigest":       manifest.Digest,
		"scenery.sh/runtime.linkedGoTarget":               result.VNextTarget.Name,
	})
	return nil
}

func writeVNextRuntimeBundle(result *Result) error {
	if result.VNextContract == nil || result.VNextTarget == nil || result.VNextBuildInput == nil {
		return nil
	}
	descriptor := VNextRuntimeBundleDescriptor{
		APIVersion: "scenery.runtime-bundle/v1", ArtifactKind: "go_runtime_bundle",
		Application: result.VNextContract.Manifest.Application.Name, Target: result.VNextTarget.Name,
		ContractRevision:       result.VNextContract.Manifest.ContractRevision,
		ImplementationRevision: result.ImplementationRevisions[result.VNextTarget.Name], BuildInput: result.VNextBuildInput,
		ResolvedGoTarget: result.VNextTarget.Resolved, RuntimeABI: "scenery.go-runtime/v1",
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	return writeVNextBundleFile(VNextRuntimeBundlePath(result.AppRoot, result.VNextTarget.Name), append(data, '\n'))
}

func VNextRuntimeBundlePath(appRoot, target string) string {
	return filepath.Join(appRoot, ".scenery", "build", "vnext", target+".json")
}

func ReadVNextRuntimeBundle(appRoot, target string) (VNextRuntimeBundleDescriptor, error) {
	data, err := os.ReadFile(VNextRuntimeBundlePath(appRoot, target))
	if err != nil {
		return VNextRuntimeBundleDescriptor{}, err
	}
	var descriptor VNextRuntimeBundleDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return VNextRuntimeBundleDescriptor{}, fmt.Errorf("invalid scenery.runtime-bundle/v1 descriptor")
	}
	if descriptor.APIVersion != "scenery.runtime-bundle/v1" || descriptor.Target != target || descriptor.BuildInput == nil || descriptor.BuildInput.Target != target || descriptor.BuildInput.Digest == "" || descriptor.ImplementationRevision == "" {
		return VNextRuntimeBundleDescriptor{}, fmt.Errorf("invalid scenery.runtime-bundle/v1 identity")
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

func writeVNextBundleFile(path string, data []byte) error {
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
