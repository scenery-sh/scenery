package agent

import (
	"strings"
	"time"
)

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
			if !force && !aliasLeaseOwnerStale(existing, r.verifyOwner) {
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
		if !force && !sessionDomainHostOwnerStale(other, r.verifyOwner) {
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
func sessionDomainHostOwnerStale(session Session, verifyOwner func(Owner) error) bool {
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
	return verifyOwner(owner) != nil
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

func aliasLeaseOwnerStale(alias AliasLease, verifyOwner func(Owner) error) bool {
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
	return verifyOwner(owner) != nil
}

func (r *Registry) verifyOwner(owner Owner) error {
	if r != nil && r.ownerVerifier != nil {
		return r.ownerVerifier(owner)
	}
	return VerifyOwner(owner)
}

func normalizeAliasLease(alias AliasLease) AliasLease {
	alias.Host = normalizeRouteHost(alias.Host)
	alias.Route = normalizeAliasRoute(alias.Route)
	return alias
}

func normalizeAliasRoute(route string) string {
	return sanitizeLabel(route)
}
