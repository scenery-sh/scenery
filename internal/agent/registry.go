package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

type Registry struct {
	path             string
	router           string
	scheme           string
	mu               sync.Mutex
	sessions         map[string]Session
	substrates       map[string]Substrate
	aliases          map[string]AliasLease
	routeHosts       map[string]routeTarget
	currentByAppRoot map[string]string
}

type routeTarget struct {
	SessionID string
	Route     string
}

type registryFile struct {
	machine.ArtifactIdentity
	Sessions         []Session         `json:"sessions"`
	Substrates       []Substrate       `json:"substrates,omitempty"`
	Aliases          []AliasLease      `json:"aliases,omitempty"`
	CurrentByAppRoot map[string]string `json:"current_by_app_root,omitempty"`
}

func OpenRegistry(path, routerAddr string, routerScheme ...string) (*Registry, error) {
	scheme := "http"
	if len(routerScheme) > 0 && strings.TrimSpace(routerScheme[0]) != "" {
		scheme = strings.TrimSpace(routerScheme[0])
	}
	r := &Registry{
		path:             path,
		router:           routerAddr,
		scheme:           scheme,
		sessions:         make(map[string]Session),
		substrates:       make(map[string]Substrate),
		aliases:          make(map[string]AliasLease),
		routeHosts:       make(map[string]routeTarget),
		currentByAppRoot: make(map[string]string),
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) UpsertSubstrate(req UpsertSubstrateRequest) (Substrate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := sanitizeLabel(req.Kind)
	if kind == "" {
		return Substrate{}, errors.New("substrate kind must not be empty")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "ready"
	}
	now := time.Now().UTC()
	createdAt := now
	var current *Substrate
	if existing, ok := r.substrates[kind]; ok {
		current = &existing
	}
	if current != nil && !current.CreatedAt.IsZero() {
		createdAt = current.CreatedAt
	}
	ownerPID := req.OwnerPID
	if ownerPID == 0 && current != nil {
		ownerPID = current.OwnerPID
	}
	owner := req.Owner
	if owner.PID == 0 && current != nil && current.Owner.PID > 0 {
		owner = current.Owner
	}
	owner = OwnerFromRequest(ownerPID, owner, "scenery substrate")
	pids := copyIntMap(req.PIDs)
	owners := ownersForSubstrate(kind, pids, req.Owners, current)
	lastExit := copySubstrateExit(req.LastExit)
	componentExits := componentExitsForSubstrate(status, req.ComponentExits, current)
	if lastExit == nil && current != nil && status != "ready" {
		lastExit = copySubstrateExit(current.LastExit)
	}
	substrate := Substrate{
		ArtifactIdentity: substrateIdentity(),
		Kind:             kind,
		Status:           status,
		OwnerPID:         ownerPID,
		Owner:            owner,
		PIDs:             pids,
		Owners:           owners,
		URLs:             copyStringMap(req.URLs),
		Endpoints:        copyStringMap(req.Endpoints),
		Leases:           leasesForSubstrate(req.Leases, current),
		LastExit:         lastExit,
		ComponentExits:   componentExits,
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	r.substrates[kind] = substrate
	if err := r.saveLocked(); err != nil {
		return Substrate{}, err
	}
	return substrate, nil
}

func (r *Registry) GetSubstrate(kind string) (Substrate, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	substrate, ok := r.substrates[sanitizeLabel(kind)]
	return substrate, ok
}

func (r *Registry) ListSubstrates() []Substrate {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedSubstrates(r.substrates)
}

func (r *Registry) DeleteSubstrate(kind string) (Substrate, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := sanitizeLabel(kind)
	substrate, ok := r.substrates[key]
	if !ok {
		return Substrate{}, false, nil
	}
	delete(r.substrates, key)
	if err := r.saveLocked(); err != nil {
		return Substrate{}, false, err
	}
	return substrate, true, nil
}

func (r *Registry) Upsert(req RegisterRequest) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessionID, err := NormalizeSessionID(req.SessionID)
	if err != nil {
		return Session{}, err
	}
	if sessionID == "" {
		branch := strings.TrimSpace(req.Branch)
		if branch == "" {
			branch = discoverGitBranch(req.AppRoot)
		}
		sessionID = SessionID(req.AppRoot, branch)
	}
	var existing *Session
	if current, ok := r.sessions[sessionID]; ok {
		existing = &current
	}
	session, err := NewSession(req, r.router, r.scheme, existing)
	if err != nil {
		return Session{}, err
	}
	if existing != nil && !requestMayClaimSession(req, *existing, session) {
		existingPID := firstPositive(existing.OwnerPID, existing.Owner.PID)
		return Session{}, fmt.Errorf("scenery up session %q is already running for app root %s under owner PID %d", sessionID, existing.AppRoot, existingPID)
	}
	if blocking, ok := r.blockingAppRootSessionLocked(session); ok {
		blockingPID := firstPositive(blocking.OwnerPID, blocking.Owner.PID)
		return Session{}, fmt.Errorf("scenery up is already running for app root %s under owner PID %d; use a separate Git worktree for another live code copy", session.AppRoot, blockingPID)
	}
	session.Aliases, session.AliasConflicts = r.claimAliasesLocked(session, req.ClaimAliases)
	r.claimDomainHostLocked(&session, req.ClaimAliases)
	r.sessions[session.SessionID] = session
	r.currentByAppRoot[filepath.Clean(session.AppRoot)] = session.SessionID
	r.rebuildRouteHostIndexLocked()
	if err := r.saveLocked(); err != nil {
		return Session{}, err
	}
	if err := WriteManifest(session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (r *Registry) blockingAppRootSessionLocked(next Session) (Session, bool) {
	nextRoot := filepath.Clean(next.AppRoot)
	for _, existing := range r.sessions {
		if existing.SessionID == next.SessionID || filepath.Clean(existing.AppRoot) != nextRoot {
			continue
		}
		if sessionBlocksAppRootRegistration(existing) {
			return existing, true
		}
	}
	return Session{}, false
}

func sessionBlocksAppRootRegistration(existing Session) bool {
	existingPID := firstPositive(existing.OwnerPID, existing.Owner.PID)
	if existingPID <= 0 {
		return false
	}
	owner := existing.Owner
	if owner.PID != existingPID {
		owner = Owner{}
	}
	if owner.PID <= 0 {
		owner.PID = existingPID
	}
	if VerifyOwner(owner) == nil {
		return true
	}
	return ownerProcessInspectable(existingPID)
}

func requestMayClaimSession(req RegisterRequest, existing, next Session) bool {
	requestPID := firstPositive(req.OwnerPID, req.Owner.PID, next.OwnerPID, next.Owner.PID)
	existingPID := firstPositive(existing.OwnerPID, existing.Owner.PID)
	if requestPID <= 0 || existingPID <= 0 || requestPID == existingPID {
		return true
	}
	owner := existing.Owner
	if owner.PID != existingPID {
		owner = Owner{}
	}
	if owner.PID <= 0 {
		owner.PID = existing.OwnerPID
	}
	if VerifyOwner(owner) != nil {
		if ownerProcessInspectable(existingPID) {
			return false
		}
		return req.ClaimOwner
	}
	return false
}

func ownerProcessInspectable(pid int) bool {
	if pid <= 0 {
		return false
	}
	owner := CaptureOwner(pid, "")
	return owner.PID > 0 && (owner.StartedAt != "" || owner.CmdlineHash != "" || owner.Exe != "")
}

func (r *Registry) Get(id string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[strings.TrimSpace(id)]
	return session, ok
}

func (r *Registry) List() []Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedSessions(r.sessions)
}

func (r *Registry) RouteTargetForHost(host string) (Session, string, bool) {
	host = normalizeRouteHost(host)
	if host == "" {
		return Session{}, "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target, ok := r.routeHosts[host]
	if !ok {
		return Session{}, "", false
	}
	session, ok := r.sessions[target.SessionID]
	if !ok {
		return Session{}, "", false
	}
	return session, target.Route, true
}

func (r *Registry) FindByAppRoot(root string) []Session {
	root = filepath.Clean(strings.TrimSpace(root))
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []Session
	for _, session := range r.sessions {
		if filepath.Clean(session.AppRoot) == root {
			matches = append(matches, session)
		}
	}
	sortSessions(matches)
	currentID := r.currentByAppRoot[root]
	if currentID != "" {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].SessionID == currentID {
				return true
			}
			if matches[j].SessionID == currentID {
				return false
			}
			return false
		})
	}
	return matches
}

