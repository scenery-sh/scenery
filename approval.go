package scenery

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ApprovalToken is a detached, plan-bound authorization for explicitly named
// risk scopes. Signature is excluded from ApprovalTokenPayload.
type ApprovalToken struct {
	PlanID     string    `json:"plan_id"`
	Caller     string    `json:"caller"`
	RiskScopes []string  `json:"risk_scopes"`
	ExpiresAt  time.Time `json:"expires_at"`
	Signature  string    `json:"signature"`
}

// ApprovalTokenPayload returns the canonical bytes an approval service signs.
func ApprovalTokenPayload(token ApprovalToken) ([]byte, error) {
	scopes, err := validateApprovalTokenClaims(token)
	if err != nil {
		return nil, err
	}
	projection := struct {
		PlanID     string    `json:"plan_id"`
		Caller     string    `json:"caller"`
		RiskScopes []string  `json:"risk_scopes"`
		ExpiresAt  time.Time `json:"expires_at"`
	}{token.PlanID, token.Caller, scopes, token.ExpiresAt.UTC()}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, err
	}
	return MarshalContractValue(JSON(encoded), "json")
}

// ValidateApprovalToken enforces the scenery.approval-token.v1 shape and
// signature encoding before a caller attempts trust-root verification.
func ValidateApprovalToken(token ApprovalToken) error {
	if _, err := validateApprovalTokenClaims(token); err != nil {
		return err
	}
	parts := strings.Split(token.Signature, ":")
	if len(parts) != 3 || parts[0] != "ed25519" || invalidApprovalKeyID(parts[1]) {
		return fmt.Errorf("approval signature must use ed25519:<key-id>:<base64>")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(parts[2])
	}
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return fmt.Errorf("approval signature must contain a 64-byte standard-base64 Ed25519 signature")
	}
	return nil
}

func validateApprovalTokenClaims(token ApprovalToken) ([]string, error) {
	if len(token.PlanID) != len("sha256:")+64 || !strings.HasPrefix(token.PlanID, "sha256:") {
		return nil, fmt.Errorf("approval plan_id must be a canonical SHA-256 digest")
	}
	digest := strings.TrimPrefix(token.PlanID, "sha256:")
	if digest != strings.ToLower(digest) {
		return nil, fmt.Errorf("approval plan_id must be a canonical SHA-256 digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return nil, fmt.Errorf("approval plan_id must be a canonical SHA-256 digest")
	}
	if token.Caller == "" || !utf8.ValidString(token.Caller) {
		return nil, fmt.Errorf("approval caller must be a non-empty UTF-8 string")
	}
	if len(token.RiskScopes) == 0 {
		return nil, fmt.Errorf("approval risk_scopes must not be empty")
	}
	seen := make(map[string]bool, len(token.RiskScopes))
	scopes := append([]string(nil), token.RiskScopes...)
	for _, scope := range scopes {
		if scope == "" || !utf8.ValidString(scope) {
			return nil, fmt.Errorf("approval risk scopes must be non-empty UTF-8 strings")
		}
		if seen[scope] {
			return nil, fmt.Errorf("approval risk scope %q is duplicated", scope)
		}
		seen[scope] = true
	}
	sort.Strings(scopes)
	return scopes, nil
}

func invalidApprovalKeyID(value string) bool {
	if value == "" || strings.Contains(value, ":") || !utf8.ValidString(value) {
		return true
	}
	return strings.IndexFunc(value, unicode.IsSpace) >= 0
}
