package auth

type EmailVerificationConfirmParams struct {
	Token string `json:"token"`
}

type LogoutResponse struct {
	OK        bool   `json:"ok"`
	SetCookie string `json:"-" header:"Set-Cookie"`
}