func (r *Registry) Delete(id string) (Session, bool, error) {
	return r.delete(id, 0, Owner{}, false)
}

func (r *Registry) DeleteOwned(id string, ownerPID int) (Session, bool, error) {
	return r.delete(id, ownerPID, Owner{}, false)
}

func (r *Registry) DeleteOwnedIdentity(id string, ownerPID int, owner Owner, strict bool) (Session, bool, error) {
	return r.delete(id, ownerPID, owner, strict)
}

func (r *Registry) delete(id string, ownerPID int, requestedOwner Owner, strict bool) (Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id = strings.TrimSpace(id)
	session, ok := r.sessions[id]
	if !ok {
		return Session{}, false, nil
	}
	if ownerPID < 0 && firstPositive(session.OwnerPID, session.Owner.PID) > 0 {
		return session, false, nil
	}
	if ownerPID > 0 && !sessionOwnerIdentityMatches(session, ownerPID, requestedOwner, strict) {
		return session, false, nil
	}
	delete(r.sessions, id)
	for host, alias := range r.aliases {
		if alias.SessionID == id {
			delete(r.aliases, host)
		}
	}
	key := filepath.Clean(session.AppRoot)
	if r.currentByAppRoot[key] == id {
		delete(r.currentByAppRoot, key)
		for _, candidate := range sortedSessions(r.sessions) {
			if filepath.Clean(candidate.AppRoot) == key {
				r.currentByAppRoot[key] = candidate.SessionID
				break
			}
		}
	}
	r.rebuildRouteHostIndexLocked()
	if err := r.saveLocked(); err != nil {
		return Session{}, false, err
	}
	return session, true, nil
}

