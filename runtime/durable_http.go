package runtime

import (
	"encoding/json"
	"net/http"
	"strings"

	"scenery.sh/internal/durable/store"
)

type durableLeaseRequest struct {
	WorkerID string `json:"worker_id"`
	LeaseID  string `json:"lease_id,omitempty"`
}

type durableLeaseResponse struct {
	Leased   bool            `json:"leased"`
	Service  string          `json:"service,omitempty"`
	WorkerID string          `json:"worker_id,omitempty"`
	LeaseID  string          `json:"lease_id,omitempty"`
	Job      *durableHTTPJob `json:"job,omitempty"`
}

type durableHTTPJob struct {
	ID         string          `json:"id"`
	TaskName   string          `json:"task_name"`
	Attempt    int             `json:"attempt"`
	InputCodec string          `json:"input_codec"`
	Input      json.RawMessage `json:"input,omitempty"`
}

type durableLeaseActionRequest struct {
	WorkerID string          `json:"worker_id"`
	LeaseID  string          `json:"lease_id"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func durableHTTPConfigured() bool {
	activeDurableStores.mu.RLock()
	defer activeDurableStores.mu.RUnlock()
	return len(activeDurableStores.stores) > 0
}

func (s *server) registerDurableRoutes() {
	registerRoute(s.public, "/__scenery/durable/v1/:service/lease", []string{http.MethodPost}, s.handleDurableLease)
	registerRoute(s.public, "/__scenery/durable/v1/:service/jobs/:job/heartbeat", []string{http.MethodPost}, s.handleDurableHeartbeat)
	registerRoute(s.public, "/__scenery/durable/v1/:service/jobs/:job/complete", []string{http.MethodPost}, s.handleDurableComplete)
	registerRoute(s.public, "/__scenery/durable/v1/:service/jobs/:job/fail", []string{http.MethodPost}, s.handleDurableFail)
}

func (s *server) handleDurableLease(w http.ResponseWriter, req *http.Request, params routeParams) {
	db, token, ok := authenticateDurableWorker(w, req, params.ByName("service"))
	if !ok {
		return
	}
	var body durableLeaseRequest
	if !decodeDurableJSON(w, req, &body) {
		return
	}
	body.WorkerID = strings.TrimSpace(body.WorkerID)
	if body.WorkerID == "" {
		durableHTTPError(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	leaseID := strings.TrimSpace(body.LeaseID)
	if leaseID == "" {
		var err error
		leaseID, err = newDurableID("lease_")
		if err != nil {
			durableHTTPError(w, http.StatusInternalServerError, "create lease id")
			return
		}
	}
	job, leased, err := db.LeaseReadyJobWithToken(req.Context(), body.WorkerID, leaseID, token.TokenHash)
	if err != nil {
		durableHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := durableLeaseResponse{Leased: leased, Service: db.Service, WorkerID: body.WorkerID}
	if leased {
		resp.LeaseID = leaseID
		resp.Job = &durableHTTPJob{
			ID:         job.ID,
			TaskName:   job.TaskName,
			Attempt:    job.Attempt,
			InputCodec: job.InputCodec,
			Input:      json.RawMessage(job.InputBlob),
		}
	}
	writeDurableJSON(w, http.StatusOK, resp)
}

func (s *server) handleDurableHeartbeat(w http.ResponseWriter, req *http.Request, params routeParams) {
	db, _, ok := authenticateDurableWorker(w, req, params.ByName("service"))
	if !ok {
		return
	}
	var body durableLeaseActionRequest
	if !decodeDurableJSON(w, req, &body) {
		return
	}
	if err := db.HeartbeatJob(req.Context(), params.ByName("job"), body.WorkerID, body.LeaseID); err != nil {
		durableHTTPError(w, http.StatusConflict, err.Error())
		return
	}
	writeDurableJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDurableComplete(w http.ResponseWriter, req *http.Request, params routeParams) {
	db, _, ok := authenticateDurableWorker(w, req, params.ByName("service"))
	if !ok {
		return
	}
	var body durableLeaseActionRequest
	if !decodeDurableJSON(w, req, &body) {
		return
	}
	result := []byte(body.Result)
	if len(result) == 0 {
		result = []byte(`{}`)
	}
	if err := db.CompleteLeasedJob(req.Context(), params.ByName("job"), body.WorkerID, body.LeaseID, result); err != nil {
		durableHTTPError(w, http.StatusConflict, err.Error())
		return
	}
	writeDurableJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDurableFail(w http.ResponseWriter, req *http.Request, params routeParams) {
	db, _, ok := authenticateDurableWorker(w, req, params.ByName("service"))
	if !ok {
		return
	}
	var body durableLeaseActionRequest
	if !decodeDurableJSON(w, req, &body) {
		return
	}
	msg := strings.TrimSpace(body.Error)
	if msg == "" {
		msg = "durable worker failed job"
	}
	if err := db.FailLeasedJob(req.Context(), params.ByName("job"), body.WorkerID, body.LeaseID, []byte(msg)); err != nil {
		durableHTTPError(w, http.StatusConflict, err.Error())
		return
	}
	writeDurableJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func authenticateDurableWorker(w http.ResponseWriter, req *http.Request, service string) (*store.Store, store.WorkerToken, bool) {
	service, err := store.NormalizeServiceName(service)
	if err != nil {
		durableHTTPError(w, http.StatusBadRequest, err.Error())
		return nil, store.WorkerToken{}, false
	}
	activeDurableStores.mu.RLock()
	db := activeDurableStores.stores[service]
	activeDurableStores.mu.RUnlock()
	if db == nil {
		durableHTTPError(w, http.StatusNotFound, "durable service not found")
		return nil, store.WorkerToken{}, false
	}
	secret := bearerToken(req.Header.Get("Authorization"))
	token, ok, err := db.AuthenticateWorkerToken(req.Context(), secret)
	if err != nil {
		durableHTTPError(w, http.StatusInternalServerError, err.Error())
		return nil, store.WorkerToken{}, false
	}
	if !ok {
		durableHTTPError(w, http.StatusUnauthorized, "durable worker token is invalid")
		return nil, store.WorkerToken{}, false
	}
	return db, token, true
}

func bearerToken(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < len("Bearer ") || !strings.EqualFold(value[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[len("Bearer "):])
}

func decodeDurableJSON(w http.ResponseWriter, req *http.Request, dst any) bool {
	if err := json.NewDecoder(req.Body).Decode(dst); err != nil {
		durableHTTPError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeDurableJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func durableHTTPError(w http.ResponseWriter, status int, message string) {
	writeDurableJSON(w, status, map[string]string{"error": message})
}
