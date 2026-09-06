package generate

import (
	"path/filepath"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

type ClientCoverage struct {
	Target   string `json:"target"`
	Bindings int    `json:"bindings"`
	Message  string `json:"message,omitempty"`
}

// ClientCoverageFor describes the same selection used to render fetch clients.
func ClientCoverageFor(result *compiler.Result, selector string) []ClientCoverage {
	coverage := []ClientCoverage{}
	if result == nil || result.Manifest == nil {
		return coverage
	}
	resources := append(append([]Resource(nil), result.Manifest.Resources...), result.FrameworkResources...)
	for _, target := range typescriptTargets(result.Manifest.Resources, selector) {
		item := ClientCoverage{Target: target.Address, Bindings: len(publicHTTPBindings(resources, target))}
		if item.Bindings == 0 && len(reachableAssistantSurfaces(resources, target)) == 0 {
			item.Message = "no exported HTTP bindings selected; check package operation exports and the target's gateways/include selectors"
		}
		coverage = append(coverage, item)
	}
	return coverage
}

type EditorWorkspaceReport struct {
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	WorkFile string `json:"work_file,omitempty"`
}

func editorWorkspaceSkipReason(result *compiler.Result) string {
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return "invalid_contract"
	}
	root, err := filepath.Abs(result.Root)
	if err != nil {
		return "invalid_root"
	}
	frameworkRoot, err := filepath.Abs(app.RepoRoot())
	if err == nil && root != frameworkRoot && pathWithin(frameworkRoot, root) {
		return "scenery_repository_fixture"
	}
	return ""
}

// DescribeEditorWorkspace exposes intentional no-ops without changing ownership.
func DescribeEditorWorkspace(result *compiler.Result, check bool) EditorWorkspaceReport {
	if reason := editorWorkspaceSkipReason(result); reason != "" {
		return EditorWorkspaceReport{Status: "skipped", Reason: reason}
	}
	status := generateapi.InspectEditorWorkspace(result.Root)
	if status.Conflict {
		return EditorWorkspaceReport{Status: "conflict", Reason: status.Message, WorkFile: status.WorkFile}
	}
	if status.Managed {
		return EditorWorkspaceReport{Status: "managed", WorkFile: status.WorkFile}
	}
	if check {
		return EditorWorkspaceReport{Status: "not_requested", Reason: "check_only", WorkFile: status.WorkFile}
	}
	return EditorWorkspaceReport{Status: "skipped", Reason: "no_go_contracts", WorkFile: status.WorkFile}
}
