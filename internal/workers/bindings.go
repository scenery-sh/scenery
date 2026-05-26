package workers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"unicode"
)

type BindingResult struct {
	OK          bool          `json:"ok"`
	OutputDir   string        `json:"output_dir"`
	Files       []BindingFile `json:"files"`
	Diagnostics []Diagnostic  `json:"diagnostics,omitempty"`
}

type BindingFile struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Manifest string `json:"manifest"`
}

type bindingActivity struct {
	Name     string
	FuncName string
	Input    string
	Output   string
}

type bindingData struct {
	App          string
	Language     string
	BuildID      string
	PayloadCodec string
	Namespace    string
	TaskQueues   []string
	Activities   []bindingActivity
}

func GenerateBindings(appRoot, appName, outDir string) (BindingResult, error) {
	return GenerateBindingsWithKnownActivities(appRoot, appName, outDir, nil)
}

func GenerateBindingsWithKnownActivities(appRoot, appName, outDir string, knownActivities []string) (BindingResult, error) {
	result := BindingResult{OK: true}
	if strings.TrimSpace(outDir) == "" {
		outDir = filepath.Join(appRoot, ".onlava", "workers", "generated")
	}
	result.OutputDir = filepath.ToSlash(outDir)

	validation := ValidateWithKnownActivities(appRoot, appName, knownActivities)
	if !validation.OK {
		result.OK = false
		result.Diagnostics = validation.Diagnostics
		return result, fmt.Errorf("worker manifest validation failed")
	}
	paths, err := manifestPaths(appRoot)
	if err != nil {
		return result, err
	}
	for _, path := range paths {
		manifest, err := readManifest(path)
		if err != nil {
			return result, err
		}
		manifest.Path = path
		files, err := generateManifestBindings(outDir, manifest)
		if err != nil {
			return result, err
		}
		result.Files = append(result.Files, files...)
	}
	slices.SortFunc(result.Files, func(a, b BindingFile) int {
		return strings.Compare(a.Path, b.Path)
	})
	return result, nil
}

func generateManifestBindings(outDir string, manifest Manifest) ([]BindingFile, error) {
	data := bindingDataForManifest(manifest)
	base := strings.TrimSuffix(filepath.Base(manifest.Path), filepath.Ext(manifest.Path))
	if base == "" || base == "." {
		base = sanitizeIdentifier(manifest.Language)
	}
	dir := filepath.Join(outDir, sanitizeIdentifier(base))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	switch normalizeLanguage(manifest.Language) {
	case "python":
		path := filepath.Join(dir, "onlava_worker.py")
		if err := writeTemplate(path, pythonBindingTemplate, data); err != nil {
			return nil, err
		}
		return []BindingFile{{Path: filepath.ToSlash(path), Language: manifest.Language, Manifest: filepath.ToSlash(manifest.Path)}}, nil
	case "typescript":
		path := filepath.Join(dir, "onlava_worker.ts")
		if err := writeTemplate(path, typescriptBindingTemplate, data); err != nil {
			return nil, err
		}
		return []BindingFile{{Path: filepath.ToSlash(path), Language: manifest.Language, Manifest: filepath.ToSlash(manifest.Path)}}, nil
	default:
		metadataPath := filepath.Join(dir, "onlava_worker.json")
		payload, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return nil, err
		}
		payload = append(payload, '\n')
		if err := os.WriteFile(metadataPath, payload, 0o644); err != nil {
			return nil, err
		}
		return []BindingFile{{Path: filepath.ToSlash(metadataPath), Language: manifest.Language, Manifest: filepath.ToSlash(manifest.Path)}}, nil
	}
}

func bindingDataForManifest(manifest Manifest) bindingData {
	activities := make([]bindingActivity, 0, len(manifest.Activities))
	for _, activity := range manifest.Activities {
		activities = append(activities, bindingActivity{
			Name:     activity.Name,
			FuncName: sanitizeIdentifier(activity.Name),
			Input:    activity.Input,
			Output:   activity.Output,
		})
	}
	slices.SortFunc(activities, func(a, b bindingActivity) int {
		return strings.Compare(a.Name, b.Name)
	})
	taskQueues := manifestTaskQueueNames(manifest)
	slices.Sort(taskQueues)
	return bindingData{
		App:          manifest.App,
		Language:     manifest.Language,
		BuildID:      manifest.BuildID,
		PayloadCodec: manifest.PayloadCodec,
		Namespace:    manifest.Temporal.Namespace,
		TaskQueues:   taskQueues,
		Activities:   activities,
	}
}

func writeTemplate(path string, tmpl *template.Template, data bindingData) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func normalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ts", "typescript", "javascript", "js", "node":
		return "typescript"
	case "py", "python":
		return "python"
	default:
		return strings.ToLower(strings.TrimSpace(language))
	}
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "worker"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "worker"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "worker_" + out
	}
	return out
}

var pythonBindingTemplate = template.Must(template.New("python-worker").Parse(`# Code generated by onlava worker bindings; DO NOT EDIT.

APP = {{ printf "%q" .App }}
LANGUAGE = {{ printf "%q" .Language }}
BUILD_ID = {{ printf "%q" .BuildID }}
PAYLOAD_CODEC = {{ printf "%q" .PayloadCodec }}
NAMESPACE = {{ printf "%q" .Namespace }}
TASK_QUEUES = [
{{- range .TaskQueues }}
    {{ printf "%q" . }},
{{- end }}
]

ACTIVITIES = {
{{- range .Activities }}
    {{ printf "%q" .Name }}: {
        "input": {{ printf "%q" .Input }},
        "output": {{ printf "%q" .Output }},
        "function": {{ printf "%q" .FuncName }},
    },
{{- end }}
}

{{ range .Activities }}
async def {{ .FuncName }}(payload):
    """Implement onlava activity {{ .Name }}.

    Payload codec: {{ $.PayloadCodec }}
    Input schema: {{ .Input }}
    Output schema: {{ .Output }}
    """
    raise NotImplementedError({{ printf "%q" .Name }})
{{ end }}

ACTIVITY_FUNCTIONS = [
{{- range .Activities }}
    {{ .FuncName }},
{{- end }}
]
`))

var typescriptBindingTemplate = template.Must(template.New("typescript-worker").Parse(`// Code generated by onlava worker bindings; DO NOT EDIT.

export const app = {{ printf "%q" .App }};
export const language = {{ printf "%q" .Language }};
export const buildId = {{ printf "%q" .BuildID }};
export const payloadCodec = {{ printf "%q" .PayloadCodec }};
export const namespace = {{ printf "%q" .Namespace }};
export const taskQueues = [
{{- range .TaskQueues }}
  {{ printf "%q" . }},
{{- end }}
] as const;

export const activities = {
{{- range .Activities }}
  {{ printf "%q" .Name }}: {
    input: {{ printf "%q" .Input }},
    output: {{ printf "%q" .Output }},
    functionName: {{ printf "%q" .FuncName }},
  },
{{- end }}
} as const;

{{ range .Activities }}
export async function {{ .FuncName }}(payload: unknown): Promise<unknown> {
  throw new Error({{ printf "%q" (printf "Implement onlava activity %s" .Name) }});
}
{{ end }}

export const activityImplementations = {
{{- range .Activities }}
  {{ printf "%q" .Name }}: {{ .FuncName }},
{{- end }}
};
`))
