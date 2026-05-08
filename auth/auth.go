package auth

import (
	"context"

	"github.com/pbrazdil/onlava/runtime"
)

type UID string

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