func sessionOwnerIdentityMatches(session Session, ownerPID int, requested Owner, strict bool) bool {
	effectivePID := firstPositive(session.OwnerPID, session.Owner.PID)
	if effectivePID != ownerPID {
		return false
	}
	if !strict {
		return true
	}
	if requested.PID != ownerPID || !ownerHasFingerprint(requested) {
		return false
	}
	current := session.Owner
	if current.PID != ownerPID || !ownerHasFingerprint(current) {
		return false
	}
	if requested.StartedAt != "" && current.StartedAt != requested.StartedAt {
		return false
	}
	if requested.CmdlineHash != "" && current.CmdlineHash != requested.CmdlineHash {
		return false
	}
	if requested.Exe != "" && current.Exe != requested.Exe {
		return false
	}
	return true
}

func ownerHasFingerprint(owner Owner) bool {
	return strings.TrimSpace(owner.StartedAt) != "" || strings.TrimSpace(owner.CmdlineHash) != "" || strings.TrimSpace(owner.Exe) != ""
}

func (r *Registry) claimAliasesLocked(session Session, force bool) (map[string]string, map[string]AliasLease) {
	if len(session.RouteNamespace.Hosts) == 0 {
		r.removeSessionAliasesLocked(session.SessionID)
		return nil, nil
	}
	now := session.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	desired := map[string]string{}
	for configuredRoute, configuredHost := range session.RouteNamespace.Hosts {
		route := normalizeAliasRoute(configuredRoute)
		host := normalizeRouteHost(configuredHost)
		if route == "" || host == "" || session.RouteManifest.Routes[route].URL == "" {
			continue
		}
		desired[host] = route
	}
	for host, alias := range r.aliases {
		if alias.SessionID == session.SessionID {
			if desired[host] == "" {
				delete(r.aliases, host)
			}
		}
	}
	aliases := map[string]string{}
	conflicts := map[string]AliasLease{}
	for host, route := range desired {
		existing, claimed := r.aliases[host]
		if claimed && existing.SessionID != session.SessionID {
			if !force && !aliasLeaseOwnerStale(existing) {
				conflicts[route] = existing
				continue
			}
			r.removeAliasFromSessionLocked(existing)
		}
		createdAt := now
		if claimed && !existing.CreatedAt.IsZero() {
			createdAt = existing.CreatedAt
		}
		url := routeURL(r.scheme, host, r.router, "")
		r.aliases[host] = AliasLease{
			Host:      host,
			Route:     route,
			SessionID: session.SessionID,
			AppRoot:   session.AppRoot,
			OwnerPID:  session.OwnerPID,
			Owner:     session.Owner,
			URL:       url,
			CreatedAt: createdAt,
			UpdatedAt: now,
		}
		aliases[route] = url
	}
	for route, alias := range conflicts {
		if aliases[route] != "" {
			delete(conflicts, route)
		} else {
			conflicts[route] = normalizeAliasLease(alias)
		}
	}
	if len(aliases) == 0 {
		aliases = nil
	}
	if len(conflicts) == 0 {
		conflicts = nil
	}
	return aliases, conflicts
}

