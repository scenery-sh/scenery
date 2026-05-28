package workers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

const TypeScriptWorkerGeneratedRelDir = ".onlava/generated/temporal/typescript"

type TypeScriptActivity struct {
	ExportName     string
	ImportAlias    string
	Name           string
	TaskQueue      string
	Input          string
	Output         string
	File           string
	Line           int
	MaxConcurrency int
}

type TypeScriptWorkerModel struct {
	Activities  []TypeScriptActivity
	Diagnostics []Diagnostic
}

type TypeScriptWorkerOptions struct {
	AppRoot      string
	AppName      string
	OutputDir    string
	BuildID      string
	Namespace    string
	PayloadCodec string
}

type TypeScriptWorkerResult struct {
	OK          bool                 `json:"ok"`
	OutputDir   string               `json:"output_dir"`
	Files       []BindingFile        `json:"files"`
	Activities  []TypeScriptActivity `json:"activities,omitempty"`
	Diagnostics []Diagnostic         `json:"diagnostics,omitempty"`
}

type ExternalActivityDeclaration struct {
	Name      string
	TaskQueue string
	Input     string
	Output    string
	File      string
	Line      int
	Kind      string
}

type tsRegistryImport struct {
	ExportName string
	Alias      string
	Path       string
}

type tsRegistryQueue struct {
	Name           string
	Activities     []TypeScriptActivity
	MaxConcurrency int
}

type tsRegistryData struct {
	Imports []tsRegistryImport
	Queues  []tsRegistryQueue
}

type tsWorkerData struct{}

type tsConfigData struct{}

var (
	tsActivityCallRE  = regexp.MustCompile(`(?s)(?:export\s+)?const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*activity\s*<\s*([^,\n>]+)\s*,\s*([^>\n]+)\s*>\s*\(`)
	tsMaxConcurrency  = regexp.MustCompile(`(?s)\bmaxConcurrency\s*:\s*([0-9]+)`)
	tsVersionSuffixRE = regexp.MustCompile(`/v[0-9]+$`)
)

func DiscoverTypeScriptActivities(appRoot string) TypeScriptWorkerModel {
	appRoot = strings.TrimSpace(appRoot)
	if appRoot == "" {
		return TypeScriptWorkerModel{}
	}
	var model TypeScriptWorkerModel
	err := filepath.WalkDir(appRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			model.Diagnostics = append(model.Diagnostics, Diagnostic{Path: filepath.ToSlash(path), Message: err.Error()})
			return nil
		}
		if entry.IsDir() {
			if shouldSkipTypeScriptWorkerDir(appRoot, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".ts") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			model.Diagnostics = append(model.Diagnostics, Diagnostic{Path: filepath.ToSlash(path), Message: err.Error()})
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".worker.ts") && !bytes.Contains(data, []byte("//onlava:worker")) {
			return nil
		}
		rel := relativeWorkerPath(appRoot, path)
		activities, diagnostics := parseTypeScriptWorkerFile(rel, data)
		model.Activities = append(model.Activities, activities...)
		model.Diagnostics = append(model.Diagnostics, diagnostics...)
		return nil
	})
	if err != nil {
		model.Diagnostics = append(model.Diagnostics, Diagnostic{Message: err.Error()})
	}
	slices.SortFunc(model.Activities, func(a, b TypeScriptActivity) int {
		if cmp := strings.Compare(a.File, b.File); cmp != 0 {
			return cmp
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Name, b.Name)
	})
	return model
}

func ValidateTypeScriptActivities(model TypeScriptWorkerModel) []Diagnostic {
	diagnostics := append([]Diagnostic(nil), model.Diagnostics...)
	seen := make(map[string]TypeScriptActivity)
	for _, activity := range model.Activities {
		if strings.TrimSpace(activity.Name) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: "activity config name must not be empty"})
		} else if prev, ok := seen[activity.Name]; ok {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: fmt.Sprintf("duplicate TypeScript activity %q; first declared at %s", activity.Name, activityPosition(prev))})
		} else {
			seen[activity.Name] = activity
		}
		if activity.Name != "" && !tsVersionSuffixRE.MatchString(activity.Name) {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: fmt.Sprintf("TypeScript activity %q must use a version suffix such as /v1", activity.Name)})
		}
		if strings.TrimSpace(activity.TaskQueue) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: fmt.Sprintf("TypeScript activity %q taskQueue must not be empty", activity.Name)})
		}
		if strings.TrimSpace(activity.Input) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: fmt.Sprintf("TypeScript activity %q input type must not be empty", activity.Name)})
		}
		if strings.TrimSpace(activity.Output) == "" {
			diagnostics = append(diagnostics, Diagnostic{Path: activityPosition(activity), Message: fmt.Sprintf("TypeScript activity %q output type must not be empty", activity.Name)})
		}
	}
	return diagnostics
}

