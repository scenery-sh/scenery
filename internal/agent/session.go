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
	owner = OwnerFromRequest(ownerPID, owner, "scenery up")
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
			process.Owner = OwnerFromRequest(pid, process.Owner, "scenery up api")
			processes[RouteAPI] = process
		}
	}
	backends := copyBackends(req.Backends)
	routeNamespace := normalizeRouteNamespace(req.RouteNamespace, baseAppID)
	if routeNamespaceEmpty(req.RouteNamespace) && existing != nil && !routeNamespaceEmpty(existing.RouteNamespace) {
		routeNamespace = existing.RouteNamespace
	}
	routes := routesForSession(sessionID, routerAddr, routerScheme, backends, routeNamespace)
	routeManifest := normalizeRouteManifest(req.RouteManifest, sessionID, baseAppID, appRoot, branch, backends, routes)
	if routeManifest.Mode == RouteModePath {
		routes = routesForPathManifest(routeManifest)
	}
	session := Session{
		SchemaVersion:  SessionSchemaVersion,
		SessionID:      sessionID,
		BaseAppID:      baseAppID,
		RuntimeAppID:   baseAppID + "--" + sessionID,
		RouteNamespace: routeNamespace,
		AppRoot:        appRoot,
		StateRoot:      StateRoot(appRoot, sessionID),
		Branch:         branch,
		Status:         status,
		OwnerPID:       ownerPID,
		Owner:          owner,
		AppPID:         strings.TrimSpace(req.AppPID),
		Processes:      processes,
		RouteManifest:  routeManifest,
		Routes:         routes,
		Backends:       backends,
		ReportToken:    reportToken,
		CreatedAt:      createdAt,
		UpdatedAt:      now,
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

func normalizeRouteNamespace(namespace RouteNamespace, baseAppID string) RouteNamespace {
	namespace.Workspace = sanitizeLabel(namespace.Workspace)
	namespace.BaseDomain = normalizeRouteHost(namespace.BaseDomain)
	namespace.Hosts = copyRouteHosts(namespace.Hosts)
	if namespace.BaseDomain == "" {
		if fallback := sanitizeLabel(baseAppID); fallback != "" {
			if namespace.Workspace == "" && len(namespace.Hosts) == 0 {
				namespace.Workspace = fallback
			}
		}
		namespace.BaseDomain = DefaultRouteBaseDomain
	}
	return namespace
}

func routeNamespaceEmpty(namespace RouteNamespace) bool {
	return strings.TrimSpace(namespace.Workspace) == "" && strings.TrimSpace(namespace.BaseDomain) == "" && len(namespace.Hosts) == 0
}

func copyRouteHosts(hosts map[string]string) map[string]string {
	if len(hosts) == 0 {
		return nil
	}
	copied := make(map[string]string, len(hosts))
	for key, host := range hosts {
		key = sanitizeLabel(key)
		host = normalizeRouteHost(host)
		if key == "" || host == "" {
			continue
		}
		copied[key] = host
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func normalizeRouteHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		value = value[scheme+3:]
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
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
		process.Owner = OwnerFromRequest(process.PID, process.Owner, "scenery up "+name)
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

func routesForSession(sessionID, routerAddr, routerScheme string, backends map[string]Backend, namespace RouteNamespace) map[string]string {
	routes := map[string]string{}
	if _, ok := backends[RouteAPI]; ok {
		routes[RouteAPI] = routeURL(routerScheme, sessionRouteHost(RouteAPI, sessionID, namespace), routerAddr, "")
	}
	routes[RouteDashboard] = routeURL(routerScheme, sessionRouteHost("console", sessionID, namespace), routerAddr, "")
	for kind := range backends {
		switch kind {
		case RouteAPI, RouteDashboard:
			continue
		}
		routes[kind] = routeURL(routerScheme, sessionRouteHost(kind, sessionID, namespace), routerAddr, "")
	}
	return routes
}

func normalizeRouteManifest(manifest RouteManifest, sessionID, baseAppID, appRoot, branch string, backends map[string]Backend, hostRoutes map[string]string) RouteManifest {
	if strings.TrimSpace(string(manifest.Mode)) == "" && strings.TrimSpace(manifest.BaseURL) == "" && len(manifest.Routes) == 0 {
		return hostRouteManifestForSession(sessionID, branch, hostRoutes)
	}
	mode := manifest.Mode
	if mode == "" {
		mode = RouteModeHost
	}
	out := RouteManifest{
		SchemaVersion: firstNonEmpty(manifest.SchemaVersion, RouteManifestVersion),
		Mode:          mode,
		BaseURL:       normalizeBaseURL(manifest.BaseURL),
		Root:          strings.TrimSpace(manifest.Root),
		Worktree:      sanitizeLabel(firstNonEmpty(manifest.Worktree, branch, sessionID)),
		PortLease:     copyPortLease(manifest.PortLease),
	}
	if out.Root == "" {
		if mode == RouteModePath {
			out.Root = "scenery-console"
		} else {
			out.Root = RouteDashboard
		}
	}
	out.Routes = normalizeRouteRecords(manifest.Routes)
	if mode == RouteModePath {
		if out.BaseURL == "" && out.PortLease != nil {
			out.BaseURL = normalizeBaseURL(out.PortLease.URL)
		}
		out.Routes = completePathRouteRecords(out.BaseURL, out.Routes, backends)
		if out.PortLease != nil {
			out.PortLease.AppRoot = firstNonEmpty(out.PortLease.AppRoot, appRoot)
			out.PortLease.SessionID = firstNonEmpty(out.PortLease.SessionID, sessionID)
			out.PortLease.BaseAppID = firstNonEmpty(out.PortLease.BaseAppID, baseAppID)
			out.PortLease.Branch = firstNonEmpty(out.PortLease.Branch, branch)
			out.PortLease.WorktreeLabel = firstNonEmpty(out.PortLease.WorktreeLabel, out.Worktree)
		}
		return out
	}
	if len(out.Routes) == 0 {
		return hostRouteManifestForSession(sessionID, branch, hostRoutes)
	}
	return out
}

func hostRouteManifestForSession(sessionID, branch string, routes map[string]string) RouteManifest {
	records := map[string]RouteRecord{}
	for name, rawURL := range routes {
		name = normalizeRouteName(name)
		if name == "" || strings.TrimSpace(rawURL) == "" {
			continue
		}
		kind := name
		if name == RouteDashboard {
			kind = "scenery-console"
		}
		records[name] = RouteRecord{
			Name:    name,
			Kind:    kind,
			URL:     strings.TrimSpace(rawURL),
			Backend: backendForRouteName(name),
		}
	}
	return RouteManifest{
		SchemaVersion: RouteManifestVersion,
		Mode:          RouteModeHost,
		Root:          RouteDashboard,
		Worktree:      sanitizeLabel(firstNonEmpty(branch, sessionID)),
		Routes:        records,
	}
}

func completePathRouteRecords(baseURL string, records map[string]RouteRecord, backends map[string]Backend) map[string]RouteRecord {
	if records == nil {
		records = map[string]RouteRecord{}
	}
	if _, ok := records["root"]; !ok {
		records["root"] = RouteRecord{Name: "root", Kind: "scenery-console", URL: joinRouteURL(baseURL, "/"), Path: "/"}
	}
	if _, ok := records[RouteDashboard]; !ok {
		records[RouteDashboard] = RouteRecord{Name: RouteDashboard, Kind: "scenery-console", URL: joinRouteURL(baseURL, PathModeDashboardPrefix+"/"), Path: PathModeDashboardPrefix + "/", StripPrefix: PathModeDashboardPrefix, Backend: RouteDashboard}
	}
	for name := range backends {
		name = normalizeRouteName(name)
		if name == "" {
			continue
		}
		if _, ok := records[name]; ok {
			continue
		}
		routePath := "/" + name + "/"
		kind := name
		if isFrontendRouteName(name) {
			kind = "frontend"
		}
		records[name] = RouteRecord{
			Name:        name,
			Kind:        kind,
			URL:         joinRouteURL(baseURL, routePath),
			Path:        routePath,
			StripPrefix: strings.TrimSuffix(routePath, "/"),
			Backend:     backendForRouteName(name),
		}
	}
	return normalizeRouteRecords(records)
}

func normalizeRouteRecords(records map[string]RouteRecord) map[string]RouteRecord {
	if len(records) == 0 {
		return nil
	}
	out := make(map[string]RouteRecord, len(records))
	for name, record := range records {
		name = normalizeRouteName(firstNonEmpty(record.Name, name))
		if name == "" {
			continue
		}
		record.Name = name
		if record.Kind == "" {
			record.Kind = name
		}
		record.URL = strings.TrimSpace(record.URL)
		record.Path = normalizeRoutePath(record.Path)
		record.StripPrefix = normalizeStripPrefix(record.StripPrefix)
		record.Backend = normalizeRouteName(record.Backend)
		if record.Backend == "" {
			record.Backend = backendForRouteName(name)
		}
		out[name] = record
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func routesForPathManifest(manifest RouteManifest) map[string]string {
	routes := map[string]string{}
	for name, record := range manifest.Routes {
		name = normalizeRouteName(name)
		if name == "" || strings.TrimSpace(record.URL) == "" {
			continue
		}
		routes[name] = strings.TrimSpace(record.URL)
	}
	return routes
}

func normalizeRouteName(name string) string {
	if strings.TrimSpace(name) == "root" {
		return "root"
	}
	if strings.TrimSpace(name) == "console" {
		return RouteDashboard
	}
	return sanitizeLabel(name)
}

func isFrontendRouteName(name string) bool {
	switch name {
	case "", "root", RouteAPI, RouteDashboard:
		return false
	default:
		return true
	}
}

func backendForRouteName(name string) string {
	if name == "root" {
		return ""
	}
	return normalizeRouteName(name)
}

func normalizeBaseURL(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimRight(value, "/")
}

func normalizeRoutePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	value = pathClean(value)
	if value != "/" && !strings.HasSuffix(value, "/") {
		value += "/"
	}
	return value
}

func normalizeStripPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimSuffix(pathClean(value), "/")
}

func pathClean(value string) string {
	parts := strings.Split(value, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return "/"
	}
	return "/" + strings.Join(stack, "/")
}

func joinRouteURL(baseURL, routePath string) string {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		return ""
	}
	routePath = normalizeRoutePath(routePath)
	if routePath == "" {
		routePath = "/"
	}
	return baseURL + routePath
}

func copyPortLease(lease *PortLease) *PortLease {
	if lease == nil {
		return nil
	}
	copied := *lease
	if copied.SchemaVersion == "" {
		copied.SchemaVersion = "scenery.dev.port_lease.v1"
	}
	return &copied
}

func sessionRouteHost(route, sessionID string, namespace RouteNamespace) string {
	route = sanitizeLabel(route)
	sessionID = sanitizeLabel(sessionID)
	if route == "" || sessionID == "" {
		return ""
	}
	if host := namespace.Hosts[route]; host != "" {
		return insertSessionIntoHost(host, sessionID)
	}
	baseDomain := normalizeRouteHost(namespace.BaseDomain)
	if baseDomain == "" {
		baseDomain = DefaultRouteBaseDomain
	}
	return route + "." + sessionID + "." + baseDomain
}

func insertSessionIntoHost(host, sessionID string) string {
	host = normalizeRouteHost(host)
	sessionID = sanitizeLabel(sessionID)
	if host == "" || sessionID == "" {
		return host
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return host
	}
	labels = append(labels[:1], append([]string{sessionID}, labels[1:]...)...)
	return strings.Join(labels, ".")
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
