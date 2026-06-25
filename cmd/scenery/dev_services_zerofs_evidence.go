package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const managedDevFailureEvidenceSchema = "scenery.dev.failure.v1"

type managedDevFailureEvidence struct {
	SchemaVersion   string                           `json:"schema_version"`
	CreatedAt       string                           `json:"created_at"`
	Status          string                           `json:"status"`
	Phase           string                           `json:"phase"`
	Error           string                           `json:"error"`
	SuggestedAction string                           `json:"suggested_action,omitempty"`
	App             managedDevFailureApp             `json:"app"`
	Session         managedDevFailureSession         `json:"session"`
	Substrate       managedDevFailureZeroFSSubstrate `json:"substrate"`
}

type managedDevFailureApp struct {
	Root string `json:"root,omitempty"`
	ID   string `json:"id,omitempty"`
}

type managedDevFailureSession struct {
	Status       string `json:"status"`
	ID           string `json:"id,omitempty"`
	StateRoot    string `json:"state_root,omitempty"`
	BaseAppID    string `json:"base_app_id,omitempty"`
	RuntimeAppID string `json:"runtime_app_id,omitempty"`
}

type managedDevFailureZeroFSSubstrate struct {
	Kind          string `json:"kind"`
	Component     string `json:"component"`
	StorageCellID string `json:"storage_cell_id,omitempty"`
	Route         string `json:"route,omitempty"`
	Source        string `json:"source,omitempty"`
	PID           int    `json:"pid,omitempty"`
	ConfigPath    string `json:"config_path,omitempty"`
	LogPath       string `json:"log_path,omitempty"`
	NinePSocket   string `json:"ninep_socket,omitempty"`
	RPCSocket     string `json:"rpc_socket,omitempty"`
	WebUIAddr     string `json:"webui_addr,omitempty"`
	ProcessTail   string `json:"process_tail,omitempty"`
}

func managedZeroFSServiceFromPlan(root string, plan *managedZeroFSPlan, source string) *managedZeroFSService {
	if plan == nil {
		return &managedZeroFSService{AppRoot: root, Source: source}
	}
	return &managedZeroFSService{
		StorageCellID: plan.StorageCellID,
		Route:         plan.Route,
		WebUIAddr:     plan.WebUIListen,
		Source:        source,
		AppRoot:       root,
		LogPath:       plan.LogPath,
		ConfigPath:    plan.ConfigPath,
		NinePSocket:   plan.NinePSocket,
		RPCSocket:     plan.RPCSocket,
	}
}

func populateManagedZeroFSSessionContext(service *managedZeroFSService, root string, session *localagent.Session) {
	if service == nil {
		return
	}
	service.AppRoot = firstNonEmpty(service.AppRoot, root)
	if session == nil {
		return
	}
	service.AppRoot = firstNonEmpty(service.AppRoot, session.AppRoot)
	service.SessionID = firstNonEmpty(service.SessionID, session.SessionID)
	service.SessionStateRoot = firstNonEmpty(service.SessionStateRoot, session.StateRoot)
	service.BaseAppID = firstNonEmpty(service.BaseAppID, session.BaseAppID)
	service.RuntimeAppID = firstNonEmpty(service.RuntimeAppID, session.RuntimeAppID)
}

