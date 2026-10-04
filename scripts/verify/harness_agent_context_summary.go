package main

import (
	"path/filepath"
	"strings"

	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/repoinfo"
)

const harnessAgentContextSummaryKind = "scenery.agent_context.summary"

type harnessAgentContextSummary struct {
	cliPayloadIdentity
	Run              *harnessreport.ValidationRun `json:"run"`
	Mode             string                       `json:"mode"`
	RepoChecksPassed bool                         `json:"repo_checks_passed"`
	CurrentBranch    string                       `json:"current_branch,omitempty"`
	CurrentCommit    string                       `json:"current_commit,omitempty"`
	ChangedFileCount int                          `json:"changed_file_count"`
	ErrorCount       int                          `json:"error_count"`
	WarningCount     int                          `json:"warning_count"`
	FailingSteps     []string                     `json:"failing_steps"`
	AgentScopes      []string                     `json:"agent_scopes"`
	ActivePlans      []string                     `json:"active_plans"`
	Checks           []harnessAgentCheck          `json:"checks"`
	FullContextPath  string                       `json:"full_context_path"`
}

type harnessAgentCheck struct {
	Command      string `json:"command"`
	Status       string `json:"status"`
	EvidenceStep string `json:"evidence_step,omitempty"`
	Condition    string `json:"condition,omitempty"`
}

// Coverage is evidence from this exact run, never a claim about later edits or
// an independently executed check whose receipt is absent from the archive.
func buildHarnessAgentContextSummary(resp harnessSelfResponse, contextPack harnessAgentContext) harnessAgentContextSummary {
	stable := resp.Run != nil && resp.Run.InputsStable && resp.Run.InputRevision != "" && resp.Run.InputRevision == resp.Run.FinalInputRevision
	fullTests, fullVet := false, false
	for _, step := range resp.Steps {
		fullTests = fullTests || (step.OK && step.Name == "go tests" && strings.Join(step.Command, " ") == "go test -json ./...")
		fullVet = fullVet || (step.OK && step.Name == "go vet" && strings.Join(step.Command, " ") == "go vet ./...")
	}
	summary := harnessAgentContextSummary{
		cliPayloadIdentity: newCLIPayloadIdentity(harnessAgentContextSummaryKind),
		Run:                resp.Run, Mode: resp.Mode, RepoChecksPassed: resp.OK && stable,
		CurrentBranch: contextPack.CurrentBranch, CurrentCommit: contextPack.CurrentCommit,
		ChangedFileCount: len(contextPack.DirtyFiles),
		FailingSteps:     []string{}, AgentScopes: []string{}, ActivePlans: []string{}, Checks: []harnessAgentCheck{},
	}
	if resp.Run != nil {
		summary.FullContextPath = resp.Run.ArchivePath + "/agent-context.json"
	}
	for _, step := range resp.Steps {
		if !step.OK {
			summary.FailingSteps = append(summary.FailingSteps, step.Name)
		}
		for _, diagnostic := range step.Diagnostics {
			switch diagnostic.Severity {
			case "error":
				summary.ErrorCount++
			case "warning":
				summary.WarningCount++
			}
		}
	}
	for _, plan := range contextPack.RelevantActiveExecPlans {
		summary.ActivePlans = append(summary.ActivePlans, plan.Path)
	}
	agents := buildInspectDocsAgents(resp.Repo.Root)
	for _, file := range contextPack.DirtyFiles {
		for _, scope := range agents.Scopes {
			if scope.Scope == "." || file.Path == scope.Scope || strings.HasPrefix(file.Path, scope.Scope+"/") {
				summary.AgentScopes = append(summary.AgentScopes, scope.Path)
			}
		}
	}
	summary.AgentScopes = appendUniqueSorted(nil, summary.AgentScopes...)
	commands := appendUniqueSorted(nil, contextPack.ChangedAreaRecommendedCommands...)
	if len(commands) > 0 {
		commands = appendUniqueSorted(commands, "golangci-lint run ./...")
	}
	for _, command := range commands {
		check := harnessAgentCheck{Command: command, Status: "remaining", Condition: "No receipt in this run; the check may have passed separately. Inspect matching-input/scope evidence before rerunning."}
		if strings.HasPrefix(command, "scenery logs ") {
			check.Status = "conditional"
			check.Condition = "Requires the changed application's verified running runtime; this framework run does not prove application acceptance."
		}
		if stable && check.Status != "conditional" {
			for _, step := range resp.Steps {
				if step.OK && harnessAgentStepCoversCommand(step, command) {
					check.Status = "covered"
					check.EvidenceStep = step.Name
					check.Condition = ""
					break
				}
			}
			if resp.OK && ((command == repoinfo.ValidationFullCommand && fullTests && fullVet && (resp.Mode == harnessSelfModeDefault || resp.Mode == harnessSelfModeRace || resp.Mode == harnessSelfModeRelease)) || (command == harnessValidationQuickCommand && resp.Mode == harnessSelfModeQuick)) {
				check.Status = "covered"
				check.EvidenceStep = "selected verifier run"
				check.Condition = ""
			}
		}
		summary.Checks = append(summary.Checks, check)
	}
	// Source paths in scope remain repository-relative even for absolute Go
	// package directories; the complete context carries their full metadata.
	for i := range summary.AgentScopes {
		summary.AgentScopes[i] = filepath.ToSlash(summary.AgentScopes[i])
	}
	return summary
}

func harnessAgentStepCoversCommand(step harnessStep, command string) bool {
	if strings.Join(step.Command, " ") == command {
		return true
	}
	if step.Name != "go tests" || strings.Join(step.Command, " ") != "go test -json ./..." {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "test" {
		return false
	}
	for _, field := range fields[2:] {
		if field != "." && field != "./..." && !strings.HasPrefix(field, "./") {
			return false
		}
		if !filepath.IsLocal(field) {
			return false
		}
	}
	return true
}
