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
}

type dashboardControlPlaneClient struct {
	endpoint  string
	token     string
	sessionID string
	http      *http.Client
}

type dashboardControlPlaneRequest struct {
	SessionID string             `json:"session_id,omitempty"`
	UpsertApp *devdash.AppRecord `json:"upsert_app,omitempty"`
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = req.Body.Close() }()
	var payload dashboardControlPlaneRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2<<20)).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessionID := payload.SessionID
	if payload.UpsertApp != nil && sessionID == "" {
		sessionID = payload.UpsertApp.SessionID
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
	w.WriteHeader(http.StatusNoContent)
}
