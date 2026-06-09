package neonselfhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultPageserverHTTPPort = 55434
	defaultPGVersion          = 16
)

type localCellState struct {
	Root  string         `json:"root"`
	Ports map[string]int `json:"ports"`
}

func ensureBackendIDs(state *BackendState, branch *BackendBranch, opts branchActionOptions) {
	if strings.TrimSpace(state.TenantID) == "" {
		state.TenantID = stableHexID("tenant:" + firstNonEmpty(opts.Project, branch.Project, "onlava"))
	}
	if strings.TrimSpace(branch.ParentTimelineID) == "" || !looksLikeHexID(branch.ParentTimelineID) {
		branch.ParentTimelineID = resolveParentTimelineID(*state, opts, *branch)
	}
	if strings.TrimSpace(branch.TimelineID) == "" || !looksLikeHexID(branch.TimelineID) {
		branch.TimelineID = stableHexID("timeline:" + firstNonEmpty(opts.Project, branch.Project, "onlava") + ":" + firstNonEmpty(opts.BranchID, branch.Branch))
	}
}

func resolveParentTimelineID(state BackendState, opts branchActionOptions, branch BackendBranch) string {
	parentBranch := firstNonEmpty(opts.ParentBranch, "main")
	project := firstNonEmpty(opts.Project, branch.Project, "onlava")
	if parent, ok := findReadyParentBackendBranch(state, opts.BranchID, project, parentBranch); ok {
		return parent.TimelineID
	}
	return stableParentTimelineID(project, parentBranch)
}

func stableParentTimelineID(project, parentBranch string) string {
	return stableHexID("parent:" + firstNonEmpty(project, "onlava") + ":" + firstNonEmpty(parentBranch, "main"))
}

func findReadyParentBackendBranch(state BackendState, currentBranchID, project, parentBranch string) (BackendBranch, bool) {
	keys := make([]string, 0, len(state.Branches))
	for id := range state.Branches {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for priority := 0; priority < 3; priority++ {
		for _, id := range keys {
			if id == currentBranchID {
				continue
			}
			branch := state.Branches[id]
			if !candidateParentBranchMatches(branch, project, parentBranch, priority) {
				continue
			}
			if branch.Status != "ready" || !looksLikeHexID(branch.TimelineID) {
				continue
			}
			return branch, true
		}
	}
	return BackendBranch{}, false
}

func candidateParentBranchMatches(branch BackendBranch, project, parentBranch string, priority int) bool {
	if branch.Project != "" && project != "" && branch.Project != project {
		return false
	}
	branchName := strings.TrimSpace(branch.Branch)
	endpointID := strings.TrimSpace(branch.EndpointID)
	parentBranch = strings.TrimSpace(parentBranch)
	parentID := safeIdentifier(parentBranch)
	switch priority {
	case 0:
		return branchName == parentBranch
	case 1:
		return endpointID == parentID
	default:
		return strings.HasSuffix(branchName, "/"+parentBranch) || strings.HasSuffix(endpointID, "-"+parentID)
	}
}

func ensurePageserverBackend(ctx context.Context, root string, state *BackendState, branch *BackendBranch) (bool, string, error) {
	baseURL, ok, message := pageserverBaseURL(root)
	if !ok {
		return false, message, nil
	}
	if err := ensurePageserverTenant(ctx, baseURL, state.TenantID); err != nil {
		return false, "", err
	}
	if err := ensurePageserverTimeline(ctx, baseURL, state.TenantID, branch.ParentTimelineID, "", "", state.DefaultPGVersion); err != nil {
		return false, "", fmt.Errorf("ensure parent timeline: %w", err)
	}
	if err := ensurePageserverTimeline(ctx, baseURL, state.TenantID, branch.TimelineID, branch.ParentTimelineID, "", state.DefaultPGVersion); err != nil {
		return false, "", fmt.Errorf("ensure branch timeline: %w", err)
	}
	return true, fmt.Sprintf("neon-selfhost-driver ensured tenant %s and timeline %s for %q; branch compute is starting", state.TenantID, branch.TimelineID, branch.Branch), nil
}

func pageserverBaseURL(root string) (string, bool, string) {
	port := defaultPageserverHTTPPort
	if cell, ok, err := readLocalCellState(root); err == nil && ok {
		if configured := cell.Ports["pageserver_http"]; configured > 0 {
			port = configured
		}
	} else if err != nil {
		return "", false, fmt.Sprintf("neon-selfhost-driver could not read cell.json: %v", err)
	}
	address := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
	if err != nil {
		return "", false, fmt.Sprintf("neon-selfhost-driver pageserver HTTP endpoint %s is not reachable yet", address)
	}
	_ = conn.Close()
	return "http://" + address, true, ""
}

func readLocalCellState(root string) (localCellState, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "cell.json"))
	if os.IsNotExist(err) {
		return localCellState{}, false, nil
	}
	if err != nil {
		return localCellState{}, false, err
	}
	var state localCellState
	if err := json.Unmarshal(data, &state); err != nil {
		return localCellState{}, false, fmt.Errorf("parse cell.json: %w", err)
	}
	return state, true, nil
}