func ValidateTypeScriptContracts(ts TypeScriptWorkerModel, externals []ExternalActivityDeclaration, nativeGo []ExternalActivityDeclaration) []Diagnostic {
	diagnostics := ValidateTypeScriptActivities(ts)
	byName := make(map[string]TypeScriptActivity, len(ts.Activities))
	queues := make(map[string]struct{})
	for _, activity := range ts.Activities {
		if strings.TrimSpace(activity.Name) != "" {
			byName[activity.Name] = activity
		}
		if strings.TrimSpace(activity.TaskQueue) != "" {
			queues[activity.TaskQueue] = struct{}{}
		}
	}
	for _, decl := range externals {
		if strings.TrimSpace(decl.Name) == "" {
			continue
		}
		activity, ok := byName[decl.Name]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Path: externalPosition(decl), Message: fmt.Sprintf("temporal external activity %q has no matching TypeScript activity declaration", decl.Name)})
			continue
		}
		if strings.TrimSpace(decl.TaskQueue) != "" && decl.TaskQueue != activity.TaskQueue {
			diagnostics = append(diagnostics, Diagnostic{Path: externalPosition(decl), Message: fmt.Sprintf("temporal external activity %q uses task queue %q but TypeScript activity uses %q", decl.Name, decl.TaskQueue, activity.TaskQueue)})
		}
		if decl.Input != "" && activity.Input != "" && canonicalTypeName(decl.Input) != canonicalTypeName(activity.Input) {
			diagnostics = append(diagnostics, Diagnostic{Path: externalPosition(decl), Message: fmt.Sprintf("temporal external activity %q input type %q does not match TypeScript input %q", decl.Name, decl.Input, activity.Input)})
		}
		if decl.Output != "" && activity.Output != "" && canonicalTypeName(decl.Output) != canonicalTypeName(activity.Output) {
			diagnostics = append(diagnostics, Diagnostic{Path: externalPosition(decl), Message: fmt.Sprintf("temporal external activity %q output type %q does not match TypeScript output %q", decl.Name, decl.Output, activity.Output)})
		}
	}
	for _, decl := range nativeGo {
		if strings.TrimSpace(decl.TaskQueue) == "" {
			continue
		}
		if _, ok := queues[decl.TaskQueue]; ok {
			diagnostics = append(diagnostics, Diagnostic{Path: externalPosition(decl), Message: fmt.Sprintf("Go Temporal declaration %q shares TypeScript task queue %q", decl.Name, decl.TaskQueue)})
		}
	}
	return diagnostics
}

func ValidateTypeScriptTaskQueues(activities []TypeScriptActivity, selected []string) []Diagnostic {
	if len(selected) == 0 {
		return nil
	}
	known := make(map[string]struct{})
	for _, activity := range activities {
		if strings.TrimSpace(activity.TaskQueue) != "" {
			known[activity.TaskQueue] = struct{}{}
		}
	}
	var diagnostics []Diagnostic
	for _, queue := range selected {
		queue = strings.TrimSpace(queue)
		if queue == "" {
			continue
		}
		if _, ok := known[queue]; !ok {
			diagnostics = append(diagnostics, Diagnostic{Message: fmt.Sprintf("unknown TypeScript Temporal task queue %q", queue)})
		}
	}
	return diagnostics
}

