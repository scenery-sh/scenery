package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestParseInspectArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseInspectArgs([]string{"routes", "--json", "--app-root", "/tmp/app"})
	if err != nil {
		t.Fatalf("parseInspectArgs returned error: %v", err)
	}
	if opts.Subject != "routes" {
		t.Fatalf("subject = %q", opts.Subject)
	}
	if !opts.JSON {
		t.Fatal("expected --json to be true")
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("app root = %q", opts.AppRoot)
	}

	if _, err := parseInspectArgs([]string{"data", "--json", "--database-url", "sqlite:///tmp/example.sqlite"}); err == nil || err.Error() != `unknown flag "--database-url"` {
		t.Fatalf("parseInspectArgs(data --database-url) error = %v", err)
	}
}

func TestRunSceneryInspectRequiresJSON(t *testing.T) {
	t.Parallel()

	err := runSceneryInspect([]string{"app"}, &bytes.Buffer{})
	if err == nil || err.Error() != "scenery inspect currently requires --json" {
		t.Fatalf("runSceneryInspect() error = %v", err)
	}
}

func TestRunSceneryInspectOutputsStableJSON(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"inspectapp","id":"inspect-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/inspectapp\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "users/api.go", `package users

import "context"

//scenery:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

//scenery:api public
func (*Service) Profile(context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "tenants/api.go", `package tenants

import "context"

//scenery:api private path=/tenants/config method=GET
func Config(context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "jobs/runtime.go", `package jobs

import (
	"context"

	"scenery.sh/temporal"
)

type In struct{}
type Out struct{}

var wf = temporal.NewWorkflow[In, Out]("jobs.Run/v1", temporal.WorkflowConfig{}, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{}, nil
})

var act = temporal.NewActivity[In, Out]("jobs.Do/v1", temporal.ActivityConfig{TaskQueue: "inspect.jobs.activities.go"}, func(ctx context.Context, in In) (Out, error) {
	return Out{}, nil
})
`)

	inspectArgs := func(subject string) []string {
		return []string{subject, "--json", "--app-root", root}
	}

	t.Run("app", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("app"), &out); err != nil {
			t.Fatalf("runSceneryInspect(app) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			App           struct {
				Name       string `json:"name"`
				ID         string `json:"id"`
				ModulePath string `json:"module_path"`
			} `json:"app"`
			Counts struct {
				Services  int `json:"services"`
				Endpoints int `json:"endpoints"`
			} `json:"counts"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(app) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.app.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if payload.App.Name != "inspectapp" || payload.App.ID != "inspect-id" {
			t.Fatalf("app = %+v", payload.App)
		}
		if payload.App.ModulePath != "example.com/inspectapp" {
			t.Fatalf("module_path = %q", payload.App.ModulePath)
		}
		if payload.Counts.Services != 2 || payload.Counts.Endpoints != 2 {
			t.Fatalf("counts = %+v", payload.Counts)
		}
	})

	t.Run("services", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("services"), &out); err != nil {
			t.Fatalf("runSceneryInspect(services) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Services      []struct {
				Name          string `json:"name"`
				ServiceStruct *struct {
					TypeName string `json:"type_name"`
					InitFunc string `json:"init_func"`
				} `json:"service_struct"`
			} `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(services) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.services.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if len(payload.Services) != 2 {
			t.Fatalf("services len = %d", len(payload.Services))
		}
		if payload.Services[1].Name != "users" || payload.Services[1].ServiceStruct == nil {
			t.Fatalf("users service = %+v", payload.Services[1])
		}
		if payload.Services[1].ServiceStruct.TypeName != "Service" || payload.Services[1].ServiceStruct.InitFunc != "initService" {
			t.Fatalf("service struct = %+v", payload.Services[1].ServiceStruct)
		}
	})

	t.Run("routes", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("routes"), &out); err != nil {
			t.Fatalf("runSceneryInspect(routes) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Routes        []struct {
				ID       string   `json:"id"`
				Access   string   `json:"access"`
				Path     string   `json:"path"`
				Methods  []string `json:"methods"`
				Receiver string   `json:"receiver"`
			} `json:"routes"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(routes) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.routes.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if len(payload.Routes) != 2 {
			t.Fatalf("routes len = %d", len(payload.Routes))
		}
		if payload.Routes[0].ID != "tenants.Config" || payload.Routes[0].Access != "private" || payload.Routes[0].Path != "/tenants/config" {
			t.Fatalf("route 0 = %+v", payload.Routes[0])
		}
		if payload.Routes[1].ID != "users.Profile" || payload.Routes[1].Receiver != "Service" {
			t.Fatalf("route 1 = %+v", payload.Routes[1])
		}
	})

	t.Run("build", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("build"), &out); err != nil {
			t.Fatalf("runSceneryInspect(build) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Build         struct {
				WorkspaceDir     string `json:"workspace_dir"`
				BuildStateExists bool   `json:"build_state_exists"`
			} `json:"build"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(build) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.build.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if payload.Build.WorkspaceDir == "" {
			t.Fatal("expected workspace_dir")
		}
	})

	t.Run("paths", func(t *testing.T) {
		cacheRoot := filepath.Join(t.TempDir(), "cache")
		t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("paths"), &out); err != nil {
			t.Fatalf("runSceneryInspect(paths) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Paths         struct {
				AppRoot        string `json:"app_root"`
				BuildStatePath string `json:"build_state_path"`
				CacheRoot      string `json:"cache_root"`
			} `json:"paths"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(paths) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.paths.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		gotRoot, err := filepath.EvalSymlinks(payload.Paths.AppRoot)
		if err != nil {
			t.Fatalf("EvalSymlinks(app_root): %v", err)
		}
		wantRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks(root): %v", err)
		}
		if gotRoot != wantRoot {
			t.Fatalf("app_root = %q, want %q", gotRoot, wantRoot)
		}
		if payload.Paths.BuildStatePath == "" || payload.Paths.CacheRoot != cacheRoot {
			t.Fatalf("paths = %+v", payload.Paths)
		}
	})

	t.Run("temporal", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("temporal"), &out); err != nil {
			t.Fatalf("runSceneryInspect(temporal) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Temporal      struct {
				Enabled          bool   `json:"enabled"`
				Mode             string `json:"mode"`
				Address          string `json:"address"`
				Namespace        string `json:"namespace"`
				TaskQueuePrefix  string `json:"task_queue_prefix"`
				DeploymentName   string `json:"deployment_name"`
				WorkerBuildID    string `json:"worker_build_id"`
				WorkerBuildIDSet bool   `json:"worker_build_id_set"`
				Versioning       string `json:"versioning"`
				HostReporting    bool   `json:"host_resource_reporting"`
				HostReportingEnv string `json:"host_resource_reporting_env"`
			} `json:"temporal"`
			Connectivity struct {
				Checked bool `json:"checked"`
			} `json:"connectivity"`
			WorkerManifests struct {
				Checked bool `json:"checked"`
				OK      bool `json:"ok"`
				Count   int  `json:"count"`
			} `json:"worker_manifests"`
			Declarations []struct {
				Kind              string `json:"kind"`
				Name              string `json:"name"`
				TaskQueue         string `json:"task_queue"`
				TaskQueueExplicit bool   `json:"task_queue_explicit"`
				File              string `json:"file"`
				Line              int    `json:"line"`
			} `json:"declarations"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(temporal) error = %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.temporal.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if payload.Temporal.Enabled {
			t.Fatalf("temporal unexpectedly enabled: %+v", payload.Temporal)
		}
		if payload.Temporal.Mode != "local" || payload.Temporal.Address != "127.0.0.1:7233" || payload.Temporal.Namespace != "default" {
			t.Fatalf("temporal defaults = %+v", payload.Temporal)
		}
		if payload.Temporal.TaskQueuePrefix != "scenery.inspectapp" {
			t.Fatalf("task_queue_prefix = %q", payload.Temporal.TaskQueuePrefix)
		}
		if payload.Temporal.DeploymentName != "scenery-inspectapp" || payload.Temporal.WorkerBuildID != "dev" || payload.Temporal.WorkerBuildIDSet {
			t.Fatalf("worker metadata = %+v", payload.Temporal)
		}
		if payload.Temporal.Versioning != "pinned" {
			t.Fatalf("versioning = %q", payload.Temporal.Versioning)
		}
		if !payload.Temporal.HostReporting || payload.Temporal.HostReportingEnv != "SCENERY_TEMPORAL_HOST_RESOURCE_REPORTING" {
			t.Fatalf("host resource reporting = %+v", payload.Temporal)
		}
		if payload.Connectivity.Checked {
			t.Fatalf("connectivity checked while disabled: %+v", payload.Connectivity)
		}
		if len(payload.Declarations) != 2 {
			t.Fatalf("declarations = %+v", payload.Declarations)
		}
		if payload.Declarations[0].Kind != "temporal_workflow" || payload.Declarations[0].Name != "jobs.Run/v1" || payload.Declarations[0].TaskQueue != "scenery.inspectapp.worker.go" || payload.Declarations[0].TaskQueueExplicit {
			t.Fatalf("workflow declaration = %+v", payload.Declarations[0])
		}
		if payload.Declarations[1].Kind != "temporal_activity" || payload.Declarations[1].Name != "jobs.Do/v1" || payload.Declarations[1].TaskQueue != "inspect.jobs.activities.go" || !payload.Declarations[1].TaskQueueExplicit {
			t.Fatalf("activity declaration = %+v", payload.Declarations[1])
		}
		if !payload.WorkerManifests.Checked || !payload.WorkerManifests.OK || payload.WorkerManifests.Count != 0 {
			t.Fatalf("worker_manifests = %+v", payload.WorkerManifests)
		}
	})
}

func TestRunSceneryInspectExcludesUnrelatedPackages(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "inspect-excludes-unrelated")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{"name":"inspectapp","id":"inspect-id"}`,
		"go.mod":        "module example.com/inspectapp\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRootForTest(t) + "\n",
		"users/api.go": `package users

import "context"

//scenery:service
type Service struct{}

//scenery:api public
func (*Service) Profile(context.Context) error { return nil }
`,
		"users/helpers/helper.go": `package helpers

func Helper() {}
`,
		"users/mw/mw.go": `package mw

import "scenery.sh/middleware"

//scenery:middleware target=all
func ServiceMW(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,

		"globalmw/mw.go": `package globalmw

import "scenery.sh/middleware"

//scenery:middleware global target=all
func Global(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,
		"jobs/runtime.go": `package jobs

import "scenery.sh/temporal"

type In struct{}
type Out struct{}

func workflowConfig() temporal.WorkflowConfig {
	return temporal.WorkflowConfig{TaskQueue: "custom.worker.go"}
}

var cfg = workflowConfig()

var _ = temporal.NewWorkflow[In, Out]("jobs.Run/v1", cfg, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{}, nil
})
`,
	})

	inspectArgs := func(subject string) []string {
		return []string{subject, "--json", "--app-root", root}
	}

	t.Run("app", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("app"), &out); err != nil {
			t.Fatalf("runSceneryInspect(app) error = %v", err)
		}
		var payload struct {
			Counts struct {
				Packages   int `json:"packages"`
				Services   int `json:"services"`
				Middleware int `json:"middleware"`
			} `json:"counts"`
			Services []string `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(app): %v\n%s", err, out.String())
		}
		if payload.Counts.Packages != 3 {
			t.Fatalf("packages = %d, want 3", payload.Counts.Packages)
		}
		if payload.Counts.Services != 1 || payload.Counts.Middleware != 2 {
			t.Fatalf("counts = %+v", payload.Counts)
		}
		if len(payload.Services) != 1 || payload.Services[0] != "users" {
			t.Fatalf("services = %+v", payload.Services)
		}
	})

	t.Run("services", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("services"), &out); err != nil {
			t.Fatalf("runSceneryInspect(services) error = %v", err)
		}
		var payload struct {
			Services []struct {
				Name        string   `json:"name"`
				PackageDirs []string `json:"package_dirs"`
			} `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(services): %v\n%s", err, out.String())
		}
		if len(payload.Services) != 1 {
			t.Fatalf("services len = %d", len(payload.Services))
		}
		got := payload.Services[0]
		if got.Name != "users" {
			t.Fatalf("service name = %q", got.Name)
		}
		wantDirs := []string{"users", "users/mw"}
		if len(got.PackageDirs) != len(wantDirs) {
			t.Fatalf("package_dirs = %+v, want %+v", got.PackageDirs, wantDirs)
		}
		for i := range wantDirs {
			if got.PackageDirs[i] != wantDirs[i] {
				t.Fatalf("package_dirs = %+v, want %+v", got.PackageDirs, wantDirs)
			}
		}
	})

	t.Run("temporal unresolved queue", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("temporal"), &out); err != nil {
			t.Fatalf("runSceneryInspect(temporal) error = %v", err)
		}
		var payload struct {
			Declarations []struct {
				Kind              string `json:"kind"`
				Name              string `json:"name"`
				TaskQueue         string `json:"task_queue"`
				TaskQueueExplicit bool   `json:"task_queue_explicit"`
			} `json:"declarations"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(temporal) error = %v\n%s", err, out.String())
		}
		if len(payload.Declarations) != 1 {
			t.Fatalf("declarations = %+v", payload.Declarations)
		}
		decl := payload.Declarations[0]
		if decl.Kind != "temporal_workflow" || decl.Name != "jobs.Run/v1" || decl.TaskQueue != "" || decl.TaskQueueExplicit {
			t.Fatalf("workflow declaration = %+v", decl)
		}
	})
}

func TestRunSceneryInspectStandardAuthSurfacesWithoutTenantService(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "authinspect",
  "auth": {
    "enabled": true,
    "dev_bootstrap": { "enabled": true }
  }
}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/authinspect\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "service/api.go", `package service

import (
	"context"

	"scenery.sh/auth"
)

type MeResponse struct {
	UserID string `+"`json:\"user_id\"`"+`
}

//scenery:api auth path=/me method=GET
func Me(ctx context.Context) (*MeResponse, error) {
	uid, _ := auth.UserID()
	return &MeResponse{UserID: string(uid)}, nil
}
`)

	inspectArgs := func(subject string) []string {
		return []string{subject, "--json", "--app-root", root}
	}

	t.Run("services", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("services"), &out); err != nil {
			t.Fatalf("runSceneryInspect(services) error = %v", err)
		}
		var payload struct {
			Services []struct {
				Name        string   `json:"name"`
				Endpoints   []string `json:"endpoints"`
				AuthHandler *struct {
					Name    string `json:"name"`
					Package string `json:"package"`
				} `json:"auth_handler"`
			} `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(services): %v\n%s", err, out.String())
		}

		services := map[string]struct {
			Endpoints   []string
			AuthHandler *struct {
				Name    string `json:"name"`
				Package string `json:"package"`
			}
		}{}
		for _, svc := range payload.Services {
			services[svc.Name] = struct {
				Endpoints   []string
				AuthHandler *struct {
					Name    string `json:"name"`
					Package string `json:"package"`
				}
			}{Endpoints: svc.Endpoints, AuthHandler: svc.AuthHandler}
		}
		if _, ok := services["tenants"]; ok {
			t.Fatalf("standard auth inspect should not require app-local tenants service: %+v", payload.Services)
		}
		if authSvc, ok := services["auth"]; !ok {
			t.Fatalf("auth service missing from inspect services: %+v", payload.Services)
		} else if authSvc.AuthHandler == nil || authSvc.AuthHandler.Name != "AuthHandler" || !stringSliceContains(authSvc.Endpoints, "Me") {
			t.Fatalf("auth service = %+v", authSvc)
		}
		if usersSvc, ok := services["users"]; !ok {
			t.Fatalf("users service missing from inspect services: %+v", payload.Services)
		} else if !stringSliceContains(usersSvc.Endpoints, "DevBootstrap") {
			t.Fatalf("users service = %+v", usersSvc)
		}
	})

	t.Run("routes", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("routes"), &out); err != nil {
			t.Fatalf("runSceneryInspect(routes) error = %v", err)
		}
		var payload struct {
			Routes []struct {
				ID      string `json:"id"`
				Service string `json:"service"`
				Path    string `json:"path"`
			} `json:"routes"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(routes): %v\n%s", err, out.String())
		}
		routes := map[string]string{}
		for _, route := range payload.Routes {
			if route.Service == "tenants" {
				t.Fatalf("standard auth inspect should not expose a tenants wrapper route: %+v", route)
			}
			routes[route.ID] = route.Path
		}
		if routes["auth.Me"] != "/auth/me" {
			t.Fatalf("auth.Me route = %q", routes["auth.Me"])
		}
		if routes["users.DevBootstrap"] != "/users/dev-bootstrap" {
			t.Fatalf("users.DevBootstrap route = %q", routes["users.DevBootstrap"])
		}
	})
}

