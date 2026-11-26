package runtime

import (
	"context"
	"testing"

	"scenery.sh/errs"
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
	if _, err := (sdkHost{}).ConnectionToken(context.Background(), "owner", "files", nil); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("unadmitted background call: %v", err)
	}
}
