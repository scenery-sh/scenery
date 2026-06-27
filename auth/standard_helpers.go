package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	scenery "scenery.sh"
	authdb "scenery.sh/auth/db/gen"
)

func parseUUID(value string) (authdb.UUID, error) {
	var id authdb.UUID
	if err := id.Scan(strings.TrimSpace(value)); err != nil {
		return id, fmt.Errorf("invalid uuid")
	}
	if !id.Valid {
		return id, fmt.Errorf("invalid uuid")
	}
	return id, nil
}

func newUUID() (authdb.UUID, error) {
	return parseUUID(uuid.NewString())
}

func nullableUUID(value string) (authdb.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return authdb.UUID{}, nil
	}
	return parseUUID(value)
}

func uuidString(id authdb.UUID) string {
	if !id.Valid {
		return ""
	}
	value, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return value.String()
}

func timestamptz(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

func normalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("email is required")
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return "", fmt.Errorf("email is invalid")
	}
	normalized := strings.ToLower(strings.TrimSpace(parsed.Address))
	if normalized == "" || strings.ContainsAny(normalized, "\r\n\x00") {
		return "", fmt.Errorf("email is invalid")
	}
	return normalized, nil
}

func displayEmail(value string) string {
	return strings.TrimSpace(value)
}

func defaultDisplayName(email string, explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	local, _, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || strings.TrimSpace(local) == "" {
		return "User"
	}
	return strings.TrimSpace(local)
}

func defaultWorkspaceName(displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "My"
	}
	if strings.EqualFold(name, "my") {
		return "My Workspace"
	}
	return name + " Workspace"
}

func jsonBytes(value any) []byte {
	if value == nil {
		return []byte(`{}`)
	}
	out, err := json.Marshal(value)
	if err != nil || len(out) == 0 {
		return []byte(`{}`)
	}
	return out
}

func currentAuthData() (*AuthData, error) {
	raw := Data()
	data, ok := raw.(*AuthData)
	if !ok || data == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	return data, nil
}

func requestHeaders() http.Header {
	req := scenery.CurrentRequest()
	if req == nil {
		return nil
	}
	return req.Headers
}

func requestUserAgent() string {
	return strings.TrimSpace(requestHeaders().Get("User-Agent"))
}

func requestIPHash() string {
	headers := requestHeaders()
	value := strings.TrimSpace(headers.Get("X-Forwarded-For"))
	if value != "" {
		first, _, _ := strings.Cut(value, ",")
		value = strings.TrimSpace(first)
	}
	if value == "" {
		value = strings.TrimSpace(headers.Get("X-Real-IP"))
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func isLocalRuntime() bool {
	meta := scenery.Meta()
	return meta != nil && meta.Environment.Cloud == scenery.CloudLocal
}

func safeRedirectPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	return value
}
