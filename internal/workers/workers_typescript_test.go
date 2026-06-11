package workers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverTypeScriptActivitiesFromWorkerFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTSWorkerFile(t, root, "house/preview.worker.ts", `import { activity } from "scenery/worker";

export type RenderRoofPreviewInput = { project_id: string; scene_id: string };
export type RenderRoofPreviewOutput = { preview_url: string };

export const renderRoofPreview = activity<RenderRoofPreviewInput, RenderRoofPreviewOutput>({
  name: "house.RenderRoofPreview/v1",
  taskQueue: "onlv.house.preview.ts",
  startToClose: "2m",
  maxConcurrency: 4,
}, async (ctx, input) => {
  ctx.heartbeat({ stage: "started" });
  return { preview_url: input.scene_id };
});
`)
	writeTSWorkerFile(t, root, "maps/earth.ts", `//scenery:worker
import { activity } from "@scenery/temporal";

export type NormalizeEarthMetadataInput = { scene_id: string };
export type NormalizeEarthMetadataOutput = { scene_id: string };

export const normalizeEarthMetadata = activity<NormalizeEarthMetadataInput, NormalizeEarthMetadataOutput>({
  name: "maps.NormalizeEarthMetadata/v1",
  taskQueue: "onlv.maps.earth.ts",
}, async (_ctx, input) => input);
`)
	writeTSWorkerFile(t, root, "node_modules/ignored/ignored.worker.ts", `export const ignored = activity<I, O>({ name: "ignored.Ignored/v1", taskQueue: "ignored" }, async () => ({}));`)
	// Copies inside .claude or nested git checkouts (e.g. agent worktrees) must
	// not surface as duplicate activities.
	writeTSWorkerFile(t, root, ".claude/worktrees/other/house/preview.worker.ts", `export const renderRoofPreview = activity<I, O>({ name: "house.RenderRoofPreview/v1", taskQueue: "onlv.house.preview.ts" }, async () => ({}));`)
	writeTSWorkerFile(t, root, "vendor-checkout/house/preview.worker.ts", `export const renderRoofPreview = activity<I, O>({ name: "house.RenderRoofPreview/v1", taskQueue: "onlv.house.preview.ts" }, async () => ({}));`)
	if err := os.WriteFile(filepath.Join(root, "vendor-checkout", ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := DiscoverTypeScriptActivities(root)
	if len(model.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", model.Diagnostics)
	}
	if len(model.Activities) != 2 {
		t.Fatalf("activities = %#v", model.Activities)
	}
	first := model.Activities[0]
	if first.ExportName != "renderRoofPreview" || first.Name != "house.RenderRoofPreview/v1" || first.TaskQueue != "onlv.house.preview.ts" || first.Input != "RenderRoofPreviewInput" || first.Output != "RenderRoofPreviewOutput" || first.MaxConcurrency != 4 {
		t.Fatalf("first activity = %#v", first)
	}
}

func TestGenerateTypeScriptWorkerWritesRegistryWorkerAndManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTSWorkerFile(t, root, "house/preview.worker.ts", `import { activity } from "scenery/worker";
export type RenderRoofPreviewInput = { project_id: string };
export type RenderRoofPreviewOutput = { preview_url: string };
export const renderRoofPreview = activity<RenderRoofPreviewInput, RenderRoofPreviewOutput>({
  name: "house.RenderRoofPreview/v1",
  taskQueue: "onlv.house.preview.ts",
  maxConcurrency: 4
}, async (_ctx, input) => ({ preview_url: input.project_id }));
`)
	writeTSWorkerFile(t, root, "maps/earth.worker.ts", `import { activity } from "scenery/worker";
export type NormalizeEarthMetadataInput = { scene_id: string };
export type NormalizeEarthMetadataOutput = { scene_id: string };
export const normalizeEarthMetadata = activity<NormalizeEarthMetadataInput, NormalizeEarthMetadataOutput>({
  name: "maps.NormalizeEarthMetadata/v1",
  taskQueue: "onlv.maps.earth.ts"
}, async (_ctx, input) => input);
`)

	result, err := GenerateTypeScriptWorker(TypeScriptWorkerOptions{
		AppRoot:   root,
		AppName:   "onlvnext-o5o2",
		BuildID:   "dev",
		Namespace: "default",
	})
	if err != nil {
		t.Fatalf("GenerateTypeScriptWorker returned error: %v", err)
	}
	if !result.OK || len(result.Files) != 6 {
		t.Fatalf("result = %#v", result)
	}
	outDir := filepath.Join(root, TypeScriptWorkerGeneratedRelDir)
	packageJSON, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	for _, want := range []string{
		`"@temporalio/activity": "1.17.2"`,
		`"@temporalio/worker": "1.17.2"`,
		`"tsx": "4.20.6"`,
	} {
		if !strings.Contains(string(packageJSON), want) {
			t.Fatalf("package.json missing %q:\n%s", want, packageJSON)
		}
	}
	registry, err := os.ReadFile(filepath.Join(outDir, "registry.ts"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	registryText := string(registry)
	for _, want := range []string{
		`import { renderRoofPreview as renderroofpreview } from "../../../../house/preview.worker";`,
		`"onlv.house.preview.ts": {`,
		`"house.RenderRoofPreview/v1": renderroofpreview`,
		`maxConcurrentActivityTaskExecutions: 4`,
	} {
		if !strings.Contains(registryText, want) {
			t.Fatalf("registry missing %q:\n%s", want, registryText)
		}
	}
	worker, err := os.ReadFile(filepath.Join(outDir, "worker.ts"))
	if err != nil {
		t.Fatalf("read worker: %v", err)
	}
	for _, want := range []string{
		`Worker.create`,
		`SCENERY_TEMPORAL_TASK_QUEUE`,
		`function sanitizeDeploymentName`,
		`SCENERY_DEV_SUPERVISOR_PID`,
		`function monitorSupervisorProcess`,
		`process.kill(pid, 0)`,
		`setInterval(() => {`,
		`supervisor pid `,
		`process.exit(0)`,
		`function installSignalExitFailsafe`,
		`Runtime.install`,
		`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`,
		`SCENERY_DEV_REPORT_URL`,
		`sceneryTemporalActivityInterceptor`,
		`scenery-temporal-trace`,
	} {
		if !strings.Contains(string(worker), want) {
			t.Fatalf("worker missing %q:\n%s", want, worker)
		}
	}
	if !strings.Contains(string(worker), `Worker.create`) {
		t.Fatalf("worker content:\n%s", worker)
	}
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest): %v\n%s", err, data)
	}
	manifest.Path = filepath.Join(outDir, "manifest.json")
	if diagnostics := ValidateManifest(manifest, "onlvnext-o5o2"); len(diagnostics) != 0 {
		t.Fatalf("manifest diagnostics = %#v\n%s", diagnostics, data)
	}
	if manifest.SchemaVersion != ManifestSchemaVersionV2 || manifest.Language != "typescript" || manifest.PayloadCodec != "scenery-json-v1" || len(manifest.TaskQueues) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestValidateTypeScriptContractsMatchesExternalActivities(t *testing.T) {
	t.Parallel()

	ts := TypeScriptWorkerModel{Activities: []TypeScriptActivity{{
		ExportName: "renderRoofPreview",
		Name:       "house.RenderRoofPreview/v1",
		TaskQueue:  "onlv.house.preview.ts",
		Input:      "RenderRoofPreviewInput",
		Output:     "RenderRoofPreviewOutput",
		File:       "house/preview.worker.ts",
		Line:       4,
	}}}
	externals := []ExternalActivityDeclaration{{
		Name:      "house.RenderRoofPreview/v1",
		TaskQueue: "onlv.house.preview.ts",
		Input:     "*RenderRoofPreviewInput",
		Output:    "*RenderRoofPreviewOutput",
		File:      "house/process_async.go",
		Line:      12,
		Kind:      "temporal_external_activity",
	}}
	if diagnostics := ValidateTypeScriptContracts(ts, externals, nil); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	externals[0].TaskQueue = "other.queue"
	diagnostics := ValidateTypeScriptContracts(ts, externals, nil)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "other.queue") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestValidateTypeScriptTaskQueuesRejectsUnknownSelection(t *testing.T) {
	t.Parallel()

	activities := []TypeScriptActivity{{Name: "house.Render/v1", TaskQueue: "onlv.house.preview.ts"}}
	diagnostics := ValidateTypeScriptTaskQueues(activities, []string{"onlv.house.preview.ts", "missing.ts"})
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "missing.ts") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func writeTSWorkerFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
