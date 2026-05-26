package workers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	ManifestSchemaVersion   = "onlava.worker.manifest.v1"
	ManifestSchemaVersionV2 = "onlava.worker.manifest.v2"
)

type Manifest struct {
	SchemaVersion string           `json:"schema_version,omitempty"`
	App           string           `json:"app"`
	Language      string           `json:"language"`
	BuildID       string           `json:"build_id"`
	PayloadCodec  string           `json:"payload_codec"`
	Temporal      ManifestTemporal `json:"temporal"`
	TaskQueues    []TaskQueue      `json:"task_queues,omitempty"`
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

type TaskQueue struct {
	Name             string   `json:"name"`
	Activities       []string `json:"activities,omitempty"`
	Workflows        []string `json:"workflows,omitempty"`
	RegistrationHash string   `json:"registration_hash"`
}

type Validation struct {
	Checked     bool         `json:"checked"`
	OK          bool         `json:"ok"`
	Count       int          `json:"count"`
	Manifests   []Summary    `json:"manifests"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type Summary struct {
	Path                   string             `json:"path"`
	SchemaVersion          string             `json:"schema_version,omitempty"`
	App                    string             `json:"app"`
	Language               string             `json:"language"`
	BuildID                string             `json:"build_id"`
	PayloadCodec           string             `json:"payload_codec"`
	Namespace              string             `json:"namespace"`
	TaskQueues             []string           `json:"task_queues"`
	TaskQueueRegistrations []TaskQueueSummary `json:"task_queue_registrations,omitempty"`
	Activities             []string           `json:"activities"`
}

type TaskQueueSummary struct {
	Name             string   `json:"name"`
	Activities       []string `json:"activities,omitempty"`
	Workflows        []string `json:"workflows,omitempty"`
	RegistrationHash string   `json:"registration_hash"`
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
	schemaVersion := manifestSchemaVersion(manifest)
	if schemaVersion != ManifestSchemaVersion && schemaVersion != ManifestSchemaVersionV2 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("schema_version must be %q or %q", ManifestSchemaVersion, ManifestSchemaVersionV2)})
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
	switch schemaVersion {
	case ManifestSchemaVersion:
		diagnostics = append(diagnostics, validateManifestV1Queues(manifest)...)
	case ManifestSchemaVersionV2:
		diagnostics = append(diagnostics, validateManifestV2Queues(manifest)...)
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
	if schemaVersion == ManifestSchemaVersionV2 {
		diagnostics = append(diagnostics, validateManifestV2Activities(manifest, activityNames)...)
	}
	return diagnostics
}

func manifestSchemaVersion(manifest Manifest) string {
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		return ManifestSchemaVersion
	}
	return strings.TrimSpace(manifest.SchemaVersion)
}

func validateManifestV1Queues(manifest Manifest) []Diagnostic {
	var diagnostics []Diagnostic
	if len(manifest.Temporal.TaskQueues) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "temporal.task_queues must not be empty"})
	}
	for _, queue := range manifest.Temporal.TaskQueues {
		if strings.TrimSpace(queue) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "temporal.task_queues must not contain empty values"})
		}
	}
	return diagnostics
}

func validateManifestV2Queues(manifest Manifest) []Diagnostic {
	var diagnostics []Diagnostic
	if len(manifest.Temporal.TaskQueues) > 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "onlava.worker.manifest.v2 uses top-level task_queues, not temporal.task_queues"})
	}
	if len(manifest.TaskQueues) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "task_queues must not be empty"})
	}
	seen := make(map[string]struct{})
	for _, queue := range manifest.TaskQueues {
		name := strings.TrimSpace(queue.Name)
		if name == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: "task_queues.name must not be empty"})
			continue
		}
		if _, exists := seen[name]; exists {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("duplicate task queue %q", name)})
		}
		seen[name] = struct{}{}
		hash := strings.TrimSpace(queue.RegistrationHash)
		if hash == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("task queue %q registration_hash must not be empty", name)})
		} else if !isRegistrationHash(hash) {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("task queue %q registration_hash must be sha256: followed by 64 lowercase hex characters", name)})
		}
	}
	return diagnostics
}

func isRegistrationHash(hash string) bool {
	if len(hash) != len("sha256:")+64 || !strings.HasPrefix(hash, "sha256:") {
		return false
	}
	for _, ch := range hash[len("sha256:"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func validateManifestV2Activities(manifest Manifest, activityNames map[string]struct{}) []Diagnostic {
	var diagnostics []Diagnostic
	registered := make(map[string]struct{})
	for _, queue := range manifest.TaskQueues {
		for _, activity := range queue.Activities {
			name := strings.TrimSpace(activity)
			if name == "" {
				diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("task queue %q activities must not contain empty values", queue.Name)})
				continue
			}
			registered[name] = struct{}{}
			if _, ok := activityNames[name]; !ok {
				diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("task queue %q references undeclared activity %q", queue.Name, name)})
			}
		}
		for _, workflow := range queue.Workflows {
			if strings.TrimSpace(workflow) == "" {
				diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("task queue %q workflows must not contain empty values", queue.Name)})
			}
		}
	}
	for name := range activityNames {
		if _, ok := registered[name]; !ok {
			diagnostics = append(diagnostics, Diagnostic{Path: manifest.Path, Message: fmt.Sprintf("activity %q is not registered on any task queue", name)})
		}
	}
	return diagnostics
}

func validateTaskQueueSharing(manifests []Summary) []Diagnostic {
	type owner struct {
		path             string
		language         string
		registrationHash string
	}
	owners := make(map[string]owner)
	var diagnostics []Diagnostic
	for _, manifest := range manifests {
		for _, queue := range manifest.TaskQueues {
			queue = strings.TrimSpace(queue)
			if queue == "" {
				continue
			}
			registrationHash := manifestRegistrationHash(manifest, queue)
			prev, exists := owners[queue]
			if !exists {
				owners[queue] = owner{path: manifest.Path, language: manifest.Language, registrationHash: registrationHash}
				continue
			}
			if prev.registrationHash != "" && registrationHash != "" {
				if prev.registrationHash != registrationHash {
					diagnostics = append(diagnostics, Diagnostic{
						Path:    manifest.Path,
						Message: fmt.Sprintf("task queue %q registration hash %q does not match %q from %s", queue, registrationHash, prev.registrationHash, prev.path),
					})
				}
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

func manifestRegistrationHash(manifest Summary, queue string) string {
	for _, registration := range manifest.TaskQueueRegistrations {
		if registration.Name == queue {
			return registration.RegistrationHash
		}
	}
	return ""
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
	queues := manifestTaskQueueNames(manifest)
	slices.Sort(queues)
	registrations := manifestTaskQueueSummaries(manifest)
	return Summary{
		Path:                   filepath.ToSlash(manifest.Path),
		SchemaVersion:          manifestSchemaVersion(manifest),
		App:                    manifest.App,
		Language:               manifest.Language,
		BuildID:                manifest.BuildID,
		PayloadCodec:           manifest.PayloadCodec,
		Namespace:              manifest.Temporal.Namespace,
		TaskQueues:             queues,
		TaskQueueRegistrations: registrations,
		Activities:             activities,
	}
}

func manifestTaskQueueNames(manifest Manifest) []string {
	if manifestSchemaVersion(manifest) == ManifestSchemaVersionV2 {
		queues := make([]string, 0, len(manifest.TaskQueues))
		for _, queue := range manifest.TaskQueues {
			if strings.TrimSpace(queue.Name) != "" {
				queues = append(queues, queue.Name)
			}
		}
		return queues
	}
	return append([]string(nil), manifest.Temporal.TaskQueues...)
}

func manifestTaskQueueSummaries(manifest Manifest) []TaskQueueSummary {
	if manifestSchemaVersion(manifest) != ManifestSchemaVersionV2 {
		return nil
	}
	registrations := make([]TaskQueueSummary, 0, len(manifest.TaskQueues))
	for _, queue := range manifest.TaskQueues {
		activities := append([]string(nil), queue.Activities...)
		workflows := append([]string(nil), queue.Workflows...)
		slices.Sort(activities)
		slices.Sort(workflows)
		registrations = append(registrations, TaskQueueSummary{
			Name:             queue.Name,
			Activities:       activities,
			Workflows:        workflows,
			RegistrationHash: queue.RegistrationHash,
		})
	}
	slices.SortFunc(registrations, func(a, b TaskQueueSummary) int {
		return strings.Compare(a.Name, b.Name)
	})
	return registrations
}