func TestRunSceneryInspectReportsConfigJSONAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".config.json", `{"name":"aliasinspect","id":"alias-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/aliasinspect\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "svc/api.go", `package svc

import "context"

//scenery:service
type Service struct{}

//scenery:api public
func (*Service) Ping(context.Context) error { return nil }
`)

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"app", "--json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runSceneryInspect returned error: %v", err)
	}

	var payload struct {
		App struct {
			ConfigPath string `json:"config_path"`
		} `json:"app"`
		Config struct {
			Name string `json:"name"`
		} `json:"config"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode inspect app: %v\n%s", err, out.String())
	}
	if got, want := payload.App.ConfigPath, filepath.Join(root, ".config.json"); got != want {
		t.Fatalf("config_path = %q, want %q", got, want)
	}
	if payload.Config.Name != "aliasinspect" {
		t.Fatalf("config name = %q, want aliasinspect", payload.Config.Name)
	}
}

func TestRunSceneryInspectBuildUsesLatestManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"manifestapp","id":"manifest-id"}`)
	writeTestAppFile(t, root, ".scenery/build/latest.json", `{
  "schema_version": "scenery.build.latest.v1",
  "app": {
    "name": "manifestapp",
    "id": "manifest-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".scenery.json")+`"
  },
  "build": {
    "phase": "compiled",
    "workspace_dir": "/tmp/custom-workspace",
    "binary_path": "/tmp/custom-workspace/scenery-app",
    "workspace_exists": true,
    "binary_exists": true,
    "build_state_path": "/tmp/custom-workspace/.scenery-build-state.json",
    "build_state_exists": true,
    "build_state_version": "3",
    "dependency_fingerprint": "dep123",
    "graph_fingerprint": "graph123",
    "metadata_present": true,
    "api_encoding_present": true,
    "source_file_count": 12,
    "generated_file_count": 3
  }
}`)

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"build", "--json", "--app-root", root}, &out); err != nil {
		t.Fatalf("runSceneryInspect(build) error = %v", err)
	}
	var payload struct {
		Build struct {
			WorkspaceDir          string `json:"workspace_dir"`
			DependencyFingerprint string `json:"dependency_fingerprint"`
			GeneratedFileCount    int    `json:"generated_file_count"`
		} `json:"build"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(build manifest) error = %v\n%s", err, out.String())
	}
	if payload.Build.WorkspaceDir != "/tmp/custom-workspace" {
		t.Fatalf("workspace_dir = %q", payload.Build.WorkspaceDir)
	}
	if payload.Build.DependencyFingerprint != "dep123" || payload.Build.GeneratedFileCount != 3 {
		t.Fatalf("build payload = %+v", payload.Build)
	}
}

func TestRunSceneryInspectUsesGeneratedArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"genapp","id":"gen-id"}`)
	writeTestAppFile(t, root, ".scenery/gen/app.json", `{
  "schema_version": "scenery.inspect.app.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".scenery.json")+`",
    "module_path": "example.com/from-artifact"
  },
  "config": {
    "name": "genapp",
    "id": "gen-id",
    "proxy": {},
    "observability": {
      "logs": {},
      "tracing": {}
    }
  },
  "counts": {
    "packages": 7,
    "services": 3,
    "endpoints": 9,
    "middleware": 2,
    "auth_handler": 1
  },
  "services": ["alpha", "beta", "gamma"],
  "auth_handler": {
    "service": "alpha",
    "name": "AuthHandler"
  }
}`)
	writeTestAppFile(t, root, ".scenery/gen/routes.json", `{
  "schema_version": "scenery.inspect.routes.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".scenery.json")+`"
  },
  "routes": [
    {
      "id": "alpha.Ping",
      "service": "alpha",
      "endpoint": "Ping",
      "package": "alpha",
      "file": "alpha/api.go",
      "access": "public",
      "raw": false,
      "path": "/alpha.Ping",
      "methods": ["GET"],
      "has_payload": false
    }
 ]
}`)
	writeTestAppFile(t, root, ".scenery/gen/endpoints.json", `{
  "schema_version": "scenery.inspect.endpoints.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".scenery.json")+`"
  },
  "endpoints": [
    {
      "id": "alpha.Ping",
      "service": "alpha",
      "endpoint": "Ping",
      "access": "public",
      "raw": false,
      "path": "/alpha.Ping",
      "methods": ["GET"],
      "has_payload": false,
      "wire": {
        "available": true,
        "schema_hash": "wire-ping",
        "path": "/_wire/alpha.Ping"
      }
    }
  ],
  "wire": {
    "wire_schema_hash": "wire-root",
    "available": 1,
    "unsupported": 0
  }
}`)
	writeTestAppFile(t, root, ".scenery/gen/wire/capabilities.json", `{
  "schema_version": "scenery.wire.capabilities.v1",
  "wire_schema_hash": "wire-root",
  "content_type": "application/vnd.scenery.wire+bin",
  "endpoints": {
    "alpha.Ping": {
      "id": "alpha.Ping",
      "service": "alpha",
      "endpoint": "Ping",
      "path": "/alpha.Ping",
      "methods": ["GET"],
      "available": true,
      "schema_hash": "wire-ping",
      "safe_json_retry": true,
      "wire_path": "/_wire/alpha.Ping",
      "recovery_path_pattern": "/_wire/recover/{call_id}"
    }
  }
}`)
	writeTestAppFile(t, root, ".scenery/gen/services.json", `{
  "schema_version": "scenery.inspect.services.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".scenery.json")+`"
  },
  "services": [
    {
      "name": "alpha",
      "root_rel_dir": "alpha",
      "root_abs_dir": "`+filepath.Join(root, "alpha")+`",
      "package_dirs": ["alpha"],
      "endpoints": ["Ping"],
      "middleware": [],
      "service_struct": {
        "type_name": "Service"
      }
    },
    {
      "name": "docs",
      "root_rel_dir": "docs",
      "root_abs_dir": "`+filepath.Join(root, "docs")+`",
      "package_dirs": ["docs"],
      "endpoints": [],
      "middleware": []
    },
    {
      "name": "pkg",
      "root_rel_dir": "pkg",
      "root_abs_dir": "`+filepath.Join(root, "pkg")+`",
      "package_dirs": ["pkg"],
      "endpoints": [],
      "middleware": []
    }
  ]
}`)

	inspectArgs := func(subject string) []string {
		return []string{subject, "--json", "--app-root", root}
	}

	t.Run("app", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("app"), &out); err != nil {
			t.Fatalf("runSceneryInspect(app) error = %v", err)
		}
		var payload struct {
			App struct {
				ModulePath string `json:"module_path"`
			} `json:"app"`
			Counts struct {
				Packages int `json:"packages"`
				Services int `json:"services"`
			} `json:"counts"`
			Services []string `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(app artifact): %v\n%s", err, out.String())
		}
		if payload.App.ModulePath != "example.com/from-artifact" || payload.Counts.Packages != 1 || payload.Counts.Services != 1 {
			t.Fatalf("app payload = %+v %+v", payload.App, payload.Counts)
		}
		if len(payload.Services) != 1 || payload.Services[0] != "alpha" {
			t.Fatalf("app services = %+v", payload.Services)
		}
	})

	t.Run("routes", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("routes"), &out); err != nil {
			t.Fatalf("runSceneryInspect(routes) error = %v", err)
		}
		var payload struct {
			Routes []struct {
				ID   string `json:"id"`
				File string `json:"file"`
			} `json:"routes"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(routes artifact): %v\n%s", err, out.String())
		}
		if len(payload.Routes) != 1 || payload.Routes[0].ID != "alpha.Ping" || payload.Routes[0].File != "alpha/api.go" {
			t.Fatalf("routes payload = %+v", payload.Routes)
		}
	})

	t.Run("services", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("services"), &out); err != nil {
			t.Fatalf("runSceneryInspect(services) error = %v", err)
		}
		var payload struct {
			Services []struct {
				Name string `json:"name"`
			} `json:"services"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(services artifact): %v\n%s", err, out.String())
		}
		if len(payload.Services) != 1 || payload.Services[0].Name != "alpha" {
			t.Fatalf("services payload = %+v", payload.Services)
		}
	})

	t.Run("endpoints", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("endpoints"), &out); err != nil {
			t.Fatalf("runSceneryInspect(endpoints) error = %v", err)
		}
		var payload struct {
			Wire struct {
				SchemaHash string `json:"wire_schema_hash"`
			} `json:"wire"`
			Endpoints []struct {
				ID   string `json:"id"`
				Wire struct {
					Available bool   `json:"available"`
					Path      string `json:"path"`
				} `json:"wire"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(endpoints artifact): %v\n%s", err, out.String())
		}
		if payload.Wire.SchemaHash != "wire-root" || len(payload.Endpoints) != 1 || payload.Endpoints[0].ID != "alpha.Ping" || !payload.Endpoints[0].Wire.Available {
			t.Fatalf("endpoints payload = %+v", payload)
		}
	})

	t.Run("wire", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("wire"), &out); err != nil {
			t.Fatalf("runSceneryInspect(wire) error = %v", err)
		}
		var payload struct {
			SchemaHash string `json:"wire_schema_hash"`
			Endpoints  map[string]struct {
				Available bool `json:"available"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(wire artifact): %v\n%s", err, out.String())
		}
		if payload.SchemaHash != "wire-root" || !payload.Endpoints["alpha.Ping"].Available {
			t.Fatalf("wire payload = %+v", payload)
		}
	})
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}
