package main

import ()

type assistantInitResponse struct {
	cliPayloadIdentity
	Assistant                  string              `json:"assistant"`
	Address                    string              `json:"address"`
	MCPServer                  string              `json:"mcp_server"`
	Client                     string              `json:"client"`
	Source                     string              `json:"source"`
	Package                    string              `json:"package"`
	PackageLock                string              `json:"package_lock"`
	EvalDirectory              string              `json:"eval_directory"`
	DryRun                     bool                `json:"dry_run"`
	Applied                    bool                `json:"applied"`
	Idempotent                 bool                `json:"idempotent"`
	Created                    []string            `json:"created"`
	Preserved                  []string            `json:"preserved"`
	PlanID                     string              `json:"plan_id,omitempty"`
	BaseWorkspaceRevision      string              `json:"base_workspace_revision"`
	PredictedWorkspaceRevision string              `json:"predicted_workspace_revision"`
	ContractRevision           string              `json:"contract_revision"`
	Files                      []assistantInitFile `json:"files"`
}

type assistantInitFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