// claimDomainHostLocked enforces single ownership of a path-mode dev domain
// host across sessions. A live verified owner keeps the host: the newcomer's
// manifest drops it and records the conflict. A provably stale owner loses
// the host to the newcomer; `force` transfers it from a live owner the same
// way `--claim-aliases` transfers alias leases.
func (r *Registry) claimDomainHostLocked(session *Session, force bool) {
	session.DomainHostConflict = nil
	host := normalizeRouteHost(session.RouteManifest.DomainHost)
	if session.RouteManifest.Mode != RouteModePath || host == "" {
		return
	}
	for id, other := range r.sessions {
		if id == session.SessionID {
			continue
		}
		if other.RouteManifest.Mode != RouteModePath || normalizeRouteHost(other.RouteManifest.DomainHost) != host {
			continue
		}
		if !force && !sessionDomainHostOwnerStale(other) {
			session.DomainHostConflict = &AliasLease{
				Host:      host,
				Route:     RoutePathMode,
				SessionID: other.SessionID,
				AppRoot:   other.AppRoot,
				OwnerPID:  other.OwnerPID,
				Owner:     other.Owner,
				URL:       "https://" + host,
				CreatedAt: other.CreatedAt,
				UpdatedAt: other.UpdatedAt,
			}
			session.RouteManifest.DomainHost = ""
			session.RouteManifest.DomainURL = ""
			return
		}
		other.RouteManifest.DomainHost = ""
		other.RouteManifest.DomainURL = ""
		r.sessions[id] = other
	}
}

// sessionDomainHostOwnerStale mirrors aliasLeaseOwnerStale: a host owner is
// stale only when a recorded fingerprint provably no longer matches a live
// process. Missing owners or missing fingerprints stay conservative.
func sessionDomainHostOwnerStale(session Session) bool {
	owner := session.Owner
	pid := firstPositive(session.OwnerPID, owner.PID)
	if pid <= 0 {
		return false
	}
	if owner.PID > 0 && owner.PID != pid {
		owner = Owner{}
	}
	owner.PID = pid
	if !ownerHasFingerprint(owner) {
		return false
	}
	return VerifyOwner(owner) != nil
}

func (r *Registry) removeSessionAliasesLocked(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	for host, alias := range r.aliases {
		if alias.SessionID == sessionID {
			delete(r.aliases, host)
		}
	}
}

