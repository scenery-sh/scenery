package auth

type SwitchOrganizationParams struct {
	TenantID string `json:"tenant_id"`
}

type OrganizationMember struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
}

type ListOrganizationMembersResponse struct {
	Members []OrganizationMember `json:"members"`
}

type InviteMemberParams struct {
	Email string `json:"email"`
	Role  string `json:"role,omitempty"`
}

type InviteMemberResponse struct {
	OK             bool   `json:"ok"`
	Email          string `json:"email"`
	DevInviteToken string `json:"dev_invite_token,omitempty"`
}

type AcceptInviteParams struct {
	Token string `json:"token"`
}

type UpdateMemberRoleParams struct {
	Role string `json:"role"`
}

type DisableMemberParams struct {
	UserID string `json:"user_id"`
}
