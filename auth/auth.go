package auth

import (
	"context"
	"strings"

	"github.com/pbrazdil/onlava/internal/authbridge"
	"github.com/pbrazdil/onlava/runtime"
)

type UID string

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

func WithContext(ctx context.Context, uid UID, data any) context.Context {
	return runtime.WithAuthContext(ctx, runtime.AuthInfo{
		UID:  string(uid),
		Data: data,
	})
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
