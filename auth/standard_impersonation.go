package auth

type StartImpersonationParams struct {
	TargetUserID string `json:"target_user_id"`
	TenantID     string `json:"tenant_id,omitempty"`
	Reason       string `json:"reason"`
}
