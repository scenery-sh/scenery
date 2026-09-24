package auth

import (
	"context"
	"net/http"

	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/runtimeapi"
)

func registerStandardOrganizations(host appsdk.Host) {
	registerStandardEmpty(host, "auth", "ListOrganizations", runtimeapi.Auth, "/auth/organizations", http.MethodGet, func(ctx context.Context, svc *Service, _ []string) (*ListOrganizationsResponse, error) {
		return svc.ListOrganizations(ctx)
	})
	registerStandardJSON(host, "auth", "CreateOrganization", runtimeapi.Auth, "/auth/organizations", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *CreateOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.CreateOrganization(ctx, input)
	})
	registerStandardJSON(host, "auth", "SwitchOrganization", runtimeapi.Auth, "/auth/organizations/switch", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *SwitchOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.SwitchOrganization(ctx, input)
	})
	registerStandardJSON(host, "auth", "UpdateOrganization", runtimeapi.Auth, "/auth/organizations/:tenantID", http.MethodPatch, func(ctx context.Context, svc *Service, path []string, input *UpdateOrganizationParams) (*AuthBootstrapResponse, error) {
		return svc.UpdateOrganization(ctx, path[0], input)
	})
	registerStandardEmpty(host, "auth", "DeleteOrganization", runtimeapi.Auth, "/auth/organizations/:tenantID", http.MethodDelete, func(ctx context.Context, svc *Service, path []string) (*AuthBootstrapResponse, error) {
		return svc.DeleteOrganization(ctx, path[0])
	})
	registerStandardEmpty(host, "auth", "ListOrganizationMembers", runtimeapi.Auth, "/auth/organizations/:tenantID/members", http.MethodGet, func(ctx context.Context, svc *Service, path []string) (*ListOrganizationMembersResponse, error) {
		return svc.ListOrganizationMembers(ctx, path[0])
	})
	registerStandardJSON(host, "auth", "InviteOrganizationMember", runtimeapi.Auth, "/auth/organizations/:tenantID/invites", http.MethodPost, func(ctx context.Context, svc *Service, path []string, input *InviteMemberParams) (*InviteMemberResponse, error) {
		return svc.InviteOrganizationMember(ctx, path[0], input)
	})
	registerStandardJSON(host, "auth", "AcceptInvite", runtimeapi.Auth, "/auth/invites/accept", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *AcceptInviteParams) (*AuthBootstrapResponse, error) {
		return svc.AcceptInvite(ctx, input)
	})
	registerStandardJSON(host, "auth", "UpdateOrganizationMemberRole", runtimeapi.Auth, "/auth/organizations/:tenantID/members/:userID", http.MethodPatch, func(ctx context.Context, svc *Service, path []string, input *UpdateMemberRoleParams) (*ListOrganizationMembersResponse, error) {
		return svc.UpdateOrganizationMemberRole(ctx, path[0], path[1], input)
	})
	registerStandardJSON(host, "auth", "DisableOrganizationMember", runtimeapi.Auth, "/auth/organizations/:tenantID/members/disable", http.MethodPost, func(ctx context.Context, svc *Service, path []string, input *DisableMemberParams) (*ListOrganizationMembersResponse, error) {
		return svc.DisableOrganizationMember(ctx, path[0], input)
	})
}

func registerStandardImpersonation(host appsdk.Host) {
	registerStandardJSON(host, "auth", "StartImpersonation", runtimeapi.Auth, "/auth/impersonation/start", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *StartImpersonationParams) (*AuthSessionResponse, error) {
		return svc.StartImpersonation(ctx, input)
	})
	registerStandardCookie(host, "auth", "StopImpersonation", runtimeapi.Auth, "/auth/impersonation/stop", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *RefreshParams) (*AuthSessionResponse, error) {
		return svc.StopImpersonation(ctx, input)
	})
}
