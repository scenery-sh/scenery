//go:build scenery_native_lifecycle_probe

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/generate"
	generateapi "scenery.sh/internal/generate/api"
)

// This alternate executable exposes no product CLI grammar or runtime selector.
// One process owns one arm, watcher baseline and preparation caches until EOF.
func main() {
	if err := runNativeLifecycleProbe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runNativeLifecycleProbe() error {
	rootFlag := flag.String("app-root", "", "owned source fixture")
	output := flag.String("output", "", "owned cache and artifact directory")
	mode := flag.String("mode", "", "control or worker")
	binding := flag.String("binding", "", "explicit experimental unary binding")
	flag.Parse()
	if flag.NArg() != 0 || *rootFlag == "" || *output == "" || (*mode != "control" && *mode != "worker") || (*mode == "worker" && *binding == "") {
		return fmt.Errorf("require --app-root --output --mode control|worker and worker --binding")
	}
	root, cfg, err := app.DiscoverRoot(*rootFlag)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	out, err = filepath.EvalSymlinks(out)
	if err != nil {
		return err
	}
	if out == root {
		return fmt.Errorf("probe output cannot be app root")
	}
	marker := filepath.Join(out, ".native-lifecycle-owner")
	want := root + "\n" + *mode + "\n" + *binding + "\n"
	if b, e := os.ReadFile(marker); e == nil {
		if string(b) != want {
			return fmt.Errorf("probe output has another owner")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else {
		f, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(want)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	restore := devcache.SetRoot(filepath.Join(out, "cache"))
	defer restore()
	wireBuildGenerateHooks()
	var native *nativeDevPreparation
	if *mode == "worker" {
		native = &nativeDevPreparation{workspace: filepath.Join(out, "native-workspace"), project: func(r *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
			return generate.PrepareNativeWorkerGoWorkspace(r, cfg, *binding)
		}}
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	var baseline fileSnapshot
	var kernel *build.NativeKernelArtifact
	generation := 0
	for {
		var request struct {
			Label                string
			PauseDuringCompile   bool
			PauseBeforeRecapture bool
		}
		if err := decoder.Decode(&request); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		generation++
		started := time.Now()
		var steps []build.Step
		var mu sync.Mutex
		var captured fileSnapshot
		var prepared *devBuildPreparation
		var candidate *build.NativeExperiment
		var capturedAt time.Time
		pause := func(phase string) error {
			if err := encoder.Encode(map[string]any{"Event": phase, "Label": request.Label, "PID": os.Getpid()}); err != nil {
				return err
			}
			var resume struct{ Continue bool }
			if err := decoder.Decode(&resume); err != nil {
				return err
			}
			if !resume.Continue {
				return fmt.Errorf("probe continuation required")
			}
			return nil
		}
		baseCtx, cancel := context.WithCancel(context.Background())
		var pauseOnce sync.Once
		var pauseError error
		ctx := build.WithTrace(baseCtx, func(s build.Step) {
			mu.Lock()
			steps = append(steps, s)
			mu.Unlock()
			if request.PauseDuringCompile && s.Name == "go.input_discovery" {
				pauseOnce.Do(func() {
					if err := pause("during_compile"); err != nil {
						mu.Lock()
						pauseError = err
						mu.Unlock()
						cancel()
					}
				})
			}
		})
		buildErr := func() error {
			var err error
			if baseline.files == nil {
				captured, err = scanInitialWatchedFiles(root, compiler.Compile)
			} else {
				captured, err = scanWatchedFilesReusing(root, baseline)
			}
			if err != nil {
				return err
			}
			capturedAt = time.Now()
			// Same framework verification and shared preparation as the supervisor.
			if err = build.VerifyFrameworkSession(ctx, root); err != nil {
				return err
			}
			prepared, err = loadDevBuildPreparation(ctx, root, cfg, captured, native)
			if err != nil {
				return err
			}
			prepStart := time.Now()
			if err = prepared.prepare(ctx); err != nil {
				return err
			}
			mu.Lock()
			steps = append(steps, build.Step{Name: "experiment.preparation", StartedAt: prepStart, Duration: time.Since(prepStart), OK: true})
			mu.Unlock()
			if err = prepared.analyze(); err != nil {
				return err
			}
			return prepared.compile(ctx, func(ctx context.Context, r *build.Result) error {
				if native != nil {
					candidate, err = build.CompileNativeExperiment(ctx, r, filepath.Join(out, "retained"), kernel)
				} else {
					if err = build.CompileContext(ctx, r); err != nil {
						return err
					}
					if err = build.VerifyNativeExperimentControl(ctx, r); err != nil {
						return err
					}
					retained, e := build.RetainBinary(filepath.Join(out, "retained"), r.Binary)
					if e != nil {
						return e
					}
					candidate = &build.NativeExperiment{Worker: retained, BuildInput: r.BuildInput, ImplementationRevision: r.ImplementationRevisions[r.Target.Name], ResolvedGoTarget: r.Target.Resolved}
				}
				if err != nil {
					return err
				}
				if request.PauseBeforeRecapture {
					return pause("before_final_recapture")
				}
				return nil
			})
		}()
		cancel()
		buildErr = errors.Join(buildErr, pauseError)
		elapsed := time.Since(started)
		record := map[string]any{"Mode": *mode, "Label": request.Label, "PID": os.Getpid(), "Generation": generation, "OK": buildErr == nil, "ElapsedMS": float64(elapsed) / float64(time.Millisecond), "StartedAt": started, "CapturedAt": capturedAt, "Steps": steps}
		if !capturedAt.IsZero() {
			record["CapturedEditToArtifactMS"] = float64(time.Since(capturedAt)) / float64(time.Millisecond)
		}
		if captured.files != nil {
			// Match the watch loop: failed captured edits remain the comparison baseline,
			// while later edits stay observable and retry generated content after failure.
			baseline = captured
			if buildErr == nil {
				buildErr = acceptGeneratedSnapshot(root, &baseline)
			} else {
				baseline.retryGenerated = true
			}
			current, e := scanWatchedFilesReusing(root, baseline)
			if e == nil {
				record["PendingPaths"] = changedPaths(baseline, current)
			}
			record["CapturedFingerprint"] = snapshotFingerprint(captured)
		}
		if buildErr != nil {
			record["OK"] = false
			record["Error"] = buildErr.Error()
		} else {
			kernel = candidate.Kernel
			record["Evidence"], record["Workspace"], record["ContractRevision"] = candidate, prepared.result.Dir, prepared.result.Contract.Manifest.ContractRevision
			record["GraphCacheHit"] = prepared.cached != nil
			data, e := json.MarshalIndent(record, "", "  ")
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(out, "build-evidence.json"), append(data, '\n'), 0600); e != nil {
				return e
			}
		}
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
}
