package auth

import (
	"context"
	"net/http"

	"scenery.sh/runtime"
)

func registerStandardOrganizations() {
	registerStandardEmpty("auth", "ListOrganizations", runtime.Auth, "/auth/organizations", http.MethodGet, func(ctx context.Context, svc *Service, _ []string) (*ListOrganizationsResponse, error) {
		return svc.ListOrganizations(ctx)
	})
	registerStandardJSON("auth", "CreateOrganization", runtime.Auth, "/auth/organizations", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *CreateOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.CreateOrganization(ctx, input)
	})
	registerStandardJSON("auth", "SwitchOrganization", runtime.Auth, "/auth/organizations/switch", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *SwitchOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.SwitchOrganization(ctx, input)
	})
	registerStandardJSON("auth", "UpdateOrganization", runtime.Auth, "/auth/organizations/:tenantID", http.MethodPatch, func(ctx context.Context, svc *Service, path []string, input *UpdateOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.UpdateOrganization(ctx, path[0], input)
	})
	registerStandardEmpty("auth", "DeleteOrganization", runtime.Auth, "/auth/organizations/:tenantID", http.MethodDelete, func(ctx context.Context, svc *Service, path []string) (*AuthBootstrapResponse, error) {
		return svc.DeleteOrganization(ctx, path[0])
	})
	registerStandardEmpty("auth", "ListOrganizationMembers", runtime.Auth, "/auth/organizations/:tenantID/members", http.MethodGet, func(ctx context.Context, svc *Service, path []string) (*ListOrganizationMembersResponse, error) {
		return svc.ListOrganizationMembers(ctx, path[0])
	})
	registerStandardJSON("auth", "InviteOrganizationMember", runtime.Auth, "/auth/organizations/:tenantID/invites", http.MethodPost, func(ctx context.Context, svc *Service, path []string, input *InviteMemberParams) (*InviteMemberResponse, error) {
		return svc.InviteOrganizationMember(ctx, path[0], input)
	})
	registerStandardJSON("auth", "AcceptInvite", runtime.Auth, "/auth/invites/accept", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *AcceptInviteParams) (*AuthBootstrapResponse, error) {
		return svc.AcceptInvite(ctx, input)
	})
	registerStandardJSON("auth", "UpdateOrganizationMemberRole", runtime.Auth, "/auth/organizations/:tenantID/members/:userID", http.MethodPatch, func(ctx context.Context, svc *Service, path []string, input *UpdateMemberRoleParams) (*ListOrganizationMembersResponse, error) {
		return svc.UpdateOrganizationMemberRole(ctx, path[0], path[1], input)
	})
	registerStandardJSON("auth", "DisableOrganizationMember", runtime.Auth, "/auth/organizations/:tenantID/members/disable", http.MethodPost, func(ctx context.Context, svc *Service, path []string, input *DisableMemberParams) (*ListOrganizationMembersResponse, error) {
		return svc.DisableOrganizationMember(ctx, path[0], input)
	})
}

func registerStandardImpersonation() {
	registerStandardJSON("auth", "StartImpersonation", runtime.Auth, "/auth/impersonation/start", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *StartImpersonationParams) (*AuthSessionResponse, error) {
		return svc.StartImpersonation(ctx, input)
	})
	registerStandardCookie("auth", "StopImpersonation", runtime.Auth, "/auth/impersonation/stop", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *RefreshParams) (*AuthSessionResponse, error) {
		return svc.StopImpersonation(ctx, input)
	})
}
