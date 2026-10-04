package devdash

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type StoredApp struct {
	RouteID             string            `json:"route_id,omitempty"`
	ID                  string            `json:"id"`
	BaseAppID           string            `json:"base_app_id,omitempty"`
	RuntimeAppID        string            `json:"runtime_app_id,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	Name                string            `json:"name,omitempty"`
	Root                string            `json:"root,omitempty"`
	ListenAddr          string            `json:"listen_addr,omitempty"`
	Routes              map[string]string `json:"routes,omitempty"`
	Aliases             map[string]string `json:"aliases,omitempty"`
	Offline             bool              `json:"offline,omitempty"`
	Running             bool              `json:"running,omitempty"`
	SessionStatus       string            `json:"session_status,omitempty"`
	SessionStatusReason string            `json:"session_status_reason,omitempty"`
	Compiling           bool              `json:"compiling,omitempty"`
	CompileError        string            `json:"compile_error,omitempty"`
	BuildBlock          *BuildBlock       `json:"build_block,omitempty"`
	PID                 string            `json:"pid,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at,omitempty"`
	MetadataRef         string            `json:"metadata_ref,omitempty"`
	MetadataHash        string            `json:"metadata_hash,omitempty"`
	APIEncodingRef      string            `json:"api_encoding_ref,omitempty"`
	APIEncodingHash     string            `json:"api_encoding_hash,omitempty"`
	AppRevision         string            `json:"app_revision,omitempty"`
}

type StoredAppSession struct {
	RouteID             string            `json:"route_id,omitempty"`
	ID                  string            `json:"id"`
	BaseAppID           string            `json:"base_app_id,omitempty"`
	RuntimeAppID        string            `json:"runtime_app_id,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	Name                string            `json:"name,omitempty"`
	Root                string            `json:"root,omitempty"`
	ListenAddr          string            `json:"listen_addr,omitempty"`
	Routes              map[string]string `json:"routes,omitempty"`
	Aliases             map[string]string `json:"aliases,omitempty"`
	Offline             bool              `json:"offline,omitempty"`
	Running             bool              `json:"running,omitempty"`
	SessionStatus       string            `json:"session_status,omitempty"`
	SessionStatusReason string            `json:"session_status_reason,omitempty"`
	Compiling           bool              `json:"compiling,omitempty"`
	CompileError        string            `json:"compile_error,omitempty"`
	BuildBlock          *BuildBlock       `json:"build_block,omitempty"`
	PID                 string            `json:"pid,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at,omitempty"`
	MetadataRef         string            `json:"metadata_ref,omitempty"`
	MetadataHash        string            `json:"metadata_hash,omitempty"`
	APIEncodingRef      string            `json:"api_encoding_ref,omitempty"`
	APIEncodingHash     string            `json:"api_encoding_hash,omitempty"`
	AppRevision         string            `json:"app_revision,omitempty"`
}

