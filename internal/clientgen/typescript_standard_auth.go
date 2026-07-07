package clientgen

import (
	"bytes"
	"fmt"
	"strings"
)

type standardTSMethod struct {
	Namespace string
	Name      string
	Method    string
	Path      string
	Params    []string
	Payload   string
	Response  string
	Raw       bool
}

func writeStandardAuthNamespaces(buf *bytes.Buffer, includeGoogle bool) {
	writeStandardAuthNamespace(buf, "auth", standardAuthMethods(includeGoogle))
	writeStandardAuthNamespace(buf, "users", standardUsersMethods())
}

func appendMissing(values []string, extras ...string) []string {
	seen := make(map[string]bool, len(values)+len(extras))
	for _, value := range values {
		seen[value] = true
	}
	for _, extra := range extras {
		if !seen[extra] {
			values = append(values, extra)
		}
	}
	return values
}

func writeStandardAuthNamespace(buf *bytes.Buffer, name string, methods []standardTSMethod) {
	buf.WriteString(fmt.Sprintf("export namespace %s {\n", name))
	buf.WriteString(standardAuthTypes(name))
	buf.WriteString("    export class ServiceClient {\n")
	buf.WriteString("        private baseClient: BaseClient\n\n")
	buf.WriteString("        constructor(baseClient: BaseClient) {\n")
	buf.WriteString("            this.baseClient = baseClient\n")
	for _, method := range methods {
		buf.WriteString(fmt.Sprintf("            this.%s = this.%s.bind(this)\n", method.Name, method.Name))
		if !method.Raw {
			buf.WriteString(fmt.Sprintf("            this.%sWithMeta = this.%sWithMeta.bind(this)\n", method.Name, method.Name))
		}
	}
	buf.WriteString("        }\n\n")
	for i, method := range methods {
		buf.WriteString(indentBlock(renderStandardTSMethod(method, false), 2))
		if !method.Raw {
			buf.WriteString("\n\n")
			buf.WriteString(indentBlock(renderStandardTSMethod(method, true), 2))
		}
		if i != len(methods)-1 {
			buf.WriteString("\n\n")
		}
	}
	buf.WriteString("\n    }\n")
	buf.WriteString("}\n\n")
}

func renderStandardTSMethod(method standardTSMethod, withMeta bool) string {
	params := append([]string(nil), method.Params...)
	if method.Payload != "" {
		params = append(params, fmt.Sprintf("params: %s", method.Payload))
	}
	params = append(params, "options?: CallParameters")
	body := "undefined"
	if method.Payload != "" {
		body = "JSON.stringify(params)"
	}
	name := method.Name
	response := method.Response
	if response == "" {
		response = "void"
	}
	returnType := response
	if method.Raw {
		returnType = "globalThis.Response"
	}
	if withMeta {
		name += "WithMeta"
		returnType = fmt.Sprintf("APIResponse<%s>", response)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "public async %s(%s): Promise<%s> {\n", name, strings.Join(params, ", "), returnType)
	if method.Raw {
		fmt.Fprintf(&b, "    return await this.baseClient.callAPI(%q, %s, undefined, options)\n", method.Method, method.Path)
		b.WriteString("}")
		return b.String()
	}
	fmt.Fprintf(&b, "    const resp = await this.baseClient.callTypedAPI(%q, %s, %s, options)\n", method.Method, method.Path, body)
	if withMeta {
		if response == "void" {
			b.WriteString("    return typedVoidAPIResponse(resp)\n")
		} else {
			fmt.Fprintf(&b, "    return await decodeTypedAPIResponse({body: resp, headers: resp.headers, status: resp.status, response: resp}) as APIResponse<%s>\n", response)
		}
	} else if response != "void" {
		fmt.Fprintf(&b, "    return await decodeTypedResponse(resp) as %s\n", response)
	}
	b.WriteString("}")
	return b.String()
}

