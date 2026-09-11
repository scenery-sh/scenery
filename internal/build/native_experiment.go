package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const nativeWorkerEntry = "./scenery_native_worker"
const nativeKernelEntry = "./scenery_framework_kernel"

// NativeExperiment contains private feasibility evidence, never a production
// runtime bundle or an admission into the ordinary supervisor's build cache.
type NativeExperiment struct {
	Worker                 string
	Kernel                 *NativeKernelArtifact
	KernelReused           bool
	BuildInput             *BuildInputManifest
	ImplementationRevision string
	ResolvedGoTarget       map[string]any
}

// NativeKernelArtifact is evidence for the kernel's own consumed closure. A
// retained path alone never authorizes reuse; both current inputs and bytes do.
type NativeKernelArtifact struct {
	Binary                 string
	Digest                 string
	BuildInput             *BuildInputManifest
	ImplementationRevision string
}

// CompileNativeExperiment consumes a directly prepared experimental target. Full target patterns, generated native checks,
// framework provenance and independent executable retention remain mandatory.
// Callers must own the app/cache root; this consumes its pending preparation.
func CompileNativeExperiment(ctx context.Context, prepared *Result, retentionDir string, previousKernel *NativeKernelArtifact) (*NativeExperiment, error) {
	if prepared == nil || !prepared.nativeExperiment || prepared.verification == nil || prepared.Contract == nil || prepared.Target == nil {
		return nil, fmt.Errorf("native experiment requires a fresh pending prepared target")
	}
	if !filepath.IsAbs(retentionDir) {
		return nil, fmt.Errorf("native experiment retention directory must be absolute")
	}
	relative, err := filepath.Rel(prepared.Dir, retentionDir)
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return nil, fmt.Errorf("native experiment retention must be outside the prepared workspace")
	}
	unlock, err := lockWorkspace(prepared.Dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := verifyPreparedWorkspace(prepared); err != nil {
		return nil, err
	}
	// Keep publication fields in the caller's ordinary result untouched. The
	// experiment cannot save a successful ordinary build state or prune binaries.
	result := *prepared
	result.GeneratedFiles = slices.Clone(prepared.GeneratedFiles)
	result.BuildInput, result.RuntimeLinkerMetadata, result.ImplementationRevisions = nil, nil, nil
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, &result); err != nil {
			return nil, err
		}
	}
	if err := verifyPreparedWorkspace(&result); err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "scenery-native-experiment-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	worker, kernel := filepath.Join(scratch, "worker"), filepath.Join(scratch, "kernel")
	kernelResult := result
	var kernelReused bool
	err = compileWithPreparedVerification(ctx, &result, func(ctx context.Context) error {
		full, kernelManifest, err := discoverNativeExperimentInputs(ctx, &result)
		if err != nil {
			return err
		}
		if err := applyRuntimeBundleManifest(&result, full); err != nil {
			return err
		}
		if err := applyRuntimeBundleManifest(&kernelResult, kernelManifest); err != nil {
			return err
		}
		if err := validateRuntimeLinkerMetadata(result.RuntimeLinkerMetadata); err != nil {
			return err
		}
		workerFlags := append(slices.Clone(result.GoBuildFlags), "-ldflags=-X=scenery.sh/runtime/worker.linkedInputDigest="+result.BuildInput.Digest+" -X=scenery.sh/runtime/worker.linkedImplementationRevision="+result.ImplementationRevisions[result.Target.Name]+" -X=scenery.sh/runtime/worker.linkedGoTarget="+result.Target.Name)
		workerFlags = withRuntimeBundleLinkerMetadata(workerFlags, result.RuntimeLinkerMetadata)
		if err := buildNativeExperimentEntry(ctx, &result, worker, nativeWorkerEntry, workerFlags); err != nil {
			return err
		}
		kernelReused, err = reusableNativeKernel(previousKernel, &kernelResult, retentionDir)
		if err != nil {
			return err
		}
		if kernelReused {
			kernel = previousKernel.Binary
			return nil
		}
		return buildNativeExperimentEntry(ctx, &kernelResult, kernel, nativeKernelEntry, effectiveGoBuildFlags(&kernelResult))
	})
	if err != nil {
		return nil, err
	}
	return observeBuild(ctx, "experiment.recapture_retention", func() (*NativeExperiment, error) {
		if err := verifyPreparedWorkspace(&result); err != nil {
			return nil, err
		}
		// Re-discover membership and re-hash consumed dependency/module/native bytes;
		// source timestamps and a successful compiler invocation are not freshness.
		after, kernelAfter, err := discoverNativeExperimentInputs(ctx, &result)
		if err != nil {
			return nil, err
		}
		if after.Digest != result.BuildInput.Digest {
			return nil, fmt.Errorf("native experiment build inputs changed during compilation")
		}
		if kernelAfter.Digest != kernelResult.BuildInput.Digest {
			return nil, fmt.Errorf("native kernel inputs changed during compilation")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		retainedWorker, err := RetainBinary(retentionDir, worker)
		if err != nil {
			return nil, err
		}
		retainedKernel, err := RetainBinary(retentionDir, kernel)
		if err != nil {
			return nil, err
		}
		// Retain only preparation hints in this separately owned workspace. There
		// is no successful binary/graph fingerprint for the ordinary cache to admit.
		if err := saveBuildState(result.Dir, buildState{Version: buildStateVersion, DependencyFingerprint: result.DependencyFingerprint, SourceStamps: result.SourceStamps, GeneratedFiles: result.GeneratedFiles}); err != nil {
			return nil, err
		}
		return &NativeExperiment{Worker: retainedWorker, Kernel: &NativeKernelArtifact{Binary: retainedKernel, Digest: strings.TrimPrefix(filepath.Base(retainedKernel), "scenery-app-"), BuildInput: kernelResult.BuildInput, ImplementationRevision: kernelResult.ImplementationRevisions[kernelResult.Target.Name]}, KernelReused: kernelReused, BuildInput: result.BuildInput,
			ImplementationRevision: result.ImplementationRevisions[result.Target.Name], ResolvedGoTarget: result.Target.Resolved}, nil
	})
}

func buildNativeExperimentEntry(ctx context.Context, result *Result, binary, entry string, flags []string) error {
	args := goBuildArgs(binary, flags)
	args[len(args)-1] = entry
	return runGoContextWithEnvironment(ctx, result.Dir, result.GoEnvironment, args...)
}

func reusableNativeKernel(previous *NativeKernelArtifact, current *Result, retentionDir string) (bool, error) {
	if previous == nil || previous.BuildInput == nil || previous.BuildInput.Digest != current.BuildInput.Digest || previous.ImplementationRevision != current.ImplementationRevisions[current.Target.Name] {
		return false, nil
	}
	if !validFrameworkDigest("sha256:"+previous.Digest) || previous.Binary != filepath.Join(retentionDir, "scenery-app-"+previous.Digest) {
		return false, fmt.Errorf("invalid retained native kernel ownership")
	}
	if err := VerifyRetainedBinary(previous.Binary, previous.Digest); err != nil {
		return false, err
	}
	return true, nil
}