type StoredAppModelRef struct {
	Ref         string    `json:"ref"`
	Kind        string    `json:"kind"`
	Hash        string    `json:"hash"`
	AppID       string    `json:"app_id"`
	Root        string    `json:"root,omitempty"`
	AppRevision string    `json:"app_revision,omitempty"`
	Path        string    `json:"path,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

func appSessionRecordKey(app AppRecord) string {
	if app.RouteID != "" {
		return app.RouteID
	}
	if app.SessionID != "" {
		return app.SessionID
	}
	return app.ID
}

func normalizeAppRecord(app AppRecord) AppRecord {
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = time.Now().UTC()
	}
	if len(app.Metadata) == 0 {
		app.Metadata = json.RawMessage(`{}`)
	}
	if len(app.APIEncoding) == 0 {
		app.APIEncoding = json.RawMessage(`{}`)
	}
	return app
}

func (s *Store) splitAppRecordForStore(app AppRecord) (StoredApp, StoredAppSession, []StoredAppModelRef, error) {
	appRevision := appRevisionFromMetadata(app.Metadata)
	metadataRef, err := s.putAppModelBlob("metadata", app.ID, app.Root, appRevision, app.Metadata)
	if err != nil {
		return StoredApp{}, StoredAppSession{}, nil, err
	}
	apiEncodingRef, err := s.putAppModelBlob("api-encoding", app.ID, app.Root, appRevision, app.APIEncoding)
	if err != nil {
		return StoredApp{}, StoredAppSession{}, nil, err
	}
	stored := storedAppFromAppRecord(app)
	session := storedAppSessionFromAppRecord(app)
	applyAppModelRefsToStoredApp(&stored, appRevision, metadataRef, apiEncodingRef)
	applyAppModelRefsToStoredAppSession(&session, appRevision, metadataRef, apiEncodingRef)
	refs := make([]StoredAppModelRef, 0, 2)
	if metadataRef.Ref != "" {
		refs = append(refs, metadataRef)
	}
	if apiEncodingRef.Ref != "" {
		refs = append(refs, apiEncodingRef)
	}
	return stored, session, refs, nil
}

func storedAppFromAppRecord(app AppRecord) StoredApp {
	return StoredApp{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		BuildBlock:          app.BuildBlock,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func storedAppSessionFromAppRecord(app AppRecord) StoredAppSession {
	return StoredAppSession{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		BuildBlock:          app.BuildBlock,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func (app StoredApp) toAppRecord() AppRecord {
	return AppRecord{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		BuildBlock:          app.BuildBlock,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func (session StoredAppSession) toAppRecord() AppRecord {
	return AppRecord{
		RouteID:             session.RouteID,
		ID:                  session.ID,
		BaseAppID:           session.BaseAppID,
		RuntimeAppID:        session.RuntimeAppID,
		SessionID:           session.SessionID,
		Name:                session.Name,
		Root:                session.Root,
		ListenAddr:          session.ListenAddr,
		Routes:              maps.Clone(session.Routes),
		Aliases:             maps.Clone(session.Aliases),
		Offline:             session.Offline,
		Running:             session.Running,
		SessionStatus:       session.SessionStatus,
		SessionStatusReason: session.SessionStatusReason,
		Compiling:           session.Compiling,
		CompileError:        session.CompileError,
		BuildBlock:          session.BuildBlock,
		PID:                 session.PID,
		UpdatedAt:           session.UpdatedAt,
	}
}

func applyAppModelRefsToStoredApp(app *StoredApp, appRevision string, metadataRef, apiEncodingRef StoredAppModelRef) {
	app.AppRevision = firstNonEmptyString(app.AppRevision, appRevision)
	if metadataRef.Ref != "" {
		app.MetadataRef = metadataRef.Ref
		app.MetadataHash = metadataRef.Hash
	}
	if apiEncodingRef.Ref != "" {
		app.APIEncodingRef = apiEncodingRef.Ref
		app.APIEncodingHash = apiEncodingRef.Hash
	}
}

func applyAppModelRefsToStoredAppSession(session *StoredAppSession, appRevision string, metadataRef, apiEncodingRef StoredAppModelRef) {
	session.AppRevision = firstNonEmptyString(session.AppRevision, appRevision)
	if metadataRef.Ref != "" {
		session.MetadataRef = metadataRef.Ref
		session.MetadataHash = metadataRef.Hash
	}
	if apiEncodingRef.Ref != "" {
		session.APIEncodingRef = apiEncodingRef.Ref
		session.APIEncodingHash = apiEncodingRef.Hash
	}
}

func (s *Store) hydrateStoredApp(state *storeState, stored StoredApp) (AppRecord, error) {
	app := stored.toAppRecord()
	var err error
	app.Metadata, err = s.readAppModelBlob(stored.MetadataRef)
	if err != nil {
		return AppRecord{}, err
	}
	app.APIEncoding, err = s.readAppModelBlob(stored.APIEncodingRef)
	if err != nil {
		return AppRecord{}, err
	}
	return normalizeAppRecord(app), nil
}

func (s *Store) hydrateStoredAppSession(state *storeState, stored StoredAppSession) (AppRecord, error) {
	app := stored.toAppRecord()
	var err error
	app.Metadata, err = s.readAppModelBlob(stored.MetadataRef)
	if err != nil {
		return AppRecord{}, err
	}
	app.APIEncoding, err = s.readAppModelBlob(stored.APIEncodingRef)
	if err != nil {
		return AppRecord{}, err
	}
	return normalizeAppRecord(app), nil
}

func (s *Store) UpsertApp(ctx context.Context, app AppRecord) error {
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = time.Now().UTC()
	}
	return s.withState(ctx, true, func(state *storeState) error {
		appRecord, sessionRecord, refs, err := s.splitAppRecordForStore(app)
		if err != nil {
			return err
		}
		appRecord.RouteID = appRecord.ID
		state.Apps[app.ID] = appRecord
		sessionRecord.RouteID = appSessionRecordKey(app)
		state.AppSessions[sessionRecord.RouteID] = sessionRecord
		for _, ref := range refs {
			if ref.Ref != "" {
				state.AppModelRefs[ref.Ref] = ref
			}
		}
		return nil
	})
}

func (s *Store) ListApps(ctx context.Context) ([]AppRecord, error) {
	var apps []AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, stored := range state.Apps {
			app := stored.toAppRecord()
			app.RouteID = app.ID
			app.Offline = !app.Running
			app = normalizeAppRecord(app)
			apps = append(apps, app)
		}
		sort.SliceStable(apps, func(i, j int) bool {
			if apps[i].Running != apps[j].Running {
				return apps[i].Running
			}
			return apps[i].Name < apps[j].Name
		})
		return nil
	})
	return apps, err
}

func (s *Store) ListAppSessions(ctx context.Context) ([]AppRecord, error) {
	var apps []AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		for routeID, stored := range state.AppSessions {
			app := stored.toAppRecord()
			app.RouteID = routeID
			app.Offline = !app.Running
			app = normalizeAppRecord(app)
			apps = append(apps, app)
		}
		sort.SliceStable(apps, func(i, j int) bool {
			if apps[i].Running != apps[j].Running {
				return apps[i].Running
			}
			if apps[i].Name != apps[j].Name {
				return apps[i].Name < apps[j].Name
			}
			if apps[i].SessionID != apps[j].SessionID {
				return apps[i].SessionID < apps[j].SessionID
			}
			return apps[i].UpdatedAt.After(apps[j].UpdatedAt)
		})
		return nil
	})
	return apps, err
}

func (s *Store) GetApp(ctx context.Context, appID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		found, ok := state.Apps[appID]
		if !ok {
			return sql.ErrNoRows
		}
		var err error
		app, err = s.hydrateStoredApp(state, found)
		if err != nil {
			return err
		}
		app.RouteID = app.ID
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func (s *Store) GetAppSession(ctx context.Context, routeID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		if found, ok := state.AppSessions[routeID]; ok {
			var err error
			app, err = s.hydrateStoredAppSession(state, found)
			if err != nil {
				return err
			}
			app.RouteID = routeID
			app.Offline = !app.Running
			return nil
		}
		var matches []AppRecord
		for key, candidate := range state.AppSessions {
			if candidate.SessionID == routeID {
				record, err := s.hydrateStoredAppSession(state, candidate)
				if err != nil {
					return err
				}
				record.RouteID = key
				matches = append(matches, record)
			}
		}
		if len(matches) == 0 {
			return sql.ErrNoRows
		}
		sortRunningUpdated(matches)
		app = matches[0]
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func (s *Store) GetAppForSession(ctx context.Context, appID, sessionID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		var matches []AppRecord
		for key, candidate := range state.AppSessions {
			if candidate.ID == appID && candidate.SessionID == sessionID {
				record, err := s.hydrateStoredAppSession(state, candidate)
				if err != nil {
					return err
				}
				record.RouteID = key
				matches = append(matches, record)
			}
		}
		if len(matches) == 0 {
			return sql.ErrNoRows
		}
		sortRunningUpdated(matches)
		app = matches[0]
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func sortRunningUpdated(apps []AppRecord) {
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].Running != apps[j].Running {
			return apps[i].Running
		}
		return apps[i].UpdatedAt.After(apps[j].UpdatedAt)
	})
}

func compactRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return value
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return append(json.RawMessage(nil), value...)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return append(json.RawMessage(nil), value...)
	}
	return normalized
}

func appRevisionFromMetadata(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var decoded struct {
		AppRevision string `json:"app_revision"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return ""
	}
	return decoded.AppRevision
}

