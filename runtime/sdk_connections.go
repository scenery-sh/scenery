package runtime

import (
	"context"

	"scenery.sh/errs"
	"scenery.sh/internal/runtimeapi"
)

// ConnectionToken asks the host for the connection token of the request's
// principal. Only runtime admission can grant owner selection: losing both the
// calling goroutine and its context loses authority.
func (sdkHost) ConnectionToken(ctx context.Context, user, provider string, scopes []string) (string, error) {
	invocation, _ := runtimeapi.InvocationFromContext(ctx)
	if state := currentState(); state != nil {
		invocation = state.invocation
	}
	if !invocation.Valid() || invocation.Principal() == "" || invocation.Principal() != user {
		return "", errs.B().Code(errs.PermissionDenied).Msg("connection owner does not match the request").Err()
	}
	if capabilitiesFromContext(ctx) == nil {
		return "", errs.B().Code(errs.ConnectionNotConfigured).Msg("connection runtime is not configured").Err()
	}
	return workerConnectionToken(ctx, provider, scopes)
}
