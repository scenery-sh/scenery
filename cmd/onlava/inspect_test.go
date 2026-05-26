package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestParseInspectArgs(t *testing.T) {
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

	opts, err = parseInspectArgs([]string{"data", "--json", "--database-url", "postgres://example", "--tenant", "acme", "--object", "company"})
	if err != nil {
		t.Fatalf("parseInspectArgs(data) returned error: %v", err)
	}
	if opts.Subject != "data" || !opts.JSON {
		t.Fatalf("data opts = %+v", opts)
	}
	if opts.Data.DatabaseURL != "postgres://example" || opts.Data.TenantKey != "acme" || opts.Data.ObjectName != "company" {
		t.Fatalf("data opts = %+v", opts.Data)
	}
}

func TestRunOnlavaInspectRequiresJSON(t *testing.T) {
	err := runOnlavaInspect([]string{"app"}, &bytes.Buffer{})
	if err == nil || err.Error() != "onlava inspect currently requires --json" {
		t.Fatalf("runOnlavaInspect() error = %v", err)
	}
}

func TestRunOnlavaInspectOutputsStableJSON(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"inspectapp","id":"inspect-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/inspectapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "users/api.go", `package users

import "context"

//onlava:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

//onlava:api public
func (*Service) Profile(context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "tenants/api.go", `package tenants

import "context"

//onlava:api private path=/tenants/config method=GET
func Config(context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "jobs/runtime.go", `package jobs

import (
	"context"

	"github.com/pbrazdil/onlava/temporal"
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

	restore := chdirForTest(t, root)
	defer restore()

	t.Run("app", func(t *testing.T) {
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"app", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(app) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.app.v1" {
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"services", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(services) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.services.v1" {
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"routes", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(routes) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.routes.v1" {
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"build", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(build) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.build.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if payload.Build.WorkspaceDir == "" {
			t.Fatal("expected workspace_dir")
		}
	})

	t.Run("paths", func(t *testing.T) {
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"paths", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(paths) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.paths.v1" {
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"temporal", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(temporal) error = %v", err)
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
		if payload.SchemaVersion != "onlava.inspect.temporal.v1" {
			t.Fatalf("schema_version = %q", payload.SchemaVersion)
		}
		if payload.Temporal.Enabled {
			t.Fatalf("temporal unexpectedly enabled: %+v", payload.Temporal)
		}
		if payload.Temporal.Mode != "local" || payload.Temporal.Address != "127.0.0.1:7233" || payload.Temporal.Namespace != "default" {
			t.Fatalf("temporal defaults = %+v", payload.Temporal)
		}
		if payload.Temporal.TaskQueuePrefix != "onlava.inspectapp" {
			t.Fatalf("task_queue_prefix = %q", payload.Temporal.TaskQueuePrefix)
		}
		if payload.Temporal.DeploymentName != "onlava-inspectapp" || payload.Temporal.WorkerBuildID != "dev" || payload.Temporal.WorkerBuildIDSet {
			t.Fatalf("worker metadata = %+v", payload.Temporal)
		}
		if payload.Temporal.Versioning != "pinned" {
			t.Fatalf("versioning = %q", payload.Temporal.Versioning)
		}
		if payload.Connectivity.Checked {
			t.Fatalf("connectivity checked while disabled: %+v", payload.Connectivity)
		}
		if len(payload.Declarations) != 2 {
			t.Fatalf("declarations = %+v", payload.Declarations)
		}
		if payload.Declarations[0].Kind != "temporal_workflow" || payload.Declarations[0].Name != "jobs.Run/v1" || payload.Declarations[0].TaskQueue != "onlava.inspectapp.worker.go" || payload.Declarations[0].TaskQueueExplicit {
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

func TestRunOnlavaInspectExcludesUnrelatedPackages(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"inspectapp","id":"inspect-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/inspectapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "users/api.go", `package users

import "context"

//onlava:service
type Service struct{}

//onlava:api public
func (*Service) Profile(context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "users/helpers/helper.go", `package helpers

func Helper() {}
`)
	writeTestAppFile(t, root, "users/mw/mw.go", `package mw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware target=all
func ServiceMW(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`)

	writeTestAppFile(t, root, "globalmw/mw.go", `package globalmw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware global target=all
func Global(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`)

	restore := chdirForTest(t, root)
	defer restore()

	t.Run("app", func(t *testing.T) {
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"app", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(app) error = %v", err)
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"services", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(services) error = %v", err)
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
}

func TestInspectTemporalLeavesUnresolvedWorkflowQueueEmpty(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"inspectapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/inspectapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "svc/api.go", `package svc

import "context"

//onlava:api public
func Ping(ctx context.Context) error { return nil }
`)
	writeTestAppFile(t, root, "jobs/runtime.go", `package jobs

import "github.com/pbrazdil/onlava/temporal"

type In struct{}
type Out struct{}

func workflowConfig() temporal.WorkflowConfig {
	return temporal.WorkflowConfig{TaskQueue: "custom.worker.go"}
}

var cfg = workflowConfig()

var _ = temporal.NewWorkflow[In, Out]("jobs.Run/v1", cfg, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{}, nil
})
`)

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaInspect([]string{"temporal", "--json"}, &out); err != nil {
		t.Fatalf("runOnlavaInspect(temporal) error = %v", err)
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
}

func TestRunOnlavaInspectBuildUsesLatestManifest(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"manifestapp","id":"manifest-id"}`)
	writeTestAppFile(t, root, ".onlava/build/latest.json", `{
  "schema_version": "onlava.build.latest.v1",
  "app": {
    "name": "manifestapp",
    "id": "manifest-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".onlava.json")+`"
  },
  "build": {
    "phase": "compiled",
    "workspace_dir": "/tmp/custom-workspace",
    "binary_path": "/tmp/custom-workspace/onlava-app",
    "workspace_exists": true,
    "binary_exists": true,
    "build_state_path": "/tmp/custom-workspace/.onlava-build-state.json",
    "build_state_exists": true,
    "build_state_version": "2",
    "dependency_fingerprint": "dep123",
    "graph_fingerprint": "graph123",
    "metadata_present": true,
    "api_encoding_present": true,
    "source_file_count": 12,
    "generated_file_count": 3
  }
}`)

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaInspect([]string{"build", "--json"}, &out); err != nil {
		t.Fatalf("runOnlavaInspect(build) error = %v", err)
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

func TestRunOnlavaInspectUsesGeneratedArtifacts(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"genapp","id":"gen-id"}`)
	writeTestAppFile(t, root, ".onlava/gen/app.json", `{
  "schema_version": "onlava.inspect.app.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".onlava.json")+`",
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
	writeTestAppFile(t, root, ".onlava/gen/routes.json", `{
  "schema_version": "onlava.inspect.routes.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".onlava.json")+`"
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
	writeTestAppFile(t, root, ".onlava/gen/endpoints.json", `{
  "schema_version": "onlava.inspect.endpoints.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".onlava.json")+`"
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
	writeTestAppFile(t, root, ".onlava/gen/wire/capabilities.json", `{
  "schema_version": "onlava.wire.capabilities.v1",
  "wire_schema_hash": "wire-root",
  "content_type": "application/vnd.onlava.wire+bin",
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
	writeTestAppFile(t, root, ".onlava/gen/services.json", `{
  "schema_version": "onlava.inspect.services.v1",
  "app": {
    "name": "genapp",
    "id": "gen-id",
    "root": "`+root+`",
    "config_path": "`+filepath.Join(root, ".onlava.json")+`"
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

	restore := chdirForTest(t, root)
	defer restore()

	t.Run("app", func(t *testing.T) {
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"app", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(app) error = %v", err)
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"routes", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(routes) error = %v", err)
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"services", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(services) error = %v", err)
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"endpoints", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(endpoints) error = %v", err)
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
		var out bytes.Buffer
		if err := runOnlavaInspect([]string{"wire", "--json"}, &out); err != nil {
			t.Fatalf("runOnlavaInspect(wire) error = %v", err)
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
