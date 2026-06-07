package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/inspect"
)

const inspectHarnessSchema = "onlava.inspect.harness.v1"

type inspectHarnessResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Scope         string                 `json:"scope"`
	Root          string                 `json:"root"`
	App           *inspect.AppRef        `json:"app,omitempty"`
	Repo          *harnessSelfRepo       `json:"repo,omitempty"`
	Latest        []inspectHarnessLatest `json:"latest"`
	Artifacts     []harnessArtifact      `json:"artifacts,omitempty"`
	Evidence      []harnessEvidence      `json:"evidence,omitempty"`
}

type inspectHarnessLatest struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version"`
	Exists        bool   `json:"exists"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	ModifiedAt    string `json:"modified_at,omitempty"`
}

func buildInspectHarnessResponse(opts inspectOptions) (inspectHarnessResponse, error) {
	root, scope, appRef, repoRef, err := resolveInspectHarnessRoot(opts)
	if err != nil {
		return inspectHarnessResponse{}, err
	}
	resp := inspectHarnessResponse{
		SchemaVersion: inspectHarnessSchema,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Scope:         scope,
		Root:          root,
		App:           appRef,
		Repo:          repoRef,
	}
	candidates := []inspectHarnessLatest{
		{Name: "app-harness", Path: ".onlava/harness/latest.json", SchemaVersion: "onlava.harness.result.v1"},
		{Name: "self-harness", Path: ".onlava/harness/self-latest.json", SchemaVersion: "onlava.harness.self.v1"},
		{Name: "ui-harness", Path: ".onlava/harness/ui/latest.json", SchemaVersion: "onlava.harness.ui.v1"},
		{Name: "evidence-artifacts", Path: ".onlava/harness/artifacts", SchemaVersion: harnessArtifactEvidenceSchema},
	}
	for _, item := range candidates {
		item = inspectHarnessLatestWithStat(root, item)
		resp.Latest = append(resp.Latest, item)
		if !item.Exists {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(item.Path))
		switch item.Name {
		case "app-harness":
			if payload, err := readHarnessJSON[harnessResponse](abs); err == nil {
				resp.Artifacts = append(resp.Artifacts, payload.Artifacts...)
				resp.Evidence = append(resp.Evidence, evidenceFromHarnessSteps(payload.Steps)...)
			}
		case "self-harness":
			if payload, err := readHarnessJSON[harnessSelfResponse](abs); err == nil {
				resp.Artifacts = append(resp.Artifacts, payload.Artifacts...)
				resp.Evidence = append(resp.Evidence, evidenceFromHarnessSteps(payload.Steps)...)
			}
		case "ui-harness":
			if payload, err := readHarnessJSON[harnessUIResponse](abs); err == nil {
				resp.Artifacts = append(resp.Artifacts, payload.Artifacts...)
				resp.Evidence = append(resp.Evidence, payload.Evidence...)
				for _, route := range payload.Routes {
					if route.Evidence != nil {
						resp.Evidence = append(resp.Evidence, *route.Evidence)
					}
				}
			}
		}
	}
	resp.Artifacts = dedupeHarnessArtifacts(resp.Artifacts)
	return resp, nil
}

func resolveInspectHarnessRoot(opts inspectOptions) (string, string, *inspect.AppRef, *harnessSelfRepo, error) {
	if opts.RepoRoot != "" {
		repoRoot, err := discoverOnlavaRepoRoot(opts.RepoRoot)
		if err != nil {
			return "", "", nil, nil, err
		}
		return repoRoot, "repo", nil, &harnessSelfRepo{
			Root:       repoRoot,
			ModulePath: "github.com/pbrazdil/onlava",
			GoModPath:  filepath.Join(repoRoot, "go.mod"),
		}, nil
	}
	if opts.AppRoot != "" {
		start, err := resolveAppRoot(opts.AppRoot)
		if err != nil {
			return "", "", nil, nil, err
		}
		appRoot, cfg, err := appcfg.DiscoverRoot(start)
		if err != nil {
			return "", "", nil, nil, err
		}
		app := inspectAppInfo(appRoot, cfg, nil)
		return appRoot, "app", &app, nil, nil
	}
	if cwd, err := os.Getwd(); err == nil {
		if appRoot, cfg, appErr := appcfg.DiscoverRoot(cwd); appErr == nil {
			app := inspectAppInfo(appRoot, cfg, nil)
			return appRoot, "app", &app, nil, nil
		}
	}
	repoRoot, err := discoverOnlavaRepoRoot("")
	if err != nil {
		return "", "", nil, nil, err
	}
	return repoRoot, "repo", nil, &harnessSelfRepo{
		Root:       repoRoot,
		ModulePath: "github.com/pbrazdil/onlava",
		GoModPath:  filepath.Join(repoRoot, "go.mod"),
	}, nil
}

func inspectHarnessLatestWithStat(root string, item inspectHarnessLatest) inspectHarnessLatest {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(item.Path)))
	if err != nil {
		return item
	}
	item.Exists = true
	item.SizeBytes = info.Size()
	item.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	return item
}

func readHarnessJSON[T any](path string) (T, error) {
	var out T
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func evidenceFromHarnessSteps(steps []harnessStep) []harnessEvidence {
	out := make([]harnessEvidence, 0, len(steps))
	for _, step := range steps {
		if step.Evidence == nil {
			continue
		}
		out = append(out, *step.Evidence)
	}
	return out
}

func dedupeHarnessArtifacts(items []harnessArtifact) []harnessArtifact {
	seen := map[string]bool{}
	out := make([]harnessArtifact, 0, len(items))
	for _, item := range items {
		key := item.Name + "\x00" + item.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}
