package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	path             string
	router           string
	scheme           string
	mu               sync.Mutex
	sessions         map[string]Session
	substrates       map[string]Substrate
	currentByAppRoot map[string]string
}

type registryFile struct {
	Sessions         []Session         `json:"sessions"`
	Substrates       []Substrate       `json:"substrates,omitempty"`
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
	owner = OwnerFromRequest(ownerPID, owner, "onlava substrate")
	pids := copyIntMap(req.PIDs)
	owners := ownersForSubstrate(kind, pids, req.Owners, current)
	substrate := Substrate{
		SchemaVersion: SubstrateSchemaVersion,
		Kind:          kind,
		Status:        status,
		OwnerPID:      ownerPID,
		Owner:         owner,
		PIDs:          pids,
		Owners:        owners,
		URLs:          copyStringMap(req.URLs),
		Endpoints:     copyStringMap(req.Endpoints),
		CreatedAt:     createdAt,
		UpdatedAt:     now,
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
		return Session{}, errors.New("session is owned by another live onlava dev process")
	}
	r.sessions[session.SessionID] = session
	r.currentByAppRoot[filepath.Clean(session.AppRoot)] = session.SessionID
	if err := r.saveLocked(); err != nil {
		return Session{}, err
	}
	if err := WriteManifest(session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func requestMayClaimSession(req RegisterRequest, existing, next Session) bool {
	requestPID := firstPositive(req.Owner.PID, req.OwnerPID, next.Owner.PID, next.OwnerPID)
	existingPID := firstPositive(existing.Owner.PID, existing.OwnerPID)
	if requestPID <= 0 || existingPID <= 0 || requestPID == existingPID {
		return true
	}
	if req.ClaimOwner {
		return true
	}
	owner := existing.Owner
	if owner.PID <= 0 {
		owner.PID = existing.OwnerPID
	}
	if VerifyOwner(owner) != nil {
		return true
	}
	return false
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
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[strings.TrimSpace(id)]
	if !ok {
		return Session{}, false, nil
	}
	delete(r.sessions, id)
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
	if err := r.saveLocked(); err != nil {
		return Session{}, false, err
	}
	return session, true, nil
}

func (r *Registry) load() error {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
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
	for appRoot, sessionID := range file.CurrentByAppRoot {
		appRoot = filepath.Clean(strings.TrimSpace(appRoot))
		sessionID = strings.TrimSpace(sessionID)
		if appRoot != "" && sessionID != "" {
			r.currentByAppRoot[appRoot] = sessionID
		}
	}
	return nil
}

func (r *Registry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registryFile{
		Sessions:         sortedSessions(r.sessions),
		Substrates:       sortedSubstrates(r.substrates),
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
		owner = OwnerFromRequest(pid, owner, "onlava substrate "+kind+"."+name)
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
