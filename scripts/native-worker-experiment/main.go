// Command native-worker-experiment builds plan 0180's explicit feasibility
// control or worker pair. It does not select a production runtime or start apps.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/generate"
	generateapi "scenery.sh/internal/generate/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	appRoot := flag.String("app-root", "", "explicit owned fixture root")
	output := flag.String("output", "", "private experiment cache and evidence directory")
	mode := flag.String("mode", "", "baseline, control (matched post-build checks), or worker")
	binding := flag.String("binding", "", "compiled unary HTTP binding for the worker")
	verifyKernel := flag.Bool("verify-kernel-projection", false, "audit kernel identity against independent discovery; excluded from timed series")
	flag.Parse()
	if flag.NArg() != 0 || *appRoot == "" || *output == "" || (*mode != "baseline" && *mode != "control" && *mode != "worker") || (*mode == "worker" && *binding == "") {
		return fmt.Errorf("require --app-root, --output, --mode baseline|control|worker and worker --binding")
	}
	started := time.Now()
	if _, err := build.VerifyFrameworkProducer(); err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(*appRoot)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		return err
	}
	out, err = filepath.EvalSymlinks(out)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if out == root {
		return fmt.Errorf("experiment output cannot be the app root")
	}
	marker := filepath.Join(out, ".native-worker-experiment-root")
	contents, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(root + "\n")
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err != nil {
		return err
	} else if string(contents) != root+"\n" {
		return fmt.Errorf("experiment output belongs to another app root")
	}
	restore := devcache.SetRoot(filepath.Join(out, "cache"))
	defer restore()
	build.SetGenerateHooks(build.GenerateHooks{
		ApplyPreparedImplementationCheck: generate.ApplyPreparedImplementationCheck,
		SyncCachedTypeScript:             func(r *compiler.Result) error { _, err := generate.SyncCachedTypeScriptClients(r); return err },
		PrepareBuildGoWorkspace:          generate.PrepareBuildGoWorkspace,
		RuntimeIntegrationPlan:           generate.BuildRuntimeIntegrationPlan,
		RenderAssistantAssets:            generate.RenderAssistantAssetRegistry,
	})
	var steps []build.Step
	ctx := build.WithTrace(context.Background(), func(step build.Step) { steps = append(steps, step) })
	prepareStarted := time.Now()
	var prepared *build.Result
	if *mode == "worker" {
		prepared, err = build.PrepareNativeExperiment(ctx, root, cfg, filepath.Join(out, "native-workspace"), func(result *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
			return generate.PrepareNativeWorkerGoWorkspace(result, cfg, *binding)
		})
	} else {
		prepared, err = build.PrepareForCompileWithSnapshotContext(ctx, root, cfg, nil)
	}
	if err != nil {
		return err
	}
	steps = append(steps, build.Step{Name: "experiment.preparation", StartedAt: prepareStarted, Duration: time.Since(prepareStarted), OK: true})
	var candidate *build.NativeExperiment
	if *mode == "worker" {
		var previous struct{ Evidence *build.NativeExperiment }
		if data, err := os.ReadFile(filepath.Join(out, "worker-evidence.json")); err == nil {
			if err := json.Unmarshal(data, &previous); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		var previousKernel *build.NativeKernelArtifact
		if previous.Evidence != nil {
			previousKernel = previous.Evidence.Kernel
		}
		candidate, err = build.CompileNativeExperiment(ctx, prepared, filepath.Join(out, "retained"), previousKernel)
		if err != nil {
			return err
		}
		if *verifyKernel {
			if err := build.VerifyNativeExperimentKernel(ctx, prepared, candidate); err != nil {
				return err
			}
		}
	} else {
		if err := build.CompileContext(ctx, prepared); err != nil {
			return err
		}
		if *mode == "control" {
			if err := build.VerifyNativeExperimentControl(ctx, prepared); err != nil {
				return err
			}
		}
		retentionStarted := time.Now()
		retained, err := build.RetainBinary(filepath.Join(out, "retained"), prepared.Binary)
		if err != nil {
			return err
		}
		steps = append(steps, build.Step{Name: "experiment.control_retention", StartedAt: retentionStarted, Duration: time.Since(retentionStarted), OK: true})
		candidate = &build.NativeExperiment{Worker: retained, BuildInput: prepared.BuildInput, ImplementationRevision: prepared.ImplementationRevisions[prepared.Target.Name], ResolvedGoTarget: prepared.Target.Resolved}
	}
	record := struct {
		Mode             string
		Evidence         *build.NativeExperiment
		Workspace        string
		ContractRevision string
		ElapsedMS        float64
		Steps            []build.Step
	}{*mode, candidate, prepared.Dir, prepared.Contract.Manifest.ContractRevision, float64(time.Since(started)) / float64(time.Millisecond), steps}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	// The receipt is emitted only after the complete checker/build/retention path.
	if *mode == "worker" {
		if err := os.WriteFile(filepath.Join(out, "worker-evidence.json"), append(data, '\n'), 0600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "build-evidence.json"), append(data, '\n'), 0600)
}