func standardAuthMethods(includeGoogle bool) []standardTSMethod {
	methods := []standardTSMethod{
		{Name: "AcceptInvite", Method: "POST", Path: "`/auth/invites/accept`", Payload: "AcceptInviteParams", Response: "AuthBootstrapResponse"},
		{Name: "ConfirmEmailVerification", Method: "POST", Path: "`/auth/email-verification/confirm`", Payload: "EmailVerificationConfirmParams", Response: "AuthSessionResponse"},
		{Name: "ConfirmPasswordReset", Method: "POST", Path: "`/auth/password-reset/confirm`", Payload: "PasswordResetConfirmParams", Response: "AuthSessionResponse"},
		{Name: "CreateOrganization", Method: "POST", Path: "`/auth/organizations`", Payload: "CreateOrganizationParams", Response: "AuthBootstrapResponse"},
		{Name: "DeleteOrganization", Method: "DELETE", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}`)", Params: []string{"tenantID: string"}, Response: "AuthBootstrapResponse"},
		{Name: "DisableOrganizationMember", Method: "POST", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}/members/disable`)", Params: []string{"tenantID: string"}, Payload: "DisableMemberParams", Response: "ListOrganizationMembersResponse"},
	}
	if includeGoogle {
		methods = append(methods,
			standardTSMethod{Name: "GoogleCallback", Method: "GET", Path: "`/auth/google/callback`", Raw: true},
			standardTSMethod{Name: "GoogleStart", Method: "GET", Path: "`/auth/google/start`", Raw: true},
		)
	}
	methods = append(methods, []standardTSMethod{
		{Name: "InviteOrganizationMember", Method: "POST", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}/invites`)", Params: []string{"tenantID: string"}, Payload: "InviteMemberParams", Response: "InviteMemberResponse"},
		{Name: "ListOrganizationMembers", Method: "GET", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}/members`)", Params: []string{"tenantID: string"}, Response: "ListOrganizationMembersResponse"},
		{Name: "ListOrganizations", Method: "GET", Path: "`/auth/organizations`", Response: "ListOrganizationsResponse"},
		{Name: "LoginEmail", Method: "POST", Path: "`/auth/login/email`", Payload: "EmailLoginParams", Response: "AuthSessionResponse"},
		{Name: "Logout", Method: "POST", Path: "`/auth/logout`", Payload: "RefreshParams", Response: "LogoutResponse"},
		{Name: "Me", Method: "GET", Path: "`/auth/me`", Response: "AuthBootstrapResponse"},
		{Name: "Refresh", Method: "POST", Path: "`/auth/refresh`", Payload: "RefreshParams", Response: "AuthSessionResponse"},
		{Name: "RequestPasswordReset", Method: "POST", Path: "`/auth/password-reset/request`", Payload: "PasswordResetRequestParams", Response: "PasswordResetRequestResponse"},
		{Name: "ResendEmailVerification", Method: "POST", Path: "`/auth/email-verification/resend`", Payload: "EmailVerificationResendParams", Response: "EmailVerificationResendResponse"},
		{Name: "SignupEmail", Method: "POST", Path: "`/auth/signup/email`", Payload: "EmailSignupParams", Response: "EmailSignupResponse"},
		{Name: "StartImpersonation", Method: "POST", Path: "`/auth/impersonation/start`", Payload: "StartImpersonationParams", Response: "AuthSessionResponse"},
		{Name: "StopImpersonation", Method: "POST", Path: "`/auth/impersonation/stop`", Payload: "RefreshParams", Response: "AuthSessionResponse"},
		{Name: "SwitchOrganization", Method: "POST", Path: "`/auth/organizations/switch`", Payload: "SwitchOrganizationParams", Response: "AuthBootstrapResponse"},
		{Name: "UpdateOrganization", Method: "PATCH", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}`)", Params: []string{"tenantID: string"}, Payload: "UpdateOrganizationParams", Response: "AuthBootstrapResponse"},
		{Name: "UpdateOrganizationMemberRole", Method: "PATCH", Path: "(`/auth/organizations/${encodeURIComponent(String(tenantID))}/members/${encodeURIComponent(String(userID))}`)", Params: []string{"tenantID: string", "userID: string"}, Payload: "UpdateMemberRoleParams", Response: "ListOrganizationMembersResponse"},
	}...)
	return methods
}

func standardUsersMethods() []standardTSMethod {
	return []standardTSMethod{
		{Name: "DevBootstrap", Method: "POST", Path: "`/users/dev-bootstrap`", Payload: "DevBootstrapParams", Response: "AuthResponse"},
	}
}

func standardAuthTypes(namespace string) string {
	if namespace == "users" {
		return `    export interface AuthResponse {
        token: string
    }

    export interface DevBootstrapParams {
        user_id?: string
        tenant_id?: string
    }

`
	}
	return `    export interface UserProfile {
        id: string
        email: string
        display_name: string
        avatar_url: string
        email_verified: boolean
        can_impersonate_users: boolean
    }

    export interface OrganizationSession {
        tenant_id: string
        tenant_name: string
        role: string
    }

    export interface ImpersonationState {
        actor_user_id?: string
        impersonation_id?: string
        reason?: string
    }

    export interface AuthBootstrapResponse {
        token: string
        user: UserProfile
        current_tenant_id: string
        organizations: OrganizationSession[]
        impersonation: ImpersonationState
    }

    export type AuthSessionResponse = AuthBootstrapResponse
    export interface EmailSignupParams { email: string; password: string; display_name?: string; redirect_path?: string }
    export interface EmailSignupResponse { requires_email_verification: boolean; email: string; dev_verification_token?: string }
    export interface EmailLoginParams { email: string; password: string }
    export interface RefreshParams { refresh_token?: string }
    export interface EmailVerificationConfirmParams { token: string }
    export interface EmailVerificationResendParams { email: string }
    export interface EmailVerificationResendResponse { ok: boolean; dev_verification_token?: string }
    export interface LogoutResponse { ok: boolean }
    export interface PasswordResetRequestParams { email: string }
    export interface PasswordResetRequestResponse { ok: boolean; dev_reset_token?: string }
    export interface PasswordResetConfirmParams { token: string; new_password: string }
    export interface ListOrganizationsResponse { current_tenant_id: string; sessions: OrganizationSession[] }
    export interface CreateOrganizationParams { name: string }
    export interface SwitchOrganizationParams { tenant_id: string }
    export interface UpdateOrganizationParams { name: string }
    export interface OrganizationMember { user_id: string; email: string; display_name: string; avatar_url: string; role: string; disabled: boolean }
    export interface ListOrganizationMembersResponse { members: OrganizationMember[] }
    export interface InviteMemberParams { email: string; role?: string }
    export interface InviteMemberResponse { ok: boolean; email: string; dev_invite_token?: string }
    export interface AcceptInviteParams { token: string }
    export interface UpdateMemberRoleParams { role: string }
    export interface DisableMemberParams { user_id: string }
    export interface StartImpersonationParams { target_user_id: string; tenant_id?: string; reason: string }

`
}
