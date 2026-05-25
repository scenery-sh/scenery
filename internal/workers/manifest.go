package workers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const ManifestSchemaVersion = "onlava.worker.manifest.v1"

type Manifest struct {
	SchemaVersion string           `json:"schema_version,omitempty"`
	App           string           `json:"app"`
	Language      string           `json:"language"`
	BuildID       string           `json:"build_id"`
	PayloadCodec  string           `json:"payload_codec"`
	Temporal      ManifestTemporal `json:"temporal"`
	Activities    []Activity       `json:"activities"`
	Path          string           `json:"-"`
}

type ManifestTemporal struct {
	Namespace  string   `json:"namespace"`
	TaskQueues []string `json:"task_queues"`
}

type Activity struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

type Validation struct {
	Checked     bool         `json:"checked"`
	OK          bool         `json:"ok"`
	Count       int          `json:"count"`
	Manifests   []Summary    `json:"manifests"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type Summary struct {
	Path         string   `json:"path"`
	App          string   `json:"app"`
	Language     string   `json:"language"`
	BuildID      string   `json:"build_id"`
	PayloadCodec string   `json:"payload_codec"`
	Namespace    string   `json:"namespace"`
	TaskQueues   []string `json:"task_queues"`
	Activities   []string `json:"activities"`
}

type Diagnostic struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func Validate(appRoot, appName string) Validation {
	return ValidateWithKnownActivities(appRoot, appName, nil)
}

func ValidateWithKnownActivities(appRoot, appName string, knownActivities []string) Validation {
	result := Validation{Checked: true, OK: true}
	paths, err := manifestPaths(appRoot)
	if err != nil {
		result.OK = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Message: err.Error()})
		return result
	}
	for _, path := range paths {
		manifest, err := readManifest(path)
		if err != nil {
			result.OK = false
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: path, Message: err.Error()})
			continue
		}
		manifest.Path = path
		result.Manifests = append(result.Manifests, summarize(manifest))
		result.Diagnostics = append(result.Diagnostics, validateManifest(manifest, appName)...)
	}
	result.Count = len(result.Manifests)
	result.Diagnostics = append(result.Diagnostics, validateTaskQueueSharing(result.Manifests)...)
	result.Diagnostics = append(result.Diagnostics, validateKnownActivities(result.Manifests, knownActivities)...)
	result.OK = len(result.Diagnostics) == 0
	return result
}

func manifestPaths(appRoot string) ([]string, error) {
	if strings.TrimSpace(appRoot) == "" {
		return nil, nil
	}
	matches, err := filepath.Glob(filepath.Join(appRoot, ".onlava", "workers", "*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	return matches, nil
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest Manifest, appName string) []Diagnostic {
	var diagnostics []Diagnostic
	if manifest.SchemaVersion != "" && manifest.SchemaVersion != ManifestSchemaVersion {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("schema_version must be %q", ManifestSchemaVersion)})
	}
	if strings.TrimSpace(manifest.App) == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "app must not be empty"})
	} else if appName != "" && manifest.App != appName {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("app %q does not match onlava app %q", manifest.App, appName)})
	}
	if strings.TrimSpace(manifest.Language) == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "language must not be empty"})
	}
	if strings.TrimSpace(manifest.BuildID) == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "build_id must not be empty"})
	}
	if strings.TrimSpace(manifest.PayloadCodec) == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "payload_codec must not be empty"})
	} else if manifest.PayloadCodec != "onlava-json-v1" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "payload_codec must be onlava-json-v1"})
	}
	if strings.TrimSpace(manifest.Temporal.Namespace) == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "temporal.namespace must not be empty"})
	}
	if len(manifest.Temporal.TaskQueues) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "temporal.task_queues must not be empty"})
	}
	for _, queue := range manifest.Temporal.TaskQueues {
		if strings.TrimSpace(queue) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "temporal.task_queues must not contain empty values"})
		}
	}
	if len(manifest.Activities) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "activities must not be empty"})
	}
	activityNames := make(map[string]struct{})
	for _, activity := range manifest.Activities {
		name := strings.TrimSpace(activity.Name)
		if name == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "activity name must not be empty"})
			continue
		}
		if _, exists := activityNames[name]; exists {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("duplicate activity %q", name)})
		}
		activityNames[name] = struct{}{}
		if strings.TrimSpace(activity.Input) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("activity %q input must not be empty", name)})
		}
		if strings.TrimSpace(activity.Output) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("activity %q output must not be empty", name)})
		}
	}
	return diagnostics
}

func validateTaskQueueSharing(manifests []Summary) []Diagnostic {
	type owner struct {
		path     string
		language string
	}
	owners := make(map[string]owner)
	var diagnostics []Diagnostic
	for _, manifest := range manifests {
		for _, queue := range manifest.TaskQueues {
			queue = strings.TrimSpace(queue)
			if queue == "" {
				continue
			}
			prev, exists := owners[queue]
			if !exists {
				owners[queue] = owner{path: manifest.Path, language: manifest.Language}
				continue
			}
			if prev.language != manifest.Language {
				diagnostics = append(diagnostics, Diagnostic{
					Path:    manifest.Path,
					Message: fmt.Sprintf("task queue %q is shared by incompatible worker languages %q and %q", queue, prev.language, manifest.Language),
				})
			}
		}
	}
	return diagnostics
}

func validateKnownActivities(manifests []Summary, knownActivities []string) []Diagnostic {
	if len(knownActivities) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(knownActivities))
	for _, name := range knownActivities {
		if strings.TrimSpace(name) != "" {
			known[name] = struct{}{}
		}
	}
	if len(known) == 0 {
		return nil
	}
	var diagnostics []Diagnostic
	for _, manifest := range manifests {
		for _, activity := range manifest.Activities {
			if _, ok := known[activity]; !ok {
				diagnostics = append(diagnostics, Diagnostic{
					Path:    manifest.Path,
					Message: fmt.Sprintf("activity %q is not declared by this onlava app", activity),
				})
			}
		}
	}
	return diagnostics
}

func summarize(manifest Manifest) Summary {
	activities := make([]string, 0, len(manifest.Activities))
	for _, activity := range manifest.Activities {
		if strings.TrimSpace(activity.Name) != "" {
			activities = append(activities, activity.Name)
		}
	}
	slices.Sort(activities)
	queues := append([]string(nil), manifest.Temporal.TaskQueues...)
	slices.Sort(queues)
	return Summary{
		Path:         filepath.ToSlash(manifest.Path),
		App:          manifest.App,
		Language:     manifest.Language,
		BuildID:      manifest.BuildID,
		PayloadCodec: manifest.PayloadCodec,
		Namespace:    manifest.Temporal.Namespace,
		TaskQueues:   queues,
		Activities:   activities,
	}
}
