package agent

// claimDomainHostLocked enforces single ownership of a path-mode dev domain
// host across sessions. A live verified owner keeps the host: the newcomer's
// manifest drops it and records the conflict. A provably stale owner loses
// the host to the newcomer; `force` (`--claim-aliases`) transfers it from a
// live owner.
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

// sessionDomainHostOwnerStale reports a stale host owner: a host owner is
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

func (r *Registry) verifyOwner(owner Owner) error {
	if r != nil && r.ownerVerifier != nil {
		return r.ownerVerifier(owner)
	}
	return VerifyOwner(owner)
}

func normalizeAliasRoute(route string) string {
	return sanitizeLabel(route)
}
