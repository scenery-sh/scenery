package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	path     string
	router   string
	mu       sync.Mutex
	sessions map[string]Session
}

type registryFile struct {
	Sessions []Session `json:"sessions"`
}

func OpenRegistry(path, routerAddr string) (*Registry, error) {
	r := &Registry{
		path:     path,
		router:   routerAddr,
		sessions: make(map[string]Session),
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Upsert(req RegisterRequest) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessionID := SessionID(req.AppRoot, req.Branch)
	var existing *Session
	if current, ok := r.sessions[sessionID]; ok {
		existing = &current
	}
	session, err := NewSession(req, r.router, existing)
	if err != nil {
		return Session{}, err
	}
	r.sessions[session.SessionID] = session
	if err := r.saveLocked(); err != nil {
		return Session{}, err
	}
	if err := WriteManifest(session); err != nil {
		return Session{}, err
	}
	return session, nil
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
	return nil
}

func (r *Registry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registryFile{Sessions: sortedSessions(r.sessions)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(r.path, data, 0o644)
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
		if items[i].Status == items[j].Status {
			return items[i].SessionID < items[j].SessionID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
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
