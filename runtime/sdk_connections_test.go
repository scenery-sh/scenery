package runtime

import (
	"context"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

func TestConnectionWorkerHelperCannotEraseExternalRequestAuthority(t *testing.T) {
	for _, principal := range []string{"", "other-owner"} {
		state := &requestState{request: shared.Request{Type: shared.APICall}, auth: AuthInfo{UID: principal}}
		restore := enterState(state)
		_, err := (sdkHost{}).ConnectionToken(context.Background(), "owner", "files", nil)
		restore()
		if errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("external principal %q: %v", principal, err)
		}
		_, err = (sdkHost{}).ConnectionToken(withState(t.Context(), state), "owner", "files", nil)
		if errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("context principal %q: %v", principal, err)
		}
	}
}
