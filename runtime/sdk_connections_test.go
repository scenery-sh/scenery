package runtime

import (
	"context"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/runtime/shared"
)

func TestConnectionWorkerHelperCannotEraseExternalRequestAuthority(t *testing.T) {
	for _, principal := range []string{"", "other-owner", "owner"} {
		state := &requestState{request: shared.Request{Type: shared.APICall}, auth: AuthInfo{UID: principal}}
		ctx := withNewRuntimeInvocation(withState(t.Context(), state), state)
		for _, scenario := range []struct {
			name      string
			ctx       context.Context
			goroutine bool
		}{
			{"same goroutine replaced context", context.Background(), false},
			{"new goroutine replaced context", context.Background(), true},
			{"new goroutine inherited context", ctx, true},
			{"new goroutine forged auth", WithAuthContext(context.Background(), AuthInfo{UID: "owner"}), true},
		} {
			t.Run(principal+"/"+scenario.name, func(t *testing.T) {
				want := errs.PermissionDenied
				if principal == "owner" && (scenario.name == "same goroutine replaced context" || scenario.name == "new goroutine inherited context") {
					want = errs.ConnectionNotConfigured // passed admission, without a configured vault
				}
				var err error
				if scenario.goroutine {
					result := make(chan error, 1)
					go func() {
						_, callErr := (sdkHost{}).ConnectionToken(scenario.ctx, "owner", "files", nil)
						result <- callErr
					}()
					err = <-result
				} else {
					restore := enterState(state)
					_, err = (sdkHost{}).ConnectionToken(scenario.ctx, "owner", "files", nil)
					restore()
				}
				if errs.Code(err) != want {
					t.Fatalf("admission: %v, want %s", err, want)
				}
			})
		}
	}
	workerCtx, finish := enterDurableInvocation(t.Context(), "worker", "task", "execution", time.Minute, nil, 0)
	result := make(chan error, 1)
	go func() { _, err := (sdkHost{}).ConnectionToken(workerCtx, "owner", "files", nil); result <- err }()
	if err := <-result; errs.Code(err) != errs.ConnectionNotConfigured {
		t.Fatalf("admitted worker: %v", err)
	}
	finish(nil)
	if _, err := (sdkHost{}).ConnectionToken(context.Background(), "owner", "files", nil); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("unadmitted background call: %v", err)
	}
}

func TestForwardedConnectionAuthorityUsesAdmittedPrincipal(t *testing.T) {
	for _, worker := range []bool{false, true} {
		principal := "other-owner"
		if worker {
			principal = ""
		}
		ctx, restore, err := enterProcessLinkedCall(t.Context(), runtimeapi.InvocationMetadata{ID: "forwarded", Principal: principal, ConnectionWorker: worker}, &processLinkCallState{
			Auth: processLinkAuth{UID: "owner"}, Request: processLinkRequestState{Type: shared.InternalCall},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (sdkHost{}).ConnectionToken(ctx, "owner", "files", nil)
		restore()
		want := errs.PermissionDenied
		if worker {
			want = errs.ConnectionNotConfigured
		}
		if errs.Code(err) != want {
			t.Fatalf("forwarded authority: %v, want %s", err, want)
		}
	}
}
