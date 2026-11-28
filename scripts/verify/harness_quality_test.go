package main

import (
	"errors"
	"strings"
	"testing"

	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/repoinfo"
)

func TestNormalizeDarwinTMPDIR(t *testing.T) {
	const userTemp = "/var/folders/ab/cdefghijklmnop/T/"
	const cleanUserTemp = "/var/folders/ab/cdefghijklmnop/T"
	statGID := func(path string) (uint32, bool) {
		switch path {
		case "/tmp", "/private/tmp":
			return 0, true
		case "/var/folders/ok/T", cleanUserTemp:
			return 20, true
		default:
			return 20, true
		}
	}
	darwinTemp := func() (string, error) { return userTemp + "\n", nil }

	cases := []struct {
		name        string
		goos        string
		current     string
		want        string
		wantChanged bool
	}{
		{name: "non-darwin unchanged", goos: "linux", current: "/tmp", want: "", wantChanged: false},
		{name: "empty tmpdir normalized", goos: "darwin", current: "", want: cleanUserTemp, wantChanged: true},
		{name: "wheel gid tmp normalized", goos: "darwin", current: "/tmp", want: cleanUserTemp, wantChanged: true},
		{name: "over 50 bytes normalized", goos: "darwin", current: "/" + strings.Repeat("a", 55), want: cleanUserTemp, wantChanged: true},
		{name: "short user-owned preserved", goos: "darwin", current: "/var/folders/ok/T", want: "", wantChanged: false},
		{name: "already user temp idempotent", goos: "darwin", current: cleanUserTemp, want: "", wantChanged: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := normalizeDarwinTMPDIRWith(tc.goos, tc.current, statGID, darwinTemp)
			if changed != tc.wantChanged || got != tc.want {
				t.Fatalf("normalizeDarwinTMPDIRWith(%q, %q) = (%q, %v), want (%q, %v)", tc.goos, tc.current, got, changed, tc.want, tc.wantChanged)
			}
		})
	}

	if got, changed := normalizeDarwinTMPDIRWith("darwin", "/tmp", statGID, func() (string, error) {
		return "", errors.New("getconf unavailable")
	}); changed || got != "" {
		t.Fatalf("getconf error = (%q, %v), want empty/false", got, changed)
	}
}

func TestScrubSceneryProbeEnv(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HOME=/Users/dev",
		"DATABASE_URL=postgres://leaked",
		"GOWORK=/tmp/go.work",
		"GOFLAGS=-mod=vendor",
		"SCENERY_APP_ROOT=/leaked/app",
		"SCENERY_AGENT_HOME=/leaked/home",
		"SCENERY_AGENT_SOCKET=/leaked/sock",
		"SCENERY_AGENT_ROUTER_ADDR=127.0.0.1:9999",
		"SCENERY_DEV_CACHE_DIR=/leaked/cache",
		"SCENERY_DEV_DETACHED_CHILD=1",
		"SCENERY_CUSTOM_FLAG=1",
		"SCENERY_EXECUTION_PURPOSE=verification",
	}
	got := scrubSceneryProbeEnv(base, "SCENERY_AGENT_HOME=/probe/home", "DATABASE_URL=postgres://probe")
	values := map[string]string{}
	for _, item := range got {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			values[k] = v
		}
	}
	for _, stripped := range []string{
		"GOWORK", "GOFLAGS", "SCENERY_APP_ROOT", "SCENERY_AGENT_SOCKET",
		"SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_CACHE_DIR",
		"SCENERY_DEV_DETACHED_CHILD", "SCENERY_CUSTOM_FLAG",
	} {
		if _, exists := values[stripped]; exists {
			t.Fatalf("expected %s to be stripped, got %q", stripped, values[stripped])
		}
	}
	if values["PATH"] != "/usr/bin" || values["HOME"] != "/Users/dev" {
		t.Fatalf("base env corrupted: %+v", values)
	}
	if values["SCENERY_EXECUTION_PURPOSE"] != "verification" {
		t.Fatalf("SCENERY_EXECUTION_PURPOSE = %q, want verification", values["SCENERY_EXECUTION_PURPOSE"])
	}
	if values["SCENERY_AGENT_HOME"] != "/probe/home" || values["DATABASE_URL"] != "postgres://probe" {
		t.Fatalf("explicit overrides not applied: %+v", values)
	}
}

func TestExtractDocSCNBlocks(t *testing.T) {
	doc := strings.Join([]string{
		"# Example",
		"```hcl",
		`service "ignored" {}`,
		"```",
		"```scn",
		`application "demo" {}`,
		"```",
		"```scn illustrative",
		`service "incomplete" {`,
		"```",
		"```scn",
		`service "truncated" { ... }`,
		"```",
		"```scn",
		"   ",
		"```",
	}, "\n")
	blocks, skipped := extractDocSCNBlocks("README.md", doc)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1: %+v", len(blocks), blocks)
	}
	if skipped != 3 {
		t.Fatalf("skipped = %d, want 3", skipped)
	}
	if blocks[0].DocRel != "README.md" || blocks[0].Line != 6 || blocks[0].Body != "application \"demo\" {}\n" {
		t.Fatalf("unexpected block: %+v", blocks[0])
	}
}

func TestAgentContextSummaryCoversGolangCILint(t *testing.T) {
	run := &harnessreport.ValidationRun{
		ID:                 "run-1",
		InputRevision:      "sha256:" + strings.Repeat("a", 64),
		FinalInputRevision: "sha256:" + strings.Repeat("a", 64),
		InputsStable:       true,
		ArchivePath:        ".scenery/harness/runs/run-1",
	}
	contextPack := harnessAgentContext{
		ChangedAreaRecommendedCommands: []string{repoinfo.ValidationFullCommand},
	}

	resp := harnessSelfResponse{
		OK:   true,
		Mode: harnessSelfModeDefault,
		Run:  run,
		Steps: []harnessStep{
			{Name: "go tests", OK: true, Command: harnessSelfGoTestCommand()},
			{Name: "golangci-lint", OK: true, Command: []string{"golangci-lint", "run", "./..."}},
		},
	}
	summary := buildHarnessAgentContextSummary(resp, contextPack)
	checkStatus := map[string]harnessAgentCheck{}
	for _, check := range summary.Checks {
		checkStatus[check.Command] = check
	}
	if got := checkStatus["golangci-lint run ./..."]; got.Status != "covered" || got.EvidenceStep != "golangci-lint" {
		t.Fatalf("golangci-lint check = %+v, want covered by golangci-lint", got)
	}
	if got := checkStatus[repoinfo.ValidationFullCommand]; got.Status != "covered" {
		t.Fatalf("full validation check = %+v, want covered", got)
	}

	fallbackResp := harnessSelfResponse{
		OK:   true,
		Mode: harnessSelfModeDefault,
		Run:  run,
		Steps: []harnessStep{
			{Name: "go tests", OK: true, Command: harnessSelfGoTestCommand()},
			{Name: "go vet", OK: true, Command: []string{"go", "vet", "./..."}},
		},
	}
	fallbackSummary := buildHarnessAgentContextSummary(fallbackResp, contextPack)
	fallbackStatus := map[string]harnessAgentCheck{}
	for _, check := range fallbackSummary.Checks {
		fallbackStatus[check.Command] = check
	}
	if got := fallbackStatus["golangci-lint run ./..."]; got.Status != "remaining" {
		t.Fatalf("fallback golangci-lint check = %+v, want remaining", got)
	}
	if got := fallbackStatus[repoinfo.ValidationFullCommand]; got.Status != "covered" {
		t.Fatalf("fallback full validation check = %+v, want covered", got)
	}
}
