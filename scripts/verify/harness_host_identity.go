package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// harnessServedIdentity is the build identity the application host attests on
// every response it serves.
type harnessServedIdentity struct {
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
}

// harnessResponseIdentity reads the identity a host attested on a response.
func harnessResponseIdentity(response *http.Response) harnessServedIdentity {
	return harnessServedIdentity{
		ContractRevision:       response.Header.Get("X-App-Contract-Revision"),
		ImplementationRevision: response.Header.Get("X-App-Implementation-Revision"),
		BuildInputDigest:       response.Header.Get("X-App-Build-Digest"),
	}
}

func harnessSameBuild(left, right harnessServedIdentity) bool {
	return left.ContractRevision != "" && left == right
}

// harnessWorktreeServing reads the identity the worktree's running host
// serves once its current source has been served.
func harnessWorktreeServing(ctx context.Context, repoRoot, appRoot string, env []string) (harnessServedIdentity, error) {
	output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "ps", "--wait", "-o", "json")
	if err != nil {
		return harnessServedIdentity{}, err
	}
	var status struct {
		Outcome string                 `json:"outcome"`
		Serving *harnessServedIdentity `json:"serving"`
	}
	if err := decodeCLIJSON(output, &status); err != nil {
		return harnessServedIdentity{}, err
	}
	if status.Outcome != "served" || status.Serving == nil {
		return harnessServedIdentity{}, fmt.Errorf("worktree source is %s, not served", status.Outcome)
	}
	return *status.Serving, nil
}

// harnessHostRebuild is one rebuild the development host reported.
type harnessHostRebuild struct {
	Generation     uint64   `json:"generation"`
	EditToReadyMS  float64  `json:"edit_to_ready_ms"`
	TotalMS        float64  `json:"total_ms"`
	CompileMS      float64  `json:"compile_ms"`
	LinkMS         float64  `json:"link_ms"`
	ReadyMS        float64  `json:"ready_ms"`
	Declarations   bool     `json:"declarations"`
	Rebuilt        []string `json:"rebuilt"`
	RebuiltModules []string `json:"rebuilt_modules"`
	SkippedModules []string `json:"skipped_modules"`
	GoLinks        []string `json:"go_links"`
	TriggerKind    string   `json:"trigger_kind"`
	// Failed is set for an update the host rejected while the previous
	// generation kept serving.
	Failed bool `json:"-"`
}

const (
	harnessHostRebuildPrefix = "worker rebuild "
	harnessHostFailurePrefix = "application update failed"
)

// harnessHostRebuilds are the rebuilds the host reported in a supervisor log
// after offset, in order. A failed update is followed by its timing line,
// which carries no edit-to-ready time.
func harnessHostRebuilds(events []harnessWatchEvent) []harnessHostRebuild {
	var rebuilds []harnessHostRebuild
	failed := false
	for _, event := range events {
		if event.Type != "process.output" {
			continue
		}
		for line := range strings.SplitSeq(event.Data.Output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, harnessHostFailurePrefix) {
				failed = true
				continue
			}
			payload, ok := strings.CutPrefix(line, harnessHostRebuildPrefix)
			if !ok {
				continue
			}
			var rebuild harnessHostRebuild
			if json.Unmarshal([]byte(payload), &rebuild) != nil {
				continue
			}
			rebuild.Failed = failed || rebuild.EditToReadyMS == 0
			failed = false
			rebuilds = append(rebuilds, rebuild)
		}
	}
	return rebuilds
}

// harnessHostRebuildEvidence requires one successful host rebuild after an
// implementation edit that relinked exactly the edited module's workers.
func harnessHostRebuildEvidence(events []harnessWatchEvent, module string) (map[string]any, error) {
	rebuilds := harnessHostRebuilds(events)
	successful := slices.DeleteFunc(slices.Clone(rebuilds), func(rebuild harnessHostRebuild) bool { return rebuild.Failed })
	if len(successful) != 1 {
		return nil, fmt.Errorf("implementation edit produced %d successful host rebuilds, want 1: %+v", len(successful), rebuilds)
	}
	rebuild := successful[0]
	if rebuild.Declarations || !slices.Equal(rebuild.RebuiltModules, []string{module}) || len(rebuild.GoLinks) == 0 {
		return nil, fmt.Errorf("implementation edit did not take the incremental worker path: %+v", rebuild)
	}
	return map[string]any{
		"generation": rebuild.Generation, "rebuilt_modules": rebuild.RebuiltModules, "skipped_modules": rebuild.SkippedModules,
		"go_links": rebuild.GoLinks, "compile_ms": rebuild.CompileMS, "link_ms": rebuild.LinkMS, "ready_ms": rebuild.ReadyMS,
		"edit_to_ready_ms": rebuild.EditToReadyMS, "trigger_kind": rebuild.TriggerKind,
	}, nil
}