func (r *Registry) removeAliasFromSessionLocked(alias AliasLease) {
	session, ok := r.sessions[alias.SessionID]
	if !ok {
		return
	}
	route := normalizeAliasRoute(alias.Route)
	if route == "" {
		return
	}
	if len(session.Aliases) > 0 {
		delete(session.Aliases, route)
		if len(session.Aliases) == 0 {
			session.Aliases = nil
		}
	}
	if len(session.AliasConflicts) > 0 {
		delete(session.AliasConflicts, route)
		if len(session.AliasConflicts) == 0 {
			session.AliasConflicts = nil
		}
	}
	r.sessions[session.SessionID] = session
}

func aliasLeaseOwnerStale(alias AliasLease) bool {
	owner := alias.Owner
	pid := firstPositive(alias.OwnerPID, owner.PID)
	if pid <= 0 {
		return false
	}
	if owner.PID > 0 && owner.PID != pid {
		owner = Owner{}
	}
	owner.PID = pid
	if !ownerHasFingerprint(owner) {
		return false
	}
	return VerifyOwner(owner) != nil
}

func normalizeAliasLease(alias AliasLease) AliasLease {
	alias.Host = normalizeRouteHost(alias.Host)
	alias.Route = normalizeAliasRoute(alias.Route)
	return alias
}

func normalizeAliasRoute(route string) string {
	route = sanitizeLabel(route)
	if route == "console" {
		return RouteDashboard
	}
	return route
}

func (r *Registry) load() error {
	_, err := os.Stat(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file registryFile
	if err := LoadDurableArtifact(r.path, &file, &file.ArtifactIdentity, AgentRegistryKind, agentRegistrySchemaDescriptor, 0o644, migrateLegacyRegistry); err != nil {
		return err
	}
	for _, session := range file.Sessions {
		if session.SessionID == "" {
			continue
		}
		r.sessions[session.SessionID] = session
	}
	for _, substrate := range file.Substrates {
		kind := sanitizeLabel(substrate.Kind)
		if kind == "" {
			continue
		}
		substrate.Kind = kind
		r.substrates[kind] = substrate
	}
	for _, alias := range file.Aliases {
		host := normalizeRouteHost(alias.Host)
		route := normalizeAliasRoute(alias.Route)
		if host == "" || route == "" || strings.TrimSpace(alias.SessionID) == "" {
			continue
		}
		alias.Host = host
		alias.Route = route
		r.aliases[host] = alias
	}
	for appRoot, sessionID := range file.CurrentByAppRoot {
		appRoot = filepath.Clean(strings.TrimSpace(appRoot))
		sessionID = strings.TrimSpace(sessionID)
		if appRoot != "" && sessionID != "" {
			r.currentByAppRoot[appRoot] = sessionID
		}
	}
	r.rebuildRouteHostIndexLocked()
	return nil
}

func migrateLegacyRegistry(fields map[string]json.RawMessage) error {
	if _, ok := fields["sessions"]; !ok {
		return fmt.Errorf("unsupported legacy agent registry")
	}
	if err := migrateLegacyArtifactList(fields, "sessions", func(item map[string]json.RawMessage) error {
		if err := requireLegacySchemaOrMissing(item, "scenery.dev.session.v1"); err != nil {
			return err
		}
		addIdentityFields(item, sessionIdentity())
		if raw := item["route_manifest"]; len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
			var manifest map[string]json.RawMessage
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return err
			}
			if version := manifest["schema_version"]; len(version) > 0 {
				if err := requireLegacySchema(manifest, "scenery.route_manifest.v1"); err != nil {
					return err
				}
			}
			addIdentityFields(manifest, routeManifestIdentity())
			if leaseRaw := manifest["port_lease"]; len(leaseRaw) > 0 && string(leaseRaw) != "null" {
				var lease map[string]json.RawMessage
				if err := json.Unmarshal(leaseRaw, &lease); err != nil {
					return err
				}
				if version := lease["schema_version"]; len(version) > 0 {
					if err := requireLegacySchema(lease, "scenery.dev.port_lease.v1"); err != nil {
						return err
					}
				}
				addIdentityFields(lease, portLeaseIdentity())
				encodedLease, err := json.Marshal(lease)
				if err != nil {
					return err
				}
				manifest["port_lease"] = encodedLease
			}
			encodedManifest, err := json.Marshal(manifest)
			if err != nil {
				return err
			}
			item["route_manifest"] = encodedManifest
		}
		return nil
	}); err != nil {
		return err
	}
	return migrateLegacyArtifactList(fields, "substrates", func(item map[string]json.RawMessage) error {
		if err := requireLegacySchemaOrMissing(item, "scenery.dev.substrate.v1"); err != nil {
			return err
		}
		renameLegacyField(item, "kind", "substrate_kind")
		addIdentityFields(item, substrateIdentity())
		return nil
	})
}

