package auth

import (
	"context"
	"strings"

	"scenery.sh/internal/authbridge"
	"scenery.sh/runtime"
)

type UID string

type authDataContextKey struct{}

func init() {
	authbridge.Register(authbridge.Provider{
		UserID: func() (string, bool) {
			uid, ok := UserID()
			return string(uid), ok
		},
		Data: func() any {
			return Data()
		},
		CurrentData: func() (any, bool) {
			data, ok := CurrentAuthData()
			return data, ok
		},
		TenantID: tenantIDFromAuthData,
	})
}

func UserID() (UID, bool) {
	info := runtime.CurrentAuth()
	if info == nil || info.UID == "" {
		return "", false
	}
	return UID(info.UID), true
}

func Data() any {
	info := runtime.CurrentAuth()
	if info == nil {
		return nil
	}
	return info.Data
}

func CurrentAuthData() (*AuthData, bool) {
	data, ok := Data().(*AuthData)
	return data, ok && data != nil
}

// CurrentAuditIdentity returns the authenticated request's complete,
// impersonation-aware audit identity.
func CurrentAuditIdentity(ctx context.Context) (AuditIdentity, error) {
	data, ok := currentAuthDataFromContext(ctx)
	if !ok {
		return AuditIdentity{}, unauthenticated("authentication required")
	}
	return data.AuditIdentity(), nil
}

func WithContext(ctx context.Context, uid UID, data any) context.Context {
	ctx = runtime.WithAuthContext(ctx, runtime.AuthInfo{
		UID:  string(uid),
		Data: data,
	})
	if authData, ok := data.(*AuthData); ok && authData != nil {
		ctx = context.WithValue(ctx, authDataContextKey{}, authData)
	}
	return ctx
}

func currentAuthDataFromContext(ctx context.Context) (*AuthData, bool) {
	if data, ok := CurrentAuthData(); ok {
		return data, true
	}
	if ctx == nil {
		return nil, false
	}
	data, ok := ctx.Value(authDataContextKey{}).(*AuthData)
	return data, ok && data != nil
}

func tenantIDFromAuthData(data any) (string, bool) {
	switch data := data.(type) {
	case *AuthData:
		if data == nil {
			return "", false
		}
		tenantKey := strings.TrimSpace(string(data.TenantID))
		return tenantKey, tenantKey != ""
	case AuthData:
		tenantKey := strings.TrimSpace(string(data.TenantID))
		return tenantKey, tenantKey != ""
	default:
		return "", false
	}
}
