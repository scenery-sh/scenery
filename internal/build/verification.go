package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"time"

	"scenery.sh/internal/compiler"
)

// preparedVerification marks a result whose implementation check is still
// owed; passed is the verification key of a check that passed without a
// diagnostic, written by the check and read after it has joined.
type preparedVerification struct{ passed string }

func cloneVerificationContract(contract *compiler.Result) *compiler.Result {
	checked := *contract
	manifest := *contract.Manifest
	checked.Manifest = &manifest
	checked.Diagnostics = slices.Clone(contract.Diagnostics)
	manifest.Diagnostics = slices.Clone(manifest.Diagnostics)
	return &checked
}

func preparedChecker(result *Result) (func(context.Context) (*compiler.Result, error), error) {
	if result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil || result.verification == nil {
		return nil, fmt.Errorf("prepared implementation verification is incomplete")
	}
	checked := cloneVerificationContract(result.Contract)
	workspace, target := result.Dir, *result.Target
	check := generateHooks.ApplyPreparedImplementationCheck
	key := implementationVerificationKey(result)
	if verified := generateHooks.ApplyVerifiedImplementationCheck; verified != nil && key != "" && key == result.verifiedImplementation {
		return func(ctx context.Context) (*compiler.Result, error) {
			started := time.Now()
			err := verified(ctx, checked)
			if err == nil {
				err = preparedContractError(checked)
			}
			RecordStep(ctx, Step{Name: "implementation.check", StartedAt: started, Duration: time.Since(started), Cache: "hit", Reason: "verified_inputs_unchanged", OK: err == nil})
			return checked, err
		}, nil
	}
	// The proof belongs to the inputs it names; a check of other inputs must
	// pass before those are recorded.
	result.verifiedImplementation = ""
	verification := result.verification
	before := len(checked.Diagnostics) + len(checked.Manifest.Diagnostics)
	return func(ctx context.Context) (*compiler.Result, error) {
		err := observeBuildAction(ctx, "implementation.check", func() error {
			if err := check(ctx, checked, workspace, target); err != nil {
				return err
			}
			return preparedContractError(checked)
		})
		if err == nil && key != "" && len(checked.Diagnostics)+len(checked.Manifest.Diagnostics) == before {
			verification.passed = key
		}
		return checked, err
	}, nil
}

// implementationVerificationKey names everything the Go analysis of the
// implementation check consumes: the workspace's source, generated and module
// files with the build flags, the dependency and framework identities, the
// generator, the contract, the resolved target and the Go toolchain's own
// environment. It is empty while any of those is unknown, which disables
// reuse.
func implementationVerificationKey(result *Result) string {
	if result == nil || result.Contract == nil || result.Contract.Manifest == nil || result.Target == nil ||
		result.BuildFingerprint == "" || result.DependencyFingerprint == "" || result.FrameworkFingerprint == "" || result.NeedsTidy {
		return ""
	}
	encoded, err := json.Marshal(struct {
		Version, Build, Dependencies, Framework, Generator, Graph, Contract, Toolchain string
		Target                                                                         compiler.GoBuildTarget
		Environment                                                                    []string
		Owned                                                                          []OwnedGoModuleSource
	}{
		"1", result.BuildFingerprint, result.DependencyFingerprint, result.FrameworkFingerprint,
		result.GeneratorFingerprint, result.GraphFingerprint, result.Contract.Manifest.ContractRevision, runtime.Version(),
		*result.Target, goToolIdentity(result.GoEnvironment), result.OwnedGoModuleSources,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// recordVerifiedImplementation keeps the proof of a check that just passed
// without a diagnostic, provided the build still has the inputs it checked.
// The caller has joined the check and saved the workspace state.
func recordVerifiedImplementation(result *Result, verification *preparedVerification) {
	if verification == nil || verification.passed == "" || verification.passed != implementationVerificationKey(result) {
		return
	}
	result.verifiedImplementation = verification.passed
	state, err := loadBuildState(result.Dir)
	if err != nil || state.Version != buildStateVersion || state.BuildFingerprint != result.BuildFingerprint {
		return
	}
	state.VerifiedImplementation = verification.passed
	_ = saveBuildState(result.Dir, state)
}

func completePreparedVerification(ctx context.Context, result *Result) error {
	if result.verification == nil {
		return nil
	}
	check, err := preparedChecker(result)
	if err != nil {
		return err
	}
	verification := result.verification
	checked, err := check(ctx)
	if err != nil {
		return err
	}
	result.Contract, result.verification = checked, nil
	if verification.passed != "" && verification.passed == implementationVerificationKey(result) {
		result.verifiedImplementation = verification.passed
	}
	return nil
}

// The caller holds the workspace lock and has completed every write-capable
// preparation step. Both branches consume those same bytes; neither publishes
// success. A build error still waits for authoritative native diagnostics;
// verification failure and caller cancellation stop the build branch. Both
// branches are joined before returning in every case.
func compileWithPreparedVerification(ctx context.Context, result *Result, compile func(context.Context) error) error {
	join, err := compileBesidePreparedVerification(ctx, result, compile)
	if err != nil {
		return err
	}
	return join()
}

// compileBesidePreparedVerification compiles beside the implementation check
// and returns the join that installs the checked contract. The caller must
// call it before using result.Contract, and may prepare the compiled artifacts
// meanwhile.
func compileBesidePreparedVerification(ctx context.Context, result *Result, compile func(context.Context) error) (func() error, error) {
	if result.verification == nil {
		return func() error { return nil }, compile(ctx)
	}
	check, err := preparedChecker(result)
	if err != nil {
		return nil, err
	}
	verification := result.verification
	ctx, cancel := context.WithCancel(ctx)
	var checked *compiler.Result
	var checkErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		checked, checkErr = check(ctx)
		if checkErr != nil {
			cancel()
		}
	}()
	compileErr := compile(ctx)
	if errors.Is(compileErr, context.Canceled) || errors.Is(compileErr, context.DeadlineExceeded) {
		cancel()
	}
	join := func() error {
		defer cancel()
		<-done
		checkCanceled := errors.Is(checkErr, context.Canceled) || errors.Is(checkErr, context.DeadlineExceeded)
		if checkErr != nil && (compileErr == nil || !checkCanceled) {
			return checkErr
		}
		if compileErr != nil {
			return compileErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result.Contract = checked
		if verification.passed != "" && verification.passed == implementationVerificationKey(result) {
			result.verifiedImplementation = verification.passed
		}
		return nil
	}
	if compileErr != nil {
		// A failed compile owns the outcome; join reports it after the check.
		return nil, join()
	}
	return join, nil
}