func (s *Store) putAppModelBlob(kind, appID, root, appRevision string, value json.RawMessage) (StoredAppModelRef, error) {
	value = compactRawMessage(value)
	if isEmptyJSONValue(value) {
		return StoredAppModelRef{}, nil
	}
	hashBytes := sha256.Sum256(value)
	hash := hex.EncodeToString(hashBytes[:])
	ref := kind + ":sha256:" + hash
	blobDir := filepath.Join(filepath.Dir(s.path), "app-model", kind, "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return StoredAppModelRef{}, err
	}
	blobPath := filepath.Join(blobDir, hash+".json")
	if _, err := os.Stat(blobPath); errors.Is(err, os.ErrNotExist) {
		tmp, err := os.CreateTemp(blobDir, "."+hash+"-*.json")
		if err != nil {
			return StoredAppModelRef{}, err
		}
		tmpName := tmp.Name()
		ok := false
		defer func() {
			if !ok {
				_ = os.Remove(tmpName)
			}
		}()
		if _, err := tmp.Write(value); err != nil {
			_ = tmp.Close()
			return StoredAppModelRef{}, err
		}
		if _, err := tmp.Write([]byte("\n")); err != nil {
			_ = tmp.Close()
			return StoredAppModelRef{}, err
		}
		if err := tmp.Close(); err != nil {
			return StoredAppModelRef{}, err
		}
		if err := os.Rename(tmpName, blobPath); err != nil {
			return StoredAppModelRef{}, err
		}
		ok = true
	} else if err != nil {
		return StoredAppModelRef{}, err
	}
	rel, err := filepath.Rel(filepath.Dir(s.path), blobPath)
	if err != nil {
		rel = blobPath
	}
	return StoredAppModelRef{
		Ref:         ref,
		Kind:        kind,
		Hash:        hash,
		AppID:       appID,
		Root:        root,
		AppRevision: appRevision,
		Path:        rel,
		Bytes:       int64(len(value)),
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func (s *Store) readAppModelBlob(ref string) (json.RawMessage, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	kind, hash, ok := splitAppModelRef(ref)
	if !ok {
		return nil, fmt.Errorf("invalid app model ref %q", ref)
	}
	blobPath := filepath.Join(filepath.Dir(s.path), "app-model", kind, "sha256", hash+".json")
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, fmt.Errorf("read app model blob %s: %w", ref, err)
	}
	return compactRawMessage(data), nil
}

func splitAppModelRef(ref string) (string, string, bool) {
	kind, rest, ok := strings.Cut(ref, ":sha256:")
	if !ok || strings.TrimSpace(kind) == "" || len(rest) != sha256.Size*2 {
		return "", "", false
	}
	if _, err := hex.DecodeString(rest); err != nil {
		return "", "", false
	}
	return kind, rest, true
}

func isEmptyJSONValue(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("{}")) || bytes.Equal(value, []byte("[]")) {
		return true
	}
	return false
}
