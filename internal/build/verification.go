package build

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"scenery.sh/internal/compiler"
)

type preparedVerification struct {
	patterns []string
}

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
	patterns := slices.Clone(result.verification.patterns)
	check := generateHooks.ApplyPreparedImplementationCheck
	return func(ctx context.Context) (*compiler.Result, error) {
		err := observeBuildAction(ctx, "implementation.check", func() error {
			if err := check(ctx, checked, workspace, patterns, target); err != nil {
				return err
			}
			return preparedContractError(checked)
		})
		return checked, err
	}, nil
}

func completePreparedVerification(ctx context.Context, result *Result) error {
	if result.verification == nil {
		return nil
	}
	check, err := preparedChecker(result)
	if err != nil {
		return err
	}
	checked, err := check(ctx)
	if err != nil {
		return err
	}
	result.Contract, result.verification = checked, nil
	return nil
}

// The caller holds the workspace lock and has completed every write-capable
// preparation step. Both branches consume those same bytes; neither publishes
// success. A build error still waits for authoritative native diagnostics;
// verification failure and caller cancellation stop the build branch. Both
// branches are joined before returning in every case.
func compileWithPreparedVerification(ctx context.Context, result *Result, compile func(context.Context) error) error {
	if result.verification == nil {
		return compile(ctx)
	}
	check, err := preparedChecker(result)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	return nil
}