func migrateLegacyArtifactList(fields map[string]json.RawMessage, name string, migrate func(map[string]json.RawMessage) error) error {
	raw := fields[name]
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	for _, item := range items {
		if err := migrate(item); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return err
	}
	fields[name] = encoded
	return nil
}

func (r *Registry) rebuildRouteHostIndexLocked() {
	hosts := make(map[string]routeTarget)
	for _, session := range r.sessions {
		if session.RouteManifest.Mode == RouteModePath {
			host := normalizeRouteHost(session.RouteManifest.DomainHost)
			if host == "" {
				continue
			}
			// Claim-time ownership keeps at most one session per host; the
			// preference check only makes stale persisted duplicates
			// deterministic across map iteration order.
			if existing, ok := hosts[host]; ok && !r.domainHostIndexPrefersLocked(session, existing) {
				continue
			}
			hosts[host] = routeTarget{SessionID: session.SessionID, Route: RoutePathMode}
			continue
		}
		for route, record := range session.RouteManifest.Routes {
			host := normalizeRouteHost(record.URL)
			route = normalizeAliasRoute(route)
			if host == "" || route == "" {
				continue
			}
			hosts[host] = routeTarget{SessionID: session.SessionID, Route: route}
		}
	}
	for host, alias := range r.aliases {
		route := normalizeAliasRoute(alias.Route)
		host = normalizeRouteHost(firstNonEmpty(alias.Host, host, alias.URL))
		if host == "" || route == "" || strings.TrimSpace(alias.SessionID) == "" {
			continue
		}
		if _, ok := r.sessions[alias.SessionID]; !ok {
			continue
		}
		hosts[host] = routeTarget{SessionID: alias.SessionID, Route: route}
	}
	r.routeHosts = hosts
}

func (r *Registry) domainHostIndexPrefersLocked(candidate Session, existing routeTarget) bool {
	current, ok := r.sessions[existing.SessionID]
	if !ok {
		return true
	}
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if current.UpdatedAt.After(candidate.UpdatedAt) {
		return false
	}
	return candidate.SessionID < current.SessionID
}

func (r *Registry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registryFile{
		ArtifactIdentity: agentRegistryIdentity(),
		Sessions:         sortedSessions(r.sessions),
		Substrates:       sortedSubstrates(r.substrates),
		Aliases:          sortedAliases(r.aliases),
		CurrentByAppRoot: copyRawStringMap(r.currentByAppRoot),
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(r.path, data, 0o644)
}

func sortedSubstrates(substrates map[string]Substrate) []Substrate {
	items := make([]Substrate, 0, len(substrates))
	for _, substrate := range substrates {
		items = append(items, substrate)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Kind < items[j].Kind
	})
	return items
}

