package service

import (
	"context"

	onlavaauth "github.com/pbrazdil/onlava/auth"
	"github.com/pbrazdil/onlava/errs"
)

type MeResponse struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
}

//onlava:api auth method=GET path=/whoami
func Whoami(ctx context.Context) (*MeResponse, error) {
	data, ok := onlavaauth.CurrentAuthData()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("missing auth").Err()
	}
	return &MeResponse{
		UserID:   string(data.UserID),
		TenantID: string(data.TenantID),
	}, nil
}
