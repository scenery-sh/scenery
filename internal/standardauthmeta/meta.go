package standardauthmeta

import (
	"net/http"

	"github.com/pbrazdil/onlava/internal/runtimeapi"
)

type Endpoint struct {
	Service    string
	Name       string
	Access     runtimeapi.Access
	Raw        bool
	Path       string
	Methods    []string
	HasPayload bool
}

func Endpoints() []Endpoint {
	return []Endpoint{
		{"auth", "AcceptInvite", runtimeapi.Auth, false, "/auth/invites/accept", []string{http.MethodPost}, true},
		{"auth", "ConfirmEmailVerification", runtimeapi.Public, false, "/auth/email-verification/confirm", []string{http.MethodPost}, true},
		{"auth", "ConfirmPasswordReset", runtimeapi.Public, false, "/auth/password-reset/confirm", []string{http.MethodPost}, true},
		{"auth", "CreateOrganization", runtimeapi.Auth, false, "/auth/organizations", []string{http.MethodPost}, true},
		{"auth", "DeleteOrganization", runtimeapi.Auth, false, "/auth/organizations/:tenantID", []string{http.MethodDelete}, false},
		{"auth", "DisableOrganizationMember", runtimeapi.Auth, false, "/auth/organizations/:tenantID/members/disable", []string{http.MethodPost}, true},
		{"auth", "GoogleCallback", runtimeapi.Public, true, "/auth/google/callback", []string{http.MethodGet}, false},
		{"auth", "GoogleStart", runtimeapi.Public, true, "/auth/google/start", []string{http.MethodGet}, false},
		{"auth", "InviteOrganizationMember", runtimeapi.Auth, false, "/auth/organizations/:tenantID/invites", []string{http.MethodPost}, true},
		{"auth", "ListOrganizationMembers", runtimeapi.Auth, false, "/auth/organizations/:tenantID/members", []string{http.MethodGet}, false},
		{"auth", "ListOrganizations", runtimeapi.Auth, false, "/auth/organizations", []string{http.MethodGet}, false},
		{"auth", "LoginEmail", runtimeapi.Public, false, "/auth/login/email", []string{http.MethodPost}, true},
		{"auth", "Logout", runtimeapi.Public, false, "/auth/logout", []string{http.MethodPost}, true},
		{"auth", "Me", runtimeapi.Auth, false, "/auth/me", []string{http.MethodGet}, false},
		{"auth", "Refresh", runtimeapi.Public, false, "/auth/refresh", []string{http.MethodPost}, true},
		{"auth", "RequestPasswordReset", runtimeapi.Public, false, "/auth/password-reset/request", []string{http.MethodPost}, true},
		{"auth", "ResendEmailVerification", runtimeapi.Public, false, "/auth/email-verification/resend", []string{http.MethodPost}, true},
		{"auth", "SignupEmail", runtimeapi.Public, false, "/auth/signup/email", []string{http.MethodPost}, true},
		{"auth", "StartImpersonation", runtimeapi.Auth, false, "/auth/impersonation/start", []string{http.MethodPost}, true},
		{"auth", "StopImpersonation", runtimeapi.Auth, false, "/auth/impersonation/stop", []string{http.MethodPost}, true},
		{"auth", "SwitchOrganization", runtimeapi.Auth, false, "/auth/organizations/switch", []string{http.MethodPost}, true},
		{"auth", "UpdateOrganization", runtimeapi.Auth, false, "/auth/organizations/:tenantID", []string{http.MethodPatch}, true},
		{"auth", "UpdateOrganizationMemberRole", runtimeapi.Auth, false, "/auth/organizations/:tenantID/members/:userID", []string{http.MethodPatch}, true},
		{"users", "DevBootstrap", runtimeapi.Public, false, "/users/dev-bootstrap", []string{http.MethodPost}, true},
	}
}
