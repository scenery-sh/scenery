package main

import (
	"path/filepath"
	"scenery.sh/internal/repoinfo"
	"strings"
	"testing"
)

func TestInstallPolicyAcceptsWrappingButNotUnrelatedPermission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, prose string
		fail        bool
	}{
		{"wrapped prohibition", "Do not run\n`go install ./cmd/scenery` during validation.", false},
		{"wrapped permission", "Running `go install ./cmd/scenery` is reserved for\nan explicit human request.", false},
		{"different paragraph", "Only when a human explicitly asks, deploy.\n\nRun `go install ./cmd/scenery`.", true},
		{"different bullet", "- Only when a human explicitly asks, deploy.\n- Run `go install ./cmd/scenery`.", true},
		{"unqualified command", "```sh\ngo install ./cmd/scenery\n```", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestAppFile(t, root, "AGENTS.md", tc.prose)
			diagnostics, _ := validateSharedCLIInstallPolicy(root)
			if hasErrorDiagnostics(diagnostics) != tc.fail {
				t.Fatalf("diagnostics = %+v, want failure %v", diagnostics, tc.fail)
			}
		})
	}
}

func TestSkillWorkflowRoutesResolveWithoutDuplicatingCommands(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, "SKILL.md", "[Repository](docs/agent-guide.md#working-in-the-scenery-repository)\n[App](docs/agent-guide.md#application-validation-and-completion)\n")
	writeTestAppFile(t, root, "docs/agent-guide.md", "## Working In The scenery Repository\n\n## Application Validation and Completion\n")
	if diagnostics, _ := validateSkillCoverage(root); len(diagnostics) != 0 {
		t.Fatalf("routed workflow rejected: %+v", diagnostics)
	}
	files := []harnessKnowledgeFile{{Path: "SKILL.md", Exists: true}}
	if _, diagnostics := checkHarnessMarkdownLinks(root, files); len(diagnostics) != 0 {
		t.Fatalf("valid routes rejected: %+v", diagnostics)
	}
	writeTestAppFile(t, root, "docs/agent-guide.md", "## Renamed\n")
	if _, diagnostics := checkHarnessMarkdownLinks(root, files); len(diagnostics) != 2 {
		t.Fatalf("broken workflow anchors not detected: %+v", diagnostics)
	}
}

func TestExecPlanRequiresResumeSectionsWithoutBoilerplate(t *testing.T) {
	t.Parallel()
	text := "# Implementation\n\n" + strings.Join(requiredExecPlanSections, "\n\n")
	if diagnostics := validateExecPlanSections(t.TempDir(), "docs/plans/0001-example.md", text, false); len(diagnostics) != 0 {
		t.Fatalf("plan requires redundant boilerplate: %+v", diagnostics)
	}
	text = strings.ReplaceAll(text, "## Validation and Acceptance", "## Notes")
	if diagnostics := validateExecPlanSections(t.TempDir(), "docs/plans/0001-example.md", text, false); len(diagnostics) != 1 {
		t.Fatalf("missing acceptance was not detected: %+v", diagnostics)
	}
}

func TestAgentLoopUsesFinalChangedAreaWithoutPreflight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, want, absent string }{
		{"README.md", harnessValidationQuickCommand, repoinfo.ValidationFullCommand},
		{"runtime/server.go", repoinfo.ValidationFullCommand, harnessValidationQuickCommand},
	} {
		t.Run(tc.path, func(t *testing.T) {
			root := t.TempDir()
			report := &harnessChangedAreaReport{}
			populateHarnessChangedAreaReport(root, report, []harnessChangedFile{{Path: filepath.ToSlash(tc.path), Status: "modified"}}, nil, nil)
			loop := harnessAgentFastLoop(report.RecommendedCommands)
			if !strings.Contains(loop, tc.want) || strings.Contains(loop, tc.absent) || strings.Contains(loop, "scenery doctor -o json") {
				t.Fatalf("incorrect command selection: %s", loop)
			}
		})
	}
	if loop := harnessAgentFastLoop(nil); strings.Contains(loop, "go run") || strings.Contains(loop, "scenery doctor") {
		t.Fatalf("empty changes acquired checks: %s", loop)
	}
}

func TestFailedKnowledgeCheckDoesNotRequestRelease(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	failed := runHarnessKnowledgeStep(root)
	if failed.OK {
		t.Fatal("missing documentation unexpectedly passed")
	}
	steps := buildHarnessAgentFailingSteps(root, []harnessStep{failed})
	commands := harnessAgentRerunCommands(steps)
	if len(commands) != 1 || strings.Contains(commands[0], "--release") || !strings.Contains(commands[0], "--quick") {
		t.Fatalf("ordinary failure acquired release work: %v", commands)
	}
}
