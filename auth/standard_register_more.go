package auth

import (
	"context"
	"net/http"

	"github.com/pbrazdil/onlava/runtime"
)

func registerStandardOrganizations() {
	registerStandardTyped("auth", "ListOrganizations", runtime.Auth, "/auth/organizations", []string{http.MethodGet}, nil, (*ListOrganizationsResponse)(nil), func(ctx context.Context, svc *Service, _ []any, _ any) (any, error) {
		return svc.ListOrganizations(ctx)
	})
	registerStandardTyped("auth", "CreateOrganization", runtime.Auth, "/auth/organizations", []string{http.MethodPost}, (*CreateOrganizationParams)(nil), (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.CreateOrganization(ctx, payload.(*CreateOrganizationParams))
	})
	registerStandardTyped("auth", "SwitchOrganization", runtime.Auth, "/auth/organizations/switch", []string{http.MethodPost}, (*SwitchOrganizationParams)(nil), (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.SwitchOrganization(ctx, payload.(*SwitchOrganizationParams))
	})
	registerStandardTyped("auth", "UpdateOrganization", runtime.Auth, "/auth/organizations/:tenantID", []string{http.MethodPatch}, (*UpdateOrganizationParams)(nil), (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, payload any) (any, error) {
		return svc.UpdateOrganization(ctx, pathArgs[0].(string), payload.(*UpdateOrganizationParams))
	})
	registerStandardTyped("auth", "DeleteOrganization", runtime.Auth, "/auth/organizations/:tenantID", []string{http.MethodDelete}, nil, (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, _ any) (any, error) {
		return svc.DeleteOrganization(ctx, pathArgs[0].(string))
	})
	registerStandardTyped("auth", "ListOrganizationMembers", runtime.Auth, "/auth/organizations/:tenantID/members", []string{http.MethodGet}, nil, (*ListOrganizationMembersResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, _ any) (any, error) {
		return svc.ListOrganizationMembers(ctx, pathArgs[0].(string))
	})
	registerStandardTyped("auth", "InviteOrganizationMember", runtime.Auth, "/auth/organizations/:tenantID/invites", []string{http.MethodPost}, (*InviteMemberParams)(nil), (*InviteMemberResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, payload any) (any, error) {
		return svc.InviteOrganizationMember(ctx, pathArgs[0].(string), payload.(*InviteMemberParams))
	})
	registerStandardTyped("auth", "AcceptInvite", runtime.Auth, "/auth/invites/accept", []string{http.MethodPost}, (*AcceptInviteParams)(nil), (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.AcceptInvite(ctx, payload.(*AcceptInviteParams))
	})
	registerStandardTyped("auth", "UpdateOrganizationMemberRole", runtime.Auth, "/auth/organizations/:tenantID/members/:userID", []string{http.MethodPatch}, (*UpdateMemberRoleParams)(nil), (*ListOrganizationMembersResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, payload any) (any, error) {
		return svc.UpdateOrganizationMemberRole(ctx, pathArgs[0].(string), pathArgs[1].(string), payload.(*UpdateMemberRoleParams))
	})
	registerStandardTyped("auth", "DisableOrganizationMember", runtime.Auth, "/auth/organizations/:tenantID/members/disable", []string{http.MethodPost}, (*DisableMemberParams)(nil), (*ListOrganizationMembersResponse)(nil), func(ctx context.Context, svc *Service, pathArgs []any, payload any) (any, error) {
		return svc.DisableOrganizationMember(ctx, pathArgs[0].(string), payload.(*DisableMemberParams))
	})
}

func registerStandardImpersonation() {
	registerStandardTyped("auth", "StartImpersonation", runtime.Auth, "/auth/impersonation/start", []string{http.MethodPost}, (*StartImpersonationParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.StartImpersonation(ctx, payload.(*StartImpersonationParams))
	})
	registerStandardTyped("auth", "StopImpersonation", runtime.Auth, "/auth/impersonation/stop", []string{http.MethodPost}, (*RefreshParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.StopImpersonation(ctx, payload.(*RefreshParams))
	})
}
