package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/symphony"
)

type symphonyRunDetail struct {
	Run    symphony.Run        `json:"run"`
	Events []symphony.RunEvent `json:"events"`
}

func (s *dashboardServer) dispatchSymphonyRPC(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if strings.HasPrefix(method, "symphony/run/") && method != "symphony/run/detail" {
		return nil, fmt.Errorf("%s is unavailable until dashboard runner auth is implemented", method)
	}
	appID, err := s.symphonyAppID(ctx, raw)
	if err != nil {
		return nil, err
	}
	store, err := s.dashboardSymphonyStore(ctx)
	if err != nil {
		return nil, err
	}
	switch method {
	case "symphony/state":
		return store.State(ctx, appID)
	case "symphony/task/create":
		var params struct {
			Input symphony.TaskInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return store.CreateTask(ctx, appID, params.Input)
	case "symphony/task/update":
		var params struct {
			ID    string             `json:"id"`
			Input symphony.TaskInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return store.UpdateTask(ctx, appID, params.ID, params.Input)
	case "symphony/task/move":
		var params struct {
			ID        string `json:"id"`
			StatusKey string `json:"status_key"`
			Index     int    `json:"index"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		if err := store.MoveTask(ctx, appID, params.ID, params.StatusKey, params.Index); err != nil {
			return nil, err
		}
		return store.State(ctx, appID)
	case "symphony/task/delete":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return true, store.DeleteTask(ctx, appID, params.ID)
	case "symphony/statuses/update":
		var params struct {
			Statuses []symphony.StatusUpdate `json:"statuses"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return store.UpdateStatuses(ctx, appID, params.Statuses)
	case "symphony/workflow/get":
		return store.Workflow(ctx, appID)
	case "symphony/workflow/update":
		var params struct {
			Input symphony.WorkflowInput `json:"input"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.Input.Mode) == "auto" {
			current, err := store.Workflow(ctx, appID)
			if err != nil {
				return nil, err
			}
			if current.Mode != "auto" {
				return nil, fmt.Errorf("symphony/workflow/update mode=auto is unavailable over unauthenticated dashboard RPC; run `scenery symphony auto --on --app-root <path>` locally")
			}
		}
		return store.UpdateWorkflow(ctx, appID, params.Input)
	case "symphony/run/detail":
		var params struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		run, err := store.Run(ctx, appID, params.RunID)
		if err != nil {
			return nil, err
		}
		if (run.DiffStat == "" || run.Diff == "") && symphonyWorkspacePathAllowed(run.WorkspacePath) {
			if diffStat, diff, err := collectSymphonyRunArtifacts(ctx, run.WorkspacePath); err == nil {
				if run.DiffStat == "" {
					run.DiffStat = diffStat
				}
				if run.Diff == "" {
					run.Diff = diff
				}
			}
		}
		events, err := store.RunEvents(ctx, appID, params.RunID)
		if err != nil {
			return nil, err
		}
		return symphonyRunDetail{Run: run, Events: events}, nil
	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

func (s *dashboardServer) dashboardSymphonyStore(ctx context.Context) (*symphony.Store, error) {
	if s == nil {
		return nil, fmt.Errorf("dashboard server unavailable")
	}
	s.symphonyMu.Lock()
	defer s.symphonyMu.Unlock()
	if s.symphonyStore != nil {
		return s.symphonyStore, nil
	}
	store, err := openDashboardSymphonyStore(ctx)
	if err != nil {
		return nil, err
	}
	s.symphonyStore = store
	return store, nil
}

func (s *dashboardServer) symphonyAppID(ctx context.Context, raw json.RawMessage) (string, error) {
	var params struct {
		AppID string `json:"app_id"`
	}
	_ = json.Unmarshal(raw, &params)
	requested := firstNonEmpty(params.AppID, s.dashboardActiveAppID())
	status, err := s.dashboardStatusFor(ctx, requested)
	if err != nil {
		return "", err
	}
	appID := strings.TrimSpace(status.BaseAppID)
	if appID == "" && strings.TrimSpace(status.SessionID) == "" {
		appID = strings.TrimSpace(status.AppID)
	}
	if appID == "" {
		return "", fmt.Errorf("symphony requires a stable app id for %q", requested)
	}
	return appID, nil
}

func openDashboardSymphonyStore(ctx context.Context) (*symphony.Store, error) {
	return symphony.Open(ctx, filepath.Join(symphonyCacheRoot(), "symphony.sqlite"))
}

func symphonyCacheRoot() string {
	if value := strings.TrimSpace(envpolicy.Get("SCENERY_DEV_CACHE_DIR")); value != "" {
		return value
	}
	if paths, err := localagent.DefaultPaths(); err == nil && strings.TrimSpace(paths.AgentDir) != "" {
		return filepath.Join(paths.AgentDir, "dashboard")
	}
	if dir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "scenery")
	}
	return filepath.Join(os.TempDir(), "scenery")
}

func symphonyWorkspacePathAllowed(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	root, err := filepath.Abs(filepath.Join(symphonyCacheRoot(), "workspaces"))
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