func GenerateTypeScriptWorker(opts TypeScriptWorkerOptions) (TypeScriptWorkerResult, error) {
	opts = normalizeTypeScriptWorkerOptions(opts)
	result := TypeScriptWorkerResult{OK: true, OutputDir: filepath.ToSlash(opts.OutputDir)}
	model := DiscoverTypeScriptActivities(opts.AppRoot)
	result.Activities = append([]TypeScriptActivity(nil), model.Activities...)
	diagnostics := ValidateTypeScriptActivities(model)
	if len(model.Activities) == 0 && len(diagnostics) == 0 {
		diagnostics = append(diagnostics, Diagnostic{Message: "no TypeScript worker activities discovered"})
	}
	if len(diagnostics) > 0 {
		result.OK = false
		result.Diagnostics = diagnostics
		return result, DiagnosticsError(diagnostics)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return result, err
	}
	files := []struct {
		name string
		data []byte
	}{
		{name: "onlava.ts", data: executeTypeScriptTemplate("onlava.ts", tsOnlavaTemplate, nil)},
		{name: "registry.ts", data: executeTypeScriptTemplate("registry.ts", tsRegistryTemplate, registryData(opts, model.Activities))},
		{name: "worker.ts", data: executeTypeScriptTemplate("worker.ts", tsWorkerTemplate, tsWorkerData{})},
		{name: "tsconfig.json", data: executeTypeScriptTemplate("tsconfig.json", tsConfigTemplate, tsConfigData{})},
	}
	manifest, err := typeScriptManifest(opts, model.Activities)
	if err != nil {
		return result, err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	manifestData = append(manifestData, '\n')
	files = append(files, struct {
		name string
		data []byte
	}{name: "manifest.json", data: manifestData})
	for _, file := range files {
		path := filepath.Join(opts.OutputDir, file.name)
		if err := os.WriteFile(path, file.data, 0o644); err != nil {
			return result, err
		}
		result.Files = append(result.Files, BindingFile{Path: filepath.ToSlash(path), Language: "typescript", Manifest: filepath.ToSlash(filepath.Join(opts.OutputDir, "manifest.json"))})
	}
	slices.SortFunc(result.Files, func(a, b BindingFile) int {
		return strings.Compare(a.Path, b.Path)
	})
	return result, nil
}

func DiagnosticsError(diagnostics []Diagnostic) error {
	var lines []string
	for _, diag := range diagnostics {
		if strings.TrimSpace(diag.Path) != "" {
			lines = append(lines, diag.Path+": "+diag.Message)
			continue
		}
		lines = append(lines, diag.Message)
	}
	return errors.New(strings.Join(lines, "\n"))
}

func normalizeTypeScriptWorkerOptions(opts TypeScriptWorkerOptions) TypeScriptWorkerOptions {
	opts.AppRoot = strings.TrimSpace(opts.AppRoot)
	opts.AppName = strings.TrimSpace(opts.AppName)
	if strings.TrimSpace(opts.OutputDir) == "" {
		opts.OutputDir = filepath.Join(opts.AppRoot, TypeScriptWorkerGeneratedRelDir)
	}
	if strings.TrimSpace(opts.BuildID) == "" {
		opts.BuildID = "dev"
	}
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = "default"
	}
	if strings.TrimSpace(opts.PayloadCodec) == "" {
		opts.PayloadCodec = "onlava-json-v1"
	}
	return opts
}

func shouldSkipTypeScriptWorkerDir(appRoot, path string) bool {
	base := filepath.Base(path)
	switch base {
	case ".git", ".onlava", "node_modules", "dist", "out":
		return path != appRoot
	default:
		return false
	}
}

func parseTypeScriptWorkerFile(rel string, data []byte) ([]TypeScriptActivity, []Diagnostic) {
	matches := tsActivityCallRE.FindAllSubmatchIndex(data, -1)
	activities := make([]TypeScriptActivity, 0, len(matches))
	var diagnostics []Diagnostic
	for _, match := range matches {
		line := 1 + bytes.Count(data[:match[0]], []byte("\n"))
		exportName := strings.TrimSpace(string(data[match[2]:match[3]]))
		input := cleanTypeScriptType(string(data[match[4]:match[5]]))
		output := cleanTypeScriptType(string(data[match[6]:match[7]]))
		configStart := nextNonSpace(data, match[1])
		if configStart < 0 || configStart >= len(data) || data[configStart] != '{' {
			diagnostics = append(diagnostics, Diagnostic{Path: fmt.Sprintf("%s:%d", rel, line), Message: fmt.Sprintf("activity %s must pass a literal config object", exportName)})
			continue
		}
		configEnd, ok := balancedBlockEnd(data, configStart, '{', '}')
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Path: fmt.Sprintf("%s:%d", rel, line), Message: fmt.Sprintf("activity %s config object is not balanced", exportName)})
			continue
		}
		config := string(data[configStart : configEnd+1])
		name := stringField(config, "name")
		taskQueue := stringField(config, "taskQueue")
		maxConcurrency := intField(config, tsMaxConcurrency)
		activities = append(activities, TypeScriptActivity{
			ExportName:     exportName,
			Name:           name,
			TaskQueue:      taskQueue,
			Input:          input,
			Output:         output,
			File:           rel,
			Line:           line,
			MaxConcurrency: maxConcurrency,
		})
	}
	return activities, diagnostics
}

