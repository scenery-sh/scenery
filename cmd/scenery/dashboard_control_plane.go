package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

const dashboardControlPlanePath = "/__scenery/control-plane"

type dashboardControlPlaneWriter interface {
	UpsertApp(context.Context, devdash.AppRecord) error
	WriteProcessEvent(context.Context, string, string, string, any) error
}

type localDashboardControlPlaneWriter struct {
	store *devdash.Store
}

func (w localDashboardControlPlaneWriter) UpsertApp(ctx context.Context, app devdash.AppRecord) error {
	if w.store == nil {
		return nil
	}
	return w.store.UpsertApp(ctx, app)
}

func (w localDashboardControlPlaneWriter) WriteProcessEvent(ctx context.Context, appID, sessionID, kind string, payload any) error {
	_ = sessionID
	if w.store == nil {
		return nil
	}
	return w.store.WriteProcessEvent(ctx, appID, kind, payload)
}

type dashboardControlPlaneClient struct {
	endpoint  string
	token     string
	sessionID string
	http      *http.Client
}

type dashboardControlPlaneRequest struct {
	SessionID    string                     `json:"session_id,omitempty"`
	UpsertApp    *devdash.AppRecord         `json:"upsert_app,omitempty"`
	ProcessEvent *dashboardProcessEventPost `json:"process_event,omitempty"`
}

type dashboardProcessEventPost struct {
	AppID       string          `json:"app_id"`
	SessionID   string          `json:"session_id,omitempty"`
	Kind        string          `json:"kind"`
	PayloadJSON json.RawMessage `json:"payload_json,omitempty"`
}

func newDashboardControlPlaneClient(ctx context.Context, agent *localagent.Client, session localagent.Session, token string) (*dashboardControlPlaneClient, error) {
	if agent == nil {
		return nil, errors.New("agent client is nil")
	}
	health, err := agent.Health(ctx)
	if err != nil {
		return nil, err
	}
	rawURL := controlPlaneURLForBackend(health.DashboardBackend)
	if rawURL == "" {
		return nil, errors.New("scenery agent dashboard backend is unavailable")
	}
	return &dashboardControlPlaneClient{
		endpoint:  rawURL,
		token:     token,
		sessionID: strings.TrimSpace(session.SessionID),
		http:      &http.Client{Timeout: time.Second},
	}, nil
}

func controlPlaneURLForBackend(backend localagent.Backend) string {
	network := strings.TrimSpace(backend.Network)
	addr := strings.TrimSpace(backend.Addr)
	if addr == "" || (network != "" && network != "tcp") {
		return ""
	}
	return "http://" + addr + dashboardControlPlanePath
}

func (c *dashboardControlPlaneClient) UpsertApp(ctx context.Context, app devdash.AppRecord) error {
	if c == nil {
		return nil
	}
	if app.SessionID == "" {
		app.SessionID = c.sessionID
	}
	return c.post(ctx, dashboardControlPlaneRequest{
		SessionID: firstNonEmpty(app.SessionID, c.sessionID),
		UpsertApp: &app,
	})
}

func (c *dashboardControlPlaneClient) WriteProcessEvent(ctx context.Context, appID, sessionID, kind string, payload any) error {
	if c == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sessionID = firstNonEmpty(sessionID, c.sessionID)
	return c.post(ctx, dashboardControlPlaneRequest{
		SessionID: sessionID,
		ProcessEvent: &dashboardProcessEventPost{
			AppID:       appID,
			SessionID:   sessionID,
			Kind:        kind,
			PayloadJSON: data,
		},
	})
}

func (c *dashboardControlPlaneClient) post(ctx context.Context, payload dashboardControlPlaneRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("dashboard control-plane write failed: %s", resp.Status)
	}
	return nil
}

func (s *dashboardServer) handleControlPlane(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer req.Body.Close()
	var payload dashboardControlPlaneRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2<<20)).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessionID := payload.SessionID
	if payload.UpsertApp != nil && sessionID == "" {
		sessionID = payload.UpsertApp.SessionID
	}
	if payload.ProcessEvent != nil && sessionID == "" {
		sessionID = payload.ProcessEvent.SessionID
	}
	auth := s.dashboardAuthorizeReport(req, devdash.ReportEnvelope{SessionID: sessionID})
	if !auth.Authorized {
		http.Error(w, firstNonEmpty(auth.Reason, "unauthorized"), http.StatusUnauthorized)
		return
	}
	store := s.dashboardStore()
	if store == nil {
		http.Error(w, "dashboard store unavailable", http.StatusServiceUnavailable)
		return
	}
	if payload.UpsertApp != nil {
		if payload.UpsertApp.SessionID == "" {
			payload.UpsertApp.SessionID = sessionID
		}
		if err := store.UpsertApp(req.Context(), *payload.UpsertApp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if payload.ProcessEvent != nil {
		event := payload.ProcessEvent
		var raw any
		if len(event.PayloadJSON) > 0 && json.Valid(event.PayloadJSON) {
			raw = event.PayloadJSON
		}
		if err := store.WriteProcessEvent(req.Context(), event.AppID, event.Kind, raw); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
