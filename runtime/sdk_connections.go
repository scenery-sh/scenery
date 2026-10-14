package runtime

import (
	"context"
	"database/sql"
	"sync"

	"scenery.sh/errs"
	"scenery.sh/internal/connectionapi"
	"scenery.sh/internal/connectionruntime"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime/shared"
)

var sdkConnections struct {
	sync.RWMutex
	service *connectionruntime.Service
}

func (sdkHost) RegisterConnections(providers []connectionapi.ProviderConfig, open func(context.Context) (*sql.DB, error), principal func(context.Context) (string, string, error)) error {
	service, err := connectionruntime.New(connectionruntime.Options{Host: sdkHost{}, Providers: providers, Open: open, Principal: principal, ApplicationURL: func() string { return envpolicy.Get("SCENERY_PUBLIC_APP_URL") }})
	if err != nil {
		return err
	}
	sdkConnections.Lock()
	sdkConnections.service = service
	sdkConnections.Unlock()
	service.Register()
	return nil
}

func (sdkHost) ConnectionToken(ctx context.Context, user, provider string, scopes []string) (string, error) {
	// Keep the goroutine's external request boundary even if application code
	// replaces the argument context before forwarding it to the worker helper.
	state := currentState()
	if state == nil {
		state = stateFromContext(ctx)
	}
	if state != nil && (state.auth.UID != "" && state.auth.UID != user || (state.request.Type == shared.APICall || state.request.Type == shared.RawAPICall) && state.auth.UID == "") {
		return "", errs.B().Code(errs.PermissionDenied).Msg("connection owner does not match the request").Err()
	}
	sdkConnections.RLock()
	service := sdkConnections.service
	sdkConnections.RUnlock()
	if service == nil {
		return "", errs.B().Code(errs.ConnectionNotConfigured).Msg("connection runtime is not configured").Err()
	}
	return service.Token(ctx, user, provider, scopes)
}