func nextNonSpace(data []byte, start int) int {
	for i := start; i < len(data); i++ {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return i
		}
	}
	return -1
}

func balancedBlockEnd(data []byte, start int, open, close byte) (int, bool) {
	depth := 0
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		next := byte(0)
		if i+1 < len(data) {
			next = data[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '/' && next == '/' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func stringField(config, field string) string {
	for offset := 0; offset < len(config); {
		index := strings.Index(config[offset:], field)
		if index < 0 {
			return ""
		}
		index += offset
		beforeOK := index == 0 || !isTSIdentifierByte(config[index-1])
		after := index + len(field)
		afterOK := after >= len(config) || !isTSIdentifierByte(config[after])
		if !beforeOK || !afterOK {
			offset = after
			continue
		}
		pos := skipTSWhitespace(config, after)
		if pos >= len(config) || config[pos] != ':' {
			offset = after
			continue
		}
		pos = skipTSWhitespace(config, pos+1)
		if pos >= len(config) {
			return ""
		}
		quote := config[pos]
		if quote != '\'' && quote != '"' && quote != '`' {
			return ""
		}
		var b strings.Builder
		escaped := false
		for i := pos + 1; i < len(config); i++ {
			ch := config[i]
			if escaped {
				b.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				return strings.TrimSpace(b.String())
			}
			b.WriteByte(ch)
		}
		return ""
	}
	return ""
}

func skipTSWhitespace(value string, pos int) int {
	for pos < len(value) {
		switch value[pos] {
		case ' ', '\t', '\r', '\n':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func isTSIdentifierByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '$'
}

func intField(config string, re *regexp.Regexp) int {
	match := re.FindStringSubmatch(config)
	if len(match) < 2 {
		return 0
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func cleanTypeScriptType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "readonly ")
	return value
}

func registryData(opts TypeScriptWorkerOptions, activities []TypeScriptActivity) tsRegistryData {
	outDir := opts.OutputDir
	imports := make([]tsRegistryImport, 0, len(activities))
	byQueue := make(map[string][]TypeScriptActivity)
	aliasCounts := make(map[string]int)
	for i, activity := range activities {
		alias := sanitizeIdentifier(activity.ExportName)
		aliasCounts[alias]++
		if aliasCounts[alias] > 1 {
			alias = fmt.Sprintf("%s_%d", alias, aliasCounts[alias])
		}
		activity.ImportAlias = alias
		activities[i].ImportAlias = alias
		sourcePath := filepath.Join(opts.AppRoot, activity.File)
		rel, err := filepath.Rel(outDir, sourcePath)
		if err != nil {
			rel = sourcePath
		}
		rel = strings.TrimSuffix(filepath.ToSlash(rel), ".ts")
		if !strings.HasPrefix(rel, ".") {
			rel = "./" + rel
		}
		imports = append(imports, tsRegistryImport{
			ExportName: activity.ExportName,
			Alias:      alias,
			Path:       rel,
		})
		byQueue[activity.TaskQueue] = append(byQueue[activity.TaskQueue], activity)
	}
	slices.SortFunc(imports, func(a, b tsRegistryImport) int {
		if cmp := strings.Compare(a.Path, b.Path); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ExportName, b.ExportName)
	})
	queues := make([]tsRegistryQueue, 0, len(byQueue))
	for queue, queueActivities := range byQueue {
		slices.SortFunc(queueActivities, func(a, b TypeScriptActivity) int {
			return strings.Compare(a.Name, b.Name)
		})
		queues = append(queues, tsRegistryQueue{
			Name:           queue,
			Activities:     queueActivities,
			MaxConcurrency: queueMaxConcurrency(queueActivities),
		})
	}
	slices.SortFunc(queues, func(a, b tsRegistryQueue) int {
		return strings.Compare(a.Name, b.Name)
	})
	return tsRegistryData{Imports: imports, Queues: queues}
}

func queueMaxConcurrency(activities []TypeScriptActivity) int {
	max := 0
	for _, activity := range activities {
		if activity.MaxConcurrency <= 0 {
			continue
		}
		if max == 0 || activity.MaxConcurrency < max {
			max = activity.MaxConcurrency
		}
	}
	return max
}

func typeScriptManifest(opts TypeScriptWorkerOptions, activities []TypeScriptActivity) (Manifest, error) {
	byQueue := make(map[string][]TypeScriptActivity)
	for _, activity := range activities {
		byQueue[activity.TaskQueue] = append(byQueue[activity.TaskQueue], activity)
	}
	taskQueues := make([]TaskQueue, 0, len(byQueue))
	for queue, queueActivities := range byQueue {
		names := make([]string, 0, len(queueActivities))
		for _, activity := range queueActivities {
			names = append(names, activity.Name)
		}
		slices.Sort(names)
		taskQueues = append(taskQueues, TaskQueue{
			Name:             queue,
			Activities:       names,
			RegistrationHash: registrationHash(queue, queueActivities),
		})
	}
	slices.SortFunc(taskQueues, func(a, b TaskQueue) int {
		return strings.Compare(a.Name, b.Name)
	})
	manifestActivities := make([]Activity, 0, len(activities))
	for _, activity := range activities {
		manifestActivities = append(manifestActivities, Activity{
			Name:   activity.Name,
			Input:  activity.Input,
			Output: activity.Output,
		})
	}
	slices.SortFunc(manifestActivities, func(a, b Activity) int {
		return strings.Compare(a.Name, b.Name)
	})
	return Manifest{
		SchemaVersion: ManifestSchemaVersionV2,
		App:           opts.AppName,
		Language:      "typescript",
		BuildID:       opts.BuildID,
		PayloadCodec:  opts.PayloadCodec,
		Temporal: ManifestTemporal{
			Namespace: opts.Namespace,
		},
		TaskQueues: taskQueues,
		Activities: manifestActivities,
	}, nil
}

func registrationHash(queue string, activities []TypeScriptActivity) string {
	lines := []string{
		"schema=onlava.worker.registration.v1",
		"language=typescript",
		"payload_codec=onlava-json-v1",
		"queue=" + queue,
	}
	slices.SortFunc(activities, func(a, b TypeScriptActivity) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, activity := range activities {
		lines = append(lines, "activity="+activity.Name+"|input="+activity.Input+"|output="+activity.Output)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func executeTypeScriptTemplate(name string, tmpl *template.Template, data any) []byte {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("execute %s template: %v", name, err))
	}
	return buf.Bytes()
}

func relativeWorkerPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func activityPosition(activity TypeScriptActivity) string {
	if activity.File == "" {
		return ""
	}
	if activity.Line <= 0 {
		return activity.File
	}
	return fmt.Sprintf("%s:%d", activity.File, activity.Line)
}

func externalPosition(decl ExternalActivityDeclaration) string {
	if decl.File == "" {
		return ""
	}
	if decl.Line <= 0 {
		return decl.File
	}
	return fmt.Sprintf("%s:%d", decl.File, decl.Line)
}

func canonicalTypeName(value string) string {
	value = strings.TrimSpace(value)
	for strings.HasPrefix(value, "*") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "*"))
	}
	value = strings.TrimPrefix(value, "[]")
	if index := strings.LastIndex(value, "."); index >= 0 {
		value = value[index+1:]
	}
	return strings.TrimSpace(value)
}

var tsOnlavaTemplate = template.Must(template.New("onlava.ts").Parse(`// Code generated by onlava; DO NOT EDIT.

import { Context } from "@temporalio/activity";

export type DurationString = string;

export type RetryPolicy = {
  initialInterval?: DurationString;
  backoffCoefficient?: number;
  maximumInterval?: DurationString;
  maximumAttempts?: number;
  nonRetryableErrorTypes?: string[];
};

export type ActivityConfig = {
  name: string;
  taskQueue: string;
  startToClose?: DurationString;
  heartbeatTimeout?: DurationString;
  scheduleToClose?: DurationString;
  maxConcurrency?: number;
  retry?: RetryPolicy;
};

export type ActivityContext = {
  activityName: string;
  taskQueue: string;
  heartbeat(details?: unknown): void;
  cancelled: AbortSignal;
  log: {
    info(message: string, attrs?: Record<string, unknown>): void;
    warn(message: string, attrs?: Record<string, unknown>): void;
    error(message: string, attrs?: Record<string, unknown>): void;
  };
};

export type ActivityHandler<I, O> = (ctx: ActivityContext, input: I) => Promise<O> | O;
export type ActivityImplementation<I, O> = ((input: I) => Promise<O>) & {
  __onlavaActivity?: { config: ActivityConfig };
};

export function activity<I, O>(config: ActivityConfig, handler: ActivityHandler<I, O>): ActivityImplementation<I, O> {
  const wrapped = async (input: I): Promise<O> => {
    const temporal = Context.current();
    const cancelled = new AbortController();
    const maybeCancelled = (temporal as unknown as { cancelled?: Promise<unknown> }).cancelled;
    if (maybeCancelled) {
      void maybeCancelled.then(() => cancelled.abort()).catch(() => cancelled.abort());
    }
    const ctx: ActivityContext = {
      activityName: config.name,
      taskQueue: config.taskQueue,
      heartbeat(details?: unknown) {
        temporal.heartbeat(details);
      },
      cancelled: cancelled.signal,
      log: {
        info(message: string, attrs?: Record<string, unknown>) {
          temporal.log.info(message, attrs);
        },
        warn(message: string, attrs?: Record<string, unknown>) {
          temporal.log.warn(message, attrs);
        },
        error(message: string, attrs?: Record<string, unknown>) {
          temporal.log.error(message, attrs);
        },
      },
    };
    return handler(ctx, input);
  };
  Object.defineProperty(wrapped, "__onlavaActivity", {
    value: { config },
    enumerable: false,
  });
  return wrapped as ActivityImplementation<I, O>;
}
`))

var tsRegistryTemplate = template.Must(template.New("registry.ts").Parse(`// Code generated by onlava; DO NOT EDIT.

{{- range .Imports }}
import { {{ .ExportName }} as {{ .Alias }} } from {{ printf "%q" .Path }};
{{- end }}

export const queues: Record<string, Record<string, (input: unknown) => Promise<unknown>>> = {
{{- range .Queues }}
  {{ printf "%q" .Name }}: {
  {{- range .Activities }}
    {{ printf "%q" .Name }}: {{ .ImportAlias }} as (input: unknown) => Promise<unknown>,
  {{- end }}
  },
{{- end }}
};

export const queueOptions: Record<string, { maxConcurrentActivityTaskExecutions?: number }> = {
{{- range .Queues }}
  {{ printf "%q" .Name }}: {
  {{- if gt .MaxConcurrency 0 }}
    maxConcurrentActivityTaskExecutions: {{ .MaxConcurrency }},
  {{- end }}
  },
{{- end }}
};
`))

var tsWorkerTemplate = template.Must(template.New("worker.ts").Parse(`// Code generated by onlava; DO NOT EDIT.

import { NativeConnection, Runtime, Worker, defaultPayloadConverter } from "@temporalio/worker";
import { queues, queueOptions } from "./registry";

const address = process.env.TEMPORAL_ADDRESS || "127.0.0.1:7233";
const namespace = process.env.TEMPORAL_NAMESPACE || "default";
const buildId = process.env.ONLAVA_BUILD_ID || "dev";
const deploymentName = sanitizeDeploymentName(process.env.ONLAVA_TEMPORAL_DEPLOYMENT_NAME || "onlv-typescript");
const taskQueuePrefix = process.env.ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX || "";
const appId = process.env.ONLAVA_APP_ID || "";
const sessionId = process.env.ONLAVA_SESSION_ID || "";
const appRootHash = process.env.ONLAVA_APP_ROOT_HASH || "";
const branch = process.env.ONLAVA_BRANCH || "";
const worktree = process.env.ONLAVA_WORKTREE || "";
const devReportURL = process.env.ONLAVA_DEV_REPORT_URL || "";
const devReportToken = process.env.ONLAVA_DEV_REPORT_TOKEN || "";
const appRoot = process.env.ONLAVA_APP_ROOT;
const supervisorPID = parsePositiveInteger(process.env.ONLAVA_DEV_SUPERVISOR_PID || "");

if (appRoot) {
  process.chdir(appRoot);
}

installTemporalRuntimeTelemetry();
installSignalExitFailsafe();
monitorSupervisorProcess(supervisorPID);

function selectedQueues(): string[] {
  const selected = process.env.ONLAVA_TEMPORAL_TASK_QUEUE;
  if (!selected) return Object.keys(queues);
  const wanted = selected.split(",").map((item) => item.trim()).filter(Boolean);
  for (const queue of wanted) {
    if (!(queue in queues)) {
      throw new Error(` + "`unknown TypeScript Temporal task queue ${queue}`" + `);
    }
  }
  return wanted;
}

function scopedTaskQueue(taskQueue: string): string {
  if (!sessionId) return taskQueue;
  const prefix = taskQueuePrefix.replace(/[.]+$/, "") || "onlava";
  if (taskQueue === prefix || taskQueue.startsWith(prefix + ".")) return taskQueue;
  const sanitized = taskQueue
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_.-]+/g, ".")
    .replace(/[.]+/g, ".")
    .replace(/^[._-]+|[._-]+$/g, "");
  return sanitized ? prefix + "." + sanitized : prefix;
}

function sanitizeDeploymentName(value: string): string {
  const sanitized = value
    .trim()
    .replace(/[^A-Za-z0-9_.-]+/g, "-")
    .replace(/[._-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return sanitized || "onlv-typescript";
}

function parsePositiveInteger(value: string): number {
  const parsed = Number.parseInt(value.trim(), 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return 0;
  return parsed;
}

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    const code = typeof error === "object" && error !== null ? (error as { code?: string }).code : "";
    return code === "EPERM";
  }
}

function monitorSupervisorProcess(pid: number): void {
  if (!pid) return;
  if (!processExists(pid)) {
    console.error("onlava TypeScript worker exiting because supervisor pid " + pid + " is not running");
    process.exit(0);
  }
  const timer: any = setInterval(() => {
    if (!processExists(pid)) {
      console.error("onlava TypeScript worker exiting because supervisor pid " + pid + " is not running");
      process.exit(0);
    }
  }, 1000);
  if (timer && typeof timer.unref === "function") {
    timer.unref();
  }
}

function installSignalExitFailsafe(): void {
  const once = (process as any).once;
  if (typeof once !== "function") return;
  for (const signal of ["SIGINT", "SIGTERM"]) {
    once.call(process, signal, () => {
      const timer: any = setTimeout(() => {
        console.error("onlava TypeScript worker force exiting after " + signal);
        process.exit(0);
      }, 5000);
      if (timer && typeof timer.unref === "function") {
        timer.unref();
      }
    });
  }
}

function installTemporalRuntimeTelemetry(): void {
  const metricsEndpoint = process.env.OTEL_EXPORTER_OTLP_METRICS_ENDPOINT || "";
  if (!metricsEndpoint) return;
  const globalTags: Record<string, string> = compactTags({
    onlava_app: appId,
    onlava_session_id: sessionId,
    onlava_language: "typescript",
  });
  Runtime.install({
    telemetryOptions: {
      metrics: {
        metricPrefix: "temporal_",
        globalTags,
        otel: {
          url: metricsEndpoint,
          http: true,
          metricsExportInterval: "1s",
          useSecondsForDurations: true,
        },
      },
    },
  });
}

function compactTags(tags: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(tags)) {
    if (value) out[key] = value;
  }
  return out;
}

function onlavaTemporalActivityInterceptor(sourceTaskQueue: string, taskQueue: string): any {
  return (activityContext: any) => ({
    inbound: {
      async execute(input: any, next: any): Promise<unknown> {
        const started = new Date();
        const parent = onlavaTemporalParentFromHeaders(input?.headers);
        const parentTraceId = parent?.trace_id;
        const parentSpanId = parent?.span_id;
        const traceId = validHex(parentTraceId, 32) ? parentTraceId : randomHex(16);
        const spanId = randomHex(8);
        const activityInfo = activityContext?.info || {};
        const activityName = String(activityInfo.activityType || "temporal.activity");
        let caught: unknown;
        try {
          return await next(input);
        } catch (error) {
          caught = error;
          throw error;
        } finally {
          await postTemporalActivityReport({
            traceId,
            spanId,
            parentSpanId: validHex(parentSpanId, 16) ? parentSpanId : "",
            started,
            durationNanos: Math.max(0, Date.now() - started.getTime()) * 1000000,
            activityName,
            sourceTaskQueue,
            taskQueue,
            workflowId: activityInfo.workflowExecution?.workflowId || "",
            runId: activityInfo.workflowExecution?.runId || "",
            activityId: activityInfo.activityId || "",
            attempt: activityInfo.attempt || 0,
            error: caught,
          });
        }
      },
    },
  });
}

function onlavaTemporalParentFromHeaders(headers: any): { trace_id?: string; span_id?: string } | undefined {
  const payload = headers?.[onlavaTemporalTraceHeader] ?? (typeof headers?.get === "function" ? headers.get(onlavaTemporalTraceHeader) : undefined);
  if (!payload) return undefined;
  try {
    const decoded = defaultPayloadConverter.fromPayload<Record<string, string>>(payload);
    return decoded || undefined;
  } catch {
    return undefined;
  }
}

const onlavaTemporalTraceHeader = "onlava-temporal-trace";

async function postTemporalActivityReport(input: {
  traceId: string;
  spanId: string;
  parentSpanId: string;
  started: Date;
  durationNanos: number;
  activityName: string;
  sourceTaskQueue: string;
  taskQueue: string;
  workflowId: string;
  runId: string;
  activityId: string;
  attempt: number;
  error: unknown;
}): Promise<void> {
  if (!appId || !devReportURL || !devReportToken) return;
  const isError = input.error !== undefined;
  const common = {
    app_id: appId,
    session_id: sessionId,
    app_root_hash: appRootHash,
    branch,
    worktree,
  };
  await postDevReport({
    ...common,
    type: "trace-summary",
    trace_summary: {
      trace_id: input.traceId,
      span_id: input.spanId,
      parent_span_id: input.parentSpanId || undefined,
      type: "TEMPORAL_ACTIVITY",
      is_root: !input.parentSpanId,
      is_error: isError,
      started_at: input.started.toISOString(),
      duration_nanos: input.durationNanos,
      service_name: "temporal",
      endpoint_name: input.activityName,
    },
  });
  await postDevReport({
    ...common,
    type: "log",
    log_event: {
      app_id: appId,
      session_id: sessionId,
      app_root_hash: appRootHash,
      branch,
      worktree,
      trace_id: input.traceId,
      span_id: input.spanId,
      level: isError ? "error" : "info",
      message: "temporal activity completed",
      timestamp: new Date().toISOString(),
      attrs: {
        temporal: true,
        temporal_operation: "RunActivity",
        temporal_name: input.activityName,
        temporal_task_queue: input.taskQueue,
        temporal_source_task_queue: input.sourceTaskQueue,
        temporal_workflow_id: input.workflowId,
        temporal_run_id: input.runId,
        temporal_activity_id: input.activityId,
        temporal_attempt: input.attempt,
        temporal_error: errorMessage(input.error),
      },
    },
  });
}

async function postDevReport(body: unknown): Promise<void> {
  try {
    const controller = new AbortController();
    const timer: any = setTimeout(() => controller.abort(), 1000);
    if (timer && typeof timer.unref === "function") timer.unref();
    const response = await fetch(devReportURL, {
      method: "POST",
      headers: {
        authorization: "Bearer " + devReportToken,
        "content-type": "application/json",
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    clearTimeout(timer);
    if (!response.ok) {
      console.error("onlava TypeScript worker dev report failed: " + response.status + " " + response.statusText);
    }
  } catch (error) {
    console.error("onlava TypeScript worker dev report failed: " + errorMessage(error));
  }
}

function randomHex(size: number): string {
  let out = "";
  for (let i = 0; i < size; i++) {
    out += Math.floor(Math.random() * 256).toString(16).padStart(2, "0");
  }
  return out;
}

function validHex(value: unknown, size: number): value is string {
  return typeof value === "string" && value.length === size && value !== "0".repeat(size) && /^[0-9a-f]+$/.test(value);
}

function errorMessage(error: unknown): string {
  if (error === undefined || error === null) return "";
  if (error instanceof Error) return error.message;
  return String(error);
}

const connection = await NativeConnection.connect({ address });
const workers = await Promise.all(
  selectedQueues().map((sourceTaskQueue) => {
    const taskQueue = scopedTaskQueue(sourceTaskQueue);
    return Worker.create({
      connection,
      namespace,
      taskQueue,
      activities: queues[sourceTaskQueue],
      interceptors: {
        activity: [onlavaTemporalActivityInterceptor(sourceTaskQueue, taskQueue)],
      },
      identity: ` + "`onlava:${deploymentName}:typescript:${taskQueue}:pid-${process.pid}:build-${buildId}`" + `,
      ...queueOptions[sourceTaskQueue],
      workerDeploymentOptions: {
        useWorkerVersioning: true,
        version: {
          deploymentName,
          buildId,
        },
        defaultVersioningBehavior: "PINNED",
      },
    });
  })
);
await Promise.all(workers.map((worker) => worker.run()));
`))

var tsConfigTemplate = template.Must(template.New("tsconfig.json").Parse(`{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "baseUrl": ".",
    "paths": {
      "onlava/worker": ["./onlava.ts"],
      "@onlava/temporal": ["./onlava.ts"]
    }
  }
}
`))
