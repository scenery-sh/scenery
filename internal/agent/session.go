package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func NewSession(req RegisterRequest, routerAddr string, existing *Session) (Session, error) {
	appRoot, err := filepath.Abs(strings.TrimSpace(req.AppRoot))
	if err != nil {
		return Session{}, err
	}
	if appRoot == "" {
		return Session{}, fmt.Errorf("app_root must not be empty")
	}
	baseAppID := strings.TrimSpace(req.BaseAppID)
	if baseAppID == "" {
		return Session{}, fmt.Errorf("base_app_id must not be empty")
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = discoverGitBranch(appRoot)
	}
	sessionID := SessionID(appRoot, branch)
	now := time.Now().UTC()
	createdAt := now
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "registered"
	}
	backends := copyBackends(req.Backends)
	routes := routesForSession(sessionID, routerAddr, backends)
	session := Session{
		SchemaVersion: SessionSchemaVersion,
		SessionID:     sessionID,
		BaseAppID:     baseAppID,
		RuntimeAppID:  baseAppID + "--" + sessionID,
		AppRoot:       appRoot,
		StateRoot:     StateRoot(appRoot, sessionID),
		Branch:        branch,
		Status:        status,
		OwnerPID:      req.OwnerPID,
		AppPID:        strings.TrimSpace(req.AppPID),
		Routes:        routes,
		Backends:      backends,
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}
	return session, nil
}

func SessionID(appRoot, branch string) string {
	label := sanitizeLabel(branch)
	if label == "" {
		label = sanitizeLabel(filepath.Base(appRoot))
	}
	if label == "" {
		label = "session"
	}
	sum := sha256.Sum256([]byte(filepath.Clean(appRoot)))
	suffix := hex.EncodeToString(sum[:])[:6]
	if len(label) > 48 {
		label = strings.Trim(label[:48], "-")
	}
	return label + "-" + suffix
}

func sanitizeLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if r == '-' || r == '_' || r == '/' || r == '.' || unicode.IsSpace(r) {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func discoverGitBranch(root string) string {
	cmd := exec.Command("git", "-C", root, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func copyBackends(backends map[string]Backend) map[string]Backend {
	if len(backends) == 0 {
		return map[string]Backend{}
	}
	copied := make(map[string]Backend, len(backends))
	for key, backend := range backends {
		key = sanitizeLabel(key)
		if key == "" {
			continue
		}
		backend.Network = strings.TrimSpace(backend.Network)
		if backend.Network == "" {
			backend.Network = "tcp"
		}
		backend.Addr = strings.TrimSpace(backend.Addr)
		if backend.Addr == "" {
			continue
		}
		copied[key] = backend
	}
	return copied
}

func routesForSession(sessionID, routerAddr string, backends map[string]Backend) map[string]string {
	routes := map[string]string{}
	if _, ok := backends[RouteAPI]; ok {
		routes[RouteAPI] = routeURL("api."+sessionID+".onlava.localhost", routerAddr, "")
	}
	if _, ok := backends[RouteDashboard]; ok {
		routes[RouteDashboard] = routeURL("console.onlava.localhost", routerAddr, "/s/"+sessionID)
	}
	if _, ok := backends[RouteMCP]; ok {
		routes[RouteMCP] = routeURL("mcp."+sessionID+".onlava.localhost", routerAddr, "/sse")
	}
	return routes
}

func routeURL(host, routerAddr, path string) string {
	port := ""
	if _, p, err := net.SplitHostPort(routerAddr); err == nil {
		port = p
	}
	if port != "" && port != "80" {
		host += ":" + port
	}
	if path == "" {
		path = "/"
	}
	return "http://" + host + path
}
