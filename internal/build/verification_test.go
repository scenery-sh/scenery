package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"scenery.sh/internal/compiler"
)

func TestCompilePublicationWaitsForVerification(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		name := "success"
		if rejected {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				appRoot := t.TempDir()
				writeBuildTestFile(t, appRoot, ".scenery.json", `{"name":"verification"}`)
				workspace, err := workspaceDir(appRoot, "verification")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(workspace) })
				writeBuildTestFile(t, workspace, "go.mod", "module example.test/verification\n")
				stale := filepath.Join(workspace, "scenery-app-1111111111111111")
				writeBuildTestFile(t, workspace, filepath.Base(stale), "previous")
				result := prepareCompileTestResult(&Result{AppRoot: appRoot, AppName: "verification", Dir: workspace, Binary: filepath.Join(workspace, "scenery-app-2222222222222222"), BuildFingerprint: "candidate"})
				result.Contract = &compiler.Result{Root: appRoot, ContractStatus: "valid", Manifest: &compiler.Manifest{}}
				result.Target.Name = "development"
				result.BuildInput = &BuildInputManifest{Digest: "test-build-input"}
				result.verification = &preparedVerification{}
				result.BuildFingerprint, err = workspaceBuildFingerprint(workspace, nil)
				if err != nil {
					t.Fatal(err)
				}
				original := result.Contract
				entered, release, built := make(chan struct{}), make(chan struct{}), make(chan struct{})
				oldCheck, oldGo := generateHooks.ApplyPreparedImplementationCheck, runGo
				t.Cleanup(func() { generateHooks.ApplyPreparedImplementationCheck, runGo = oldCheck, oldGo })
				generateHooks.ApplyPreparedImplementationCheck = func(_ context.Context, checked *compiler.Result, root string, _ []string, _ compiler.GoBuildTarget) error {
					if checked == original || checked.Manifest == original.Manifest || root != workspace {
						return errors.New("verification did not own an independent diagnostic snapshot")
					}
					close(entered)
					<-release
					checked.ImplementationStatus = "valid"
					if rejected {
						checked.Diagnostics = append(checked.Diagnostics, compiler.Diagnostic{Code: "SCN6202", Severity: "error", Message: "rejected implementation"})
					}
					return nil
				}
				runGo = func(_ context.Context, _ string, _ []string, args ...string) error {
					out, ok := fakeGoBuildOutput(args)
					if !ok {
						return errors.New("unexpected Go command")
					}
					err := os.WriteFile(out, []byte("private candidate"), 0o755)
					close(built)
					return err
				}
				done := make(chan error, 1)
				go func() { done <- CompileContext(context.Background(), result) }()
				<-entered
				<-built
				synctest.Wait()
				for _, path := range []string{filepath.Join(workspace, buildStateFile), RuntimeBundlePath(appRoot, result.Target.Name)} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Errorf("success evidence published before verification: %s: %v", path, err)
					}
				}
				if _, err := os.Stat(stale); err != nil {
					t.Errorf("previous binary pruned before verification: %v", err)
				}
				close(release)
				err = <-done
				if original.ImplementationStatus != "" || len(original.Diagnostics) != 0 {
					t.Fatal("checker mutated the shared graph")
				}
				if rejected {
					var diagnostic *ContractError
					if !errors.As(err, &diagnostic) || result.Contract != original {
						t.Fatalf("verification failure lost ownership/diagnostic: %v", err)
					}
					if _, err := os.Stat(result.Binary); !os.IsNotExist(err) {
						t.Fatal("failed candidate remained reusable")
					}
					if _, err := os.Stat(stale); err != nil {
						t.Fatal("verification failure pruned a previous binary")
					}
					for _, path := range []string{filepath.Join(workspace, buildStateFile), RuntimeBundlePath(appRoot, result.Target.Name)} {
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Fatalf("verification failure published success evidence: %s", path)
						}
					}
				} else {
					if err != nil || result.Contract == original || result.verification != nil {
						t.Fatalf("successful join was not published: %v", err)
					}
					manifest, exists, err := ReadLatestBuildManifest(appRoot)
					if err != nil || !exists || manifest.Build.Phase != "compiled" {
						t.Fatalf("compiled manifest missing after join: %#v %v", manifest, err)
					}
					if _, err := os.Stat(stale); !os.IsNotExist(err) {
						t.Fatal("successful publication did not prune stale private output")
					}
				}
			})
		})
	}
}

func TestPreparedCompilationCancelsAndJoinsBothBranches(t *testing.T) {
	for _, failedBranch := range []string{"check", "build", "parent"} {
		t.Run(failedBranch, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				want := errors.New(failedBranch + " failed")
				entered, exited, finishCheck := make(chan struct{}), make(chan struct{}), make(chan struct{})
				old := generateHooks.ApplyPreparedImplementationCheck
				t.Cleanup(func() { generateHooks.ApplyPreparedImplementationCheck = old })
				generateHooks.ApplyPreparedImplementationCheck = func(ctx context.Context, _ *compiler.Result, _ string, _ []string, _ compiler.GoBuildTarget) error {
					defer close(exited)
					close(entered)
					if failedBranch == "check" {
						return want
					}
					if failedBranch == "build" {
						<-finishCheck
						return nil
					}
					<-ctx.Done()
					return ctx.Err()
				}
				result := &Result{Contract: &compiler.Result{ContractStatus: "valid", Manifest: &compiler.Manifest{}}, Target: &compiler.GoBuildTarget{}, verification: &preparedVerification{}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				err := compileWithPreparedVerification(ctx, result, func(ctx context.Context) error {
					<-entered
					if failedBranch == "build" {
						close(finishCheck)
						return want
					}
					if failedBranch == "parent" {
						cancel()
					}
					<-ctx.Done()
					return ctx.Err()
				})
				if failedBranch == "parent" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("failure = %v, want %v", err, want)
				}
				select {
				case <-exited:
				default:
					t.Fatal("checker outlived the failed build operation")
				}
				if result.verification == nil {
					t.Fatal("failure recorded verification success")
				}
			})
		})
	}
}
