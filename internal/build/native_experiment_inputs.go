package build

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Both artifact identities use the same fresh full-target resolution and bytes.
// The kernel projection is not permission to omit any native target inputs.
func discoverNativeExperimentInputs(ctx context.Context, result *Result) (*BuildInputManifest, *BuildInputManifest, error) {
	patterns := append(slices.Clone(result.Target.Context.Patterns), nativeWorkerEntry, nativeKernelEntry)
	output, err := captureBuildInputGraph(ctx, result, patterns)
	if err != nil {
		return nil, nil, err
	}
	full, err := observeBuild(ctx, "go.input_fingerprint", func() (*BuildInputManifest, error) {
		return buildInputManifestFromGoList(result, output)
	})
	if err != nil {
		return nil, nil, err
	}
	kernel, err := observeBuild(ctx, "experiment.kernel_projection", func() (*BuildInputManifest, error) {
		return projectNativeKernelInputs(result, output, full)
	})
	return full, kernel, err
}

// Project exact package/module identities, not string prefixes that might also
// select unrelated descendants. Missing graph edges or byte evidence fail closed.
func projectNativeKernelInputs(result *Result, output []byte, full *BuildInputManifest) (*BuildInputManifest, error) {
	packages := map[string]goListPackage{}
	entry := ""
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode native experiment input graph: %w", err)
		}
		if _, duplicate := packages[pkg.ImportPath]; duplicate || pkg.ImportPath == "" {
			return nil, fmt.Errorf("native experiment has duplicate or unnamed input package")
		}
		packages[pkg.ImportPath] = pkg
		if filepath.Clean(pkg.Dir) == filepath.Join(result.Dir, filepath.FromSlash(nativeKernelEntry)) {
			entry = pkg.ImportPath
		}
	}
	if entry == "" {
		return nil, fmt.Errorf("native kernel entrypoint missing from complete input graph")
	}
	wanted := map[string]bool{}
	seen := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[name] {
			continue
		}
		pkg, exists := packages[name]
		if !exists {
			return nil, fmt.Errorf("native kernel dependency missing from complete input graph: %s", name)
		}
		seen[name] = true
		queue = append(queue, pkg.Imports...)
		if pkg.Standard {
			continue
		}
		for _, file := range consumedPackageFiles(pkg) {
			wanted["package/"+pkg.ImportPath+"/"+filepath.ToSlash(file)] = true
		}
		if pkg.Module != nil {
			wanted["module/"+pkg.Module.Path] = true
			module := pkg.Module
			if module.Replace != nil {
				module = module.Replace
			}
			if module.GoMod != "" {
				wanted["module/"+pkg.Module.Path+"/go.mod"] = true
			}
		}
	}
	entries := map[string]string{}
	for _, input := range full.Entries {
		if wanted[input.Identity] || strings.HasPrefix(input.Identity, "native/") ||
			strings.HasPrefix(input.Identity, "framework/") || strings.HasPrefix(input.Identity, "producer/") {
			entries[input.Identity] = input.Digest
			delete(wanted, input.Identity)
		}
	}
	if len(wanted) != 0 {
		return nil, fmt.Errorf("native kernel consumed inputs missing from complete byte evidence")
	}
	return newBuildInputManifest(result.Target.Name, entries), nil
}

// VerifyNativeExperimentControl adds the candidate's full post-build membership
// and byte check to an explicitly labeled control. Ordinary CompileContext stays
// unchanged; the experiment must call this before retaining/emitting its receipt.
func VerifyNativeExperimentControl(ctx context.Context, result *Result) (returnedErr error) {
	started := time.Now()
	defer func() {
		finishStep(ctx, "experiment.control_recapture", started, "not_applicable", "full_input_recapture", returnedErr)
	}()
	if result == nil || result.BuildInput == nil || result.verification != nil {
		return fmt.Errorf("native experiment control requires a completely verified build")
	}
	unlock, err := lockWorkspace(result.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	if err := verifyPreparedWorkspace(result); err != nil {
		return err
	}
	after, err := buildInputManifest(ctx, result)
	if err != nil {
		return err
	}
	if after.Digest != result.BuildInput.Digest {
		return fmt.Errorf("native experiment control inputs changed during compilation")
	}
	return ctx.Err()
}

// VerifyNativeExperimentKernel is the explicit, untimed audit proof that the
// projected kernel identity equals independent Go resolution of that entrypoint.
func VerifyNativeExperimentKernel(ctx context.Context, prepared *Result, candidate *NativeExperiment) error {
	if candidate == nil || candidate.Kernel == nil {
		return fmt.Errorf("native experiment has no kernel evidence")
	}
	independent := *prepared
	manifest, err := discoverBuildInputManifest(ctx, &independent, []string{nativeKernelEntry})
	if err != nil {
		return err
	}
	if manifest.Digest != candidate.Kernel.BuildInput.Digest {
		return fmt.Errorf("projected native kernel inputs differ from independent discovery")
	}
	return nil
}