func ensurePageserverTenant(ctx context.Context, baseURL, tenantID string) error {
	body := map[string]any{
		"mode":        "AttachedSingle",
		"generation":  1,
		"tenant_conf": map[string]any{},
	}
	status, response, err := pageserverJSON(ctx, http.MethodPut, baseURL+"/v1/tenant/"+tenantID+"/location_config", body)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("pageserver tenant create returned HTTP %d: %s", status, strings.TrimSpace(string(response)))
}

func ensurePageserverTimeline(ctx context.Context, baseURL, tenantID, timelineID, ancestorTimelineID, ancestorStartLSN string, pgVersion int) error {
	if pageserverTimelineExists(ctx, baseURL, tenantID, timelineID) {
		return nil
	}
	if pgVersion == 0 {
		pgVersion = defaultPGVersion
	}
	body := map[string]any{
		"new_timeline_id": timelineID,
		"pg_version":      pgVersion,
	}
	if strings.TrimSpace(ancestorTimelineID) != "" {
		body["ancestor_timeline_id"] = ancestorTimelineID
	}
	if strings.TrimSpace(ancestorStartLSN) != "" {
		body["ancestor_start_lsn"] = ancestorStartLSN
	}
	status, response, err := pageserverJSON(ctx, http.MethodPost, baseURL+"/v1/tenant/"+tenantID+"/timeline", body)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	if pageserverTimelineExists(ctx, baseURL, tenantID, timelineID) {
		return nil
	}
	return fmt.Errorf("pageserver timeline create returned HTTP %d: %s", status, strings.TrimSpace(string(response)))
}

func pageserverLSNByTimestamp(ctx context.Context, baseURL, tenantID, timelineID, timestamp string) (string, error) {
	query := url.Values{}
	query.Set("timestamp", timestamp)
	query.Set("with_lease", "true")
	reqURL := baseURL + "/v1/tenant/" + tenantID + "/timeline/" + timelineID + "/get_lsn_by_timestamp?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("pageserver timestamp LSN lookup returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var payload struct {
		LSN string `json:"lsn"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parse pageserver timestamp LSN response: %w", err)
	}
	if strings.TrimSpace(payload.LSN) == "" {
		return "", fmt.Errorf("pageserver timestamp LSN response did not include lsn")
	}
	return payload.LSN, nil
}

func pageserverTimelineExists(ctx context.Context, baseURL, tenantID, timelineID string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/tenant/"+tenantID+"/timeline/"+timelineID, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func pageserverJSON(ctx context.Context, method, url string, body any) (int, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, response, nil
}

func stableHexID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func looksLikeHexID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		return false
	}
	return true
}
