package auth

import (
	"context"

	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/connectionapi"
)

type ConnectionProvider = connectionapi.ProviderConfig
type ConnectionStartParams = connectionapi.StartParams
type ConnectionStartResponse = connectionapi.StartResponse
type ConnectionResponse = connectionapi.Status

// ConnectionToken returns a bearer token for the authenticated standard-auth
// user. Required scopes are permissions, not a request to downscope the token.
func ConnectionToken(ctx context.Context, provider string, scopes ...string) (string, error) {
	data, ok := currentAuthDataFromContext(ctx)
	if !ok {
		return "", unauthenticated("connection token requires auth")
	}
	return ConnectionTokenForUser(ctx, string(data.UserID), provider, scopes...)
}

// ConnectionTokenForUser is for trusted server workers. In a request it can
// access only the authenticated owner's connection; there is no token HTTP API.
// Applications authorize tenant-scoped work before using a user's connection.
// Keep the runtime-admitted context when starting a helper goroutine; a bare
// background context grants no authority to select a connection owner.
func ConnectionTokenForUser(ctx context.Context, user, provider string, scopes ...string) (string, error) {
	owner, err := parseUUID(user)
	if err != nil {
		return "", unauthenticated("invalid connection owner")
	}
	if data, ok := currentAuthDataFromContext(ctx); ok {
		principal, err := parseUUID(string(data.UserID))
		if err != nil || principal.Bytes != owner.Bytes {
			return "", permissionDenied("another user's connection is not available in this request")
		}
	}
	user = uuidString(owner)
	host := appsdk.CurrentHost()
	if host == nil {
		return "", errs.B().Code(errs.ConnectionNotConfigured).Msg("connection runtime is unavailable").Err()
	}
	return host.ConnectionToken(ctx, user, provider, scopes)
}
