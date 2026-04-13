package auth

import (
	"context"

	pulseauth "pulse.dev/auth"
)

type UID = pulseauth.UID

func UserID() (UID, bool) {
	return pulseauth.UserID()
}

func Data() any {
	return pulseauth.Data()
}

func WithContext(ctx context.Context, uid UID, data any) context.Context {
	return pulseauth.WithContext(ctx, uid, data)
}
