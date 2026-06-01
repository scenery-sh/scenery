package agent

import (
	"crypto/rand"
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

func NewSession(req RegisterRequest, routerAddr, routerScheme string, existing *Session) (Session, error) {
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
	requestedSessionID, err := NormalizeSessionID(req.SessionID)
	if err != nil {
		return Session{}, err
	}
	if branch == "" && requestedSessionID != "" && existing != nil && existing.SessionID == requestedSessionID {
		branch = existing.Branch
	}
	if branch == "" {
		branch = discoverGitBranch(appRoot)
	}
	sessionID := SessionID(appRoot, branch)
	if requestedSessionID != "" {
		sessionID = requestedSessionID
	}
	if existing != nil && filepath.Clean(existing.AppRoot) != appRoot {
		return Session{}, fmt.Errorf("session %q already belongs to app root %s", sessionID, existing.AppRoot)
	}
	now := time.Now().UTC()
	createdAt := now
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "registered"
	}
	reportToken := strings.TrimSpace(req.ReportToken)
	if reportToken == "" && existing != nil {
		reportToken = existing.ReportToken
	}
	ownerPID := req.OwnerPID
	if ownerPID == 0 && existing != nil {
		ownerPID = existing.OwnerPID
	}
	owner := req.Owner
	if owner.PID == 0 && existing != nil && existing.Owner.PID > 0 && (ownerPID == 0 || ownerPID == existing.Owner.PID || ownerPID == existing.OwnerPID) {
		owner = existing.Owner
	}
	owner = OwnerFromRequest(ownerPID, owner, "onlava dev")
	processes := processesForSession(req.Processes, currentProcesses(existing))
	if strings.TrimSpace(req.AppPID) != "" {
		pid := parseProcessPID(req.AppPID)
		if pid > 0 {
			if processes == nil {
				processes = map[string]Process{}
			}
			process := processes[RouteAPI]
			if process.PID != pid {
				process = Process{PID: pid}
			}
			process.Owner = OwnerFromRequest(pid, process.Owner, "onlava dev api")
			processes[RouteAPI] = process
		}
	}
	backends := copyBackends(req.Backends)
	routes := routesForSession(sessionID, routerAddr, routerScheme, backends)
	session := Session{
		SchemaVersion: SessionSchemaVersion,
		SessionID:     sessionID,
		BaseAppID:     baseAppID,
		RuntimeAppID:  baseAppID + "--" + sessionID,
		AppRoot:       appRoot,
		StateRoot:     StateRoot(appRoot, sessionID),
		Branch:        branch,
		Status:        status,
		OwnerPID:      ownerPID,
		Owner:         owner,
		AppPID:        strings.TrimSpace(req.AppPID),
		Processes:     processes,
		Routes:        routes,
		Backends:      backends,
		ReportToken:   reportToken,
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

func UniqueSessionID(appRoot, branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = discoverGitBranch(appRoot)
	}
	base := SessionID(appRoot, branch)
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	if len(base) > 55 {
		base = strings.Trim(base[:55], "-")
	}
	return base + "-" + hex.EncodeToString(buf[:]), nil
}

func NormalizeSessionID(value string) (string, error) {
	id := sanitizeLabel(value)
	if strings.TrimSpace(value) != "" && id == "" {
		return "", fmt.Errorf("invalid session id %q", value)
	}
	return id, nil
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

func currentProcesses(existing *Session) map[string]Process {
	if existing == nil {
		return nil
	}
	return existing.Processes
}

func processesForSession(requested, existing map[string]Process) map[string]Process {
	if len(requested) == 0 {
		if len(existing) == 0 {
			return nil
		}
		return copyProcesses(existing)
	}
	processes := copyProcesses(requested)
	for name, process := range processes {
		if process.PID <= 0 {
			delete(processes, name)
			continue
		}
		if existingProcess, ok := existing[name]; ok && existingProcess.PID == process.PID && process.Owner.PID == 0 {
			process.Owner = existingProcess.Owner
		}
		process.Owner = OwnerFromRequest(process.PID, process.Owner, "onlava dev "+name)
		processes[name] = process
	}
	if len(processes) == 0 {
		return nil
	}
	return processes
}

func copyProcesses(values map[string]Process) map[string]Process {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]Process, len(values))
	for key, value := range values {
		key = sanitizeLabel(key)
		if key == "" || value.PID <= 0 {
			continue
		}
		copied[key] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func parseProcessPID(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var pid int
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		pid = pid*10 + int(r-'0')
	}
	return pid
}

func routesForSession(sessionID, routerAddr, routerScheme string, backends map[string]Backend) map[string]string {
	routes := map[string]string{}
	if _, ok := backends[RouteAPI]; ok {
		routes[RouteAPI] = routeURL(routerScheme, "api."+sessionID+".onlava.localhost", routerAddr, "")
	}
	routes[RouteDashboard] = routeURL(routerScheme, "console.onlava.localhost", routerAddr, "/s/"+sessionID)
	for kind := range backends {
		switch kind {
		case RouteAPI, RouteDashboard:
			continue
		}
		routes[kind] = routeURL(routerScheme, kind+"."+sessionID+".onlava.localhost", routerAddr, "")
	}
	return routes
}

func routeURL(scheme, host, routerAddr, path string) string {
	scheme = strings.TrimSpace(scheme)
	if scheme == "" {
		scheme = "http"
	}
	port := ""
	if _, p, err := net.SplitHostPort(routerAddr); err == nil {
		port = p
	}
	defaultPort := "80"
	if scheme == "https" {
		defaultPort = "443"
	}
	if port != "" && port != defaultPort {
		host += ":" + port
	}
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}