func writeManagedZeroFSFailureEvidence(root string, session *localagent.Session, service *managedZeroFSService, phase string, cause error, suggestedAction string) (string, error) {
	if service != nil {
		populateManagedZeroFSSessionContext(service, root, session)
	}
	path := managedZeroFSFailureEvidencePath(root, session, service, phase)
	if path == "" {
		return "", fmt.Errorf("missing app root or session state root")
	}
	payload := buildManagedZeroFSFailureEvidence(session, service, phase, cause, suggestedAction)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func managedZeroFSErrorWithEvidence(root string, session *localagent.Session, plan *managedZeroFSPlan, source, phase string, cause error, suggestedAction string) error {
	if cause == nil {
		return nil
	}
	service := managedZeroFSServiceFromPlan(root, plan, source)
	populateManagedZeroFSSessionContext(service, root, session)
	evidencePath, evidenceErr := writeManagedZeroFSFailureEvidence(root, session, service, phase, cause, suggestedAction)
	return fmt.Errorf("%w%s", cause, managedZeroFSEvidenceErrorSuffix(evidencePath, evidenceErr))
}

func managedZeroFSFailureEvidencePath(root string, session *localagent.Session, service *managedZeroFSService, phase string) string {
	filename := localagentLabel(firstNonEmpty(phase, "managed-zerofs-failure"))
	if filename == "" {
		filename = "managed-zerofs-failure"
	}
	filename += "-failure.json"
	if service != nil && strings.TrimSpace(service.SessionStateRoot) != "" {
		return filepath.Join(service.SessionStateRoot, "artifacts", filename)
	}
	if session != nil && strings.TrimSpace(session.StateRoot) != "" {
		return filepath.Join(session.StateRoot, "artifacts", filename)
	}
	appRoot := strings.TrimSpace(root)
	if appRoot == "" && service != nil {
		appRoot = strings.TrimSpace(service.AppRoot)
	}
	if appRoot == "" && session != nil {
		appRoot = strings.TrimSpace(session.AppRoot)
	}
	if appRoot == "" {
		return ""
	}
	return filepath.Join(appRoot, ".scenery", "evidence", filename)
}

func buildManagedZeroFSFailureEvidence(session *localagent.Session, service *managedZeroFSService, phase string, cause error, suggestedAction string) managedDevFailureEvidence {
	service = firstManagedZeroFSService(service)
	sessionStatus := "missing"
	if strings.Contains(strings.ToLower(phase), "preflight") && strings.TrimSpace(service.SessionID) == "" {
		sessionStatus = "not_created"
	} else if strings.TrimSpace(service.SessionID) != "" {
		sessionStatus = "active"
	}
	if session != nil && strings.TrimSpace(session.SessionID) != "" {
		sessionStatus = "active"
	}
	errText := ""
	if cause != nil {
		errText = cause.Error()
	}
	return managedDevFailureEvidence{
		SchemaVersion:   managedDevFailureEvidenceSchema,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          "failed",
		Phase:           firstNonEmpty(strings.TrimSpace(phase), "managed-zerofs.failure"),
		Error:           errText,
		SuggestedAction: strings.TrimSpace(suggestedAction),
		App: managedDevFailureApp{
			Root: service.AppRoot,
			ID:   service.BaseAppID,
		},
		Session: managedDevFailureSession{
			Status:       sessionStatus,
			ID:           service.SessionID,
			StateRoot:    service.SessionStateRoot,
			BaseAppID:    service.BaseAppID,
			RuntimeAppID: service.RuntimeAppID,
		},
		Substrate: managedDevFailureZeroFSSubstrate{
			Kind:          managedZeroFSSubstrateKind(service.StorageCellID),
			Component:     "zerofs",
			StorageCellID: service.StorageCellID,
			Route:         service.Route,
			Source:        service.Source,
			PID:           firstPositiveInt(service.PID(), 0),
			ConfigPath:    service.ConfigPath,
			LogPath:       service.LogPath,
			NinePSocket:   service.NinePSocket,
			RPCSocket:     service.RPCSocket,
			WebUIAddr:     service.WebUIAddr,
			ProcessTail:   managedZeroFSProcessTail(service),
		},
	}
}

func firstManagedZeroFSService(service *managedZeroFSService) *managedZeroFSService {
	if service != nil {
		return service
	}
	return &managedZeroFSService{}
}

func managedZeroFSProcessTail(service *managedZeroFSService) string {
	if service == nil || service.process == nil {
		return ""
	}
	return tailString(service.process.tailString(), 8192)
}

func managedZeroFSEvidenceErrorSuffix(path string, err error) string {
	if strings.TrimSpace(path) != "" {
		return "\nEvidence: " + path
	}
	if err != nil {
		return "\nEvidence: unavailable: " + err.Error()
	}
	return ""
}