func sortedAliases(aliases map[string]AliasLease) []AliasLease {
	items := make([]AliasLease, 0, len(aliases))
	for _, alias := range aliases {
		items = append(items, alias)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Host != items[j].Host {
			return items[i].Host < items[j].Host
		}
		return items[i].SessionID < items[j].SessionID
	})
	return items
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		key = sanitizeLabel(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		copied[key] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func copyRawStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		copied[key] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func ownersForSubstrate(kind string, pids map[string]int, requested map[string]Owner, current *Substrate) map[string]Owner {
	owners := copyOwnerMap(requested)
	if len(pids) == 0 {
		if len(owners) == 0 {
			return nil
		}
		return owners
	}
	for name, pid := range pids {
		if pid <= 0 {
			continue
		}
		owner := owners[name]
		if owner.PID == 0 && current != nil {
			if existing := current.Owners[name]; existing.PID == pid {
				owner = existing
			}
		}
		owner = OwnerFromRequest(pid, owner, "scenery substrate "+kind+"."+name)
		if owner.PID > 0 {
			owners[name] = owner
		}
	}
	if len(owners) == 0 {
		return nil
	}
	return owners
}

func copyOwnerMap(values map[string]Owner) map[string]Owner {
	if len(values) == 0 {
		return map[string]Owner{}
	}
	copied := make(map[string]Owner, len(values))
	for key, value := range values {
		key = sanitizeLabel(key)
		if key == "" {
			continue
		}
		copied[key] = value
	}
	return copied
}

func copyIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]int, len(values))
	for key, value := range values {
		key = sanitizeLabel(key)
		if key == "" || value <= 0 {
			continue
		}
		copied[key] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func leasesForSubstrate(requested map[string]SubstrateLease, current *Substrate) map[string]SubstrateLease {
	if requested == nil && current != nil {
		return copySubstrateLeaseMap(current.Leases)
	}
	return copySubstrateLeaseMap(requested)
}

func copySubstrateLeaseMap(values map[string]SubstrateLease) map[string]SubstrateLease {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]SubstrateLease, len(values))
	for key, value := range values {
		sessionID := strings.TrimSpace(firstNonEmpty(value.SessionID, key))
		if sessionID == "" {
			continue
		}
		value.SessionID = sessionID
		value.AppRoot = strings.TrimSpace(value.AppRoot)
		value.Route = sanitizeLabel(value.Route)
		value.URL = strings.TrimSpace(value.URL)
		if value.OwnerPID == 0 && value.Owner.PID > 0 {
			value.OwnerPID = value.Owner.PID
		}
		if value.Owner.PID == 0 && value.OwnerPID > 0 {
			value.Owner = OwnerFromRequest(value.OwnerPID, value.Owner, "scenery substrate lease")
		}
		if value.CreatedAt.IsZero() {
			value.CreatedAt = value.UpdatedAt
		}
		if value.UpdatedAt.IsZero() {
			value.UpdatedAt = value.CreatedAt
		}
		copied[sessionID] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func componentExitsForSubstrate(status string, requested map[string]SubstrateExit, current *Substrate) map[string]SubstrateExit {
	if status == "ready" {
		return copySubstrateExitMap(requested)
	}
	merged := map[string]SubstrateExit{}
	if current != nil {
		for key, value := range current.ComponentExits {
			key = sanitizeLabel(key)
			if key != "" {
				merged[key] = value
			}
		}
	}
	for key, value := range requested {
		key = sanitizeLabel(key)
		if key == "" {
			continue
		}
		value.Component = firstNonEmpty(value.Component, key)
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func copySubstrateExitMap(values map[string]SubstrateExit) map[string]SubstrateExit {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]SubstrateExit, len(values))
	for key, value := range values {
		key = sanitizeLabel(key)
		if key == "" {
			continue
		}
		value.Component = firstNonEmpty(value.Component, key)
		copied[key] = value
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func copySubstrateExit(value *SubstrateExit) *SubstrateExit {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func sortedSessions(sessions map[string]Session) []Session {
	items := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, session)
	}
	sortSessions(items)
	return items
}

func sortSessions(items []Session) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].SessionID < items[j].SessionID
	})
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm, atomicfile.Options{SyncFile: true, SyncDir: true})
}
