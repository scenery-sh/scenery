package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSceneryInspectOutputsModelDSLJSON(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl")
	inspectArgs := func(subject string) []string {
		return []string{subject, "--json", "--app-root", root}
	}

	t.Run("models", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("models"), &out); err != nil {
			t.Fatalf("runSceneryInspect(models) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Models        []struct {
				Name   string `json:"name"`
				Table  string `json:"table"`
				Fields []struct {
					Name       string   `json:"name"`
					Kind       string   `json:"kind"`
					Column     string   `json:"column"`
					Filterable bool     `json:"filterable"`
					EnumValues []string `json:"enum_values"`
				} `json:"fields"`
			} `json:"models"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(models): %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.models.v1" || len(payload.Models) != 1 {
			t.Fatalf("models payload = %+v", payload)
		}
		if payload.Models[0].Name != "Task" || payload.Models[0].Table != "tasks" {
			t.Fatalf("model = %+v", payload.Models[0])
		}
		var statusField *struct {
			Name       string   `json:"name"`
			Kind       string   `json:"kind"`
			Column     string   `json:"column"`
			Filterable bool     `json:"filterable"`
			EnumValues []string `json:"enum_values"`
		}
		for i := range payload.Models[0].Fields {
			if payload.Models[0].Fields[i].Name == "Status" {
				statusField = &payload.Models[0].Fields[i]
			}
		}
		if statusField == nil || statusField.Kind != "stored" || statusField.Column != "status" || !statusField.Filterable || strings.Join(statusField.EnumValues, ",") != "todo,doing,done" {
			t.Fatalf("status field = %+v", statusField)
		}
	})

	t.Run("views", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("views"), &out); err != nil {
			t.Fatalf("runSceneryInspect(views) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Views         []struct {
				Name           string   `json:"name"`
				Kind           string   `json:"kind"`
				Entity         string   `json:"entity"`
				Route          string   `json:"route"`
				Columns        []string `json:"columns"`
				ColumnDisplays []struct {
					Field string `json:"field"`
					Kind  string `json:"kind"`
				} `json:"column_displays"`
				Filters []struct {
					Field  string `json:"field"`
					Column string `json:"column"`
					Op     string `json:"op"`
					Value  string `json:"value"`
				} `json:"filters"`
				Sorts []struct {
					Field     string `json:"field"`
					Column    string `json:"column"`
					Direction string `json:"direction"`
				} `json:"sorts"`
				Slots []struct {
					Name string `json:"name"`
				} `json:"slots"`
			} `json:"views"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(views): %v\n%s", err, out.String())
		}
		if payload.SchemaVersion != "scenery.inspect.views.v1" || len(payload.Views) != 1 {
			t.Fatalf("views payload = %+v", payload)
		}
		view := payload.Views[0]
		if view.Name != "TaskList" || view.Kind != "collection" || view.Entity != "Task" || view.Route != "/tasks" || strings.Join(view.Columns, ",") != "Title,Status,Priority,Assignee,DueAt,CreatedAt,UpdatedAt" || len(view.Slots) != 1 || view.Slots[0].Name != "TaskStatusBadge" {
			t.Fatalf("view = %+v", view)
		}
		if len(view.ColumnDisplays) != 5 || view.ColumnDisplays[0].Field != "Status" || view.ColumnDisplays[0].Kind != "badge" {
			t.Fatalf("column displays = %+v", view.ColumnDisplays)
		}
		if len(view.Filters) != 1 || view.Filters[0].Field != "Status" || view.Filters[0].Column != "status" || view.Filters[0].Op != "neq" || view.Filters[0].Value != "done" {
			t.Fatalf("filters = %+v", view.Filters)
		}
		if len(view.Sorts) != 2 || view.Sorts[0].Field != "DueAt" || view.Sorts[0].Column != "due_at" || view.Sorts[0].Direction != "asc" || view.Sorts[1].Field != "CreatedAt" || view.Sorts[1].Column != "created_at" || view.Sorts[1].Direction != "desc" {
			t.Fatalf("sorts = %+v", view.Sorts)
		}
	})

	t.Run("generated endpoints", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := runSceneryInspect(inspectArgs("endpoints"), &out); err != nil {
			t.Fatalf("runSceneryInspect(endpoints) error = %v", err)
		}
		var payload struct {
			SchemaVersion string `json:"schema_version"`
			Endpoints     []struct {
				ID        string   `json:"id"`
				Access    string   `json:"access"`
				Path      string   `json:"path"`
				Methods   []string `json:"methods"`
				Generated bool     `json:"generated"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal(endpoints): %v\n%s", err, out.String())
		}
		generated := map[string]struct {
			access  string
			path    string
			methods string
		}{}
		for _, ep := range payload.Endpoints {
			if ep.Generated {
				generated[ep.ID] = struct {
					access  string
					path    string
					methods string
				}{access: ep.Access, path: ep.Path, methods: strings.Join(ep.Methods, ",")}
			}
		}
		want := map[string]struct {
			access  string
			path    string
			methods string
		}{
			"tasks.ListTasks":  {access: "auth", path: "/tasks/tasks", methods: "GET"},
			"tasks.GetTask":    {access: "auth", path: "/tasks/tasks/:id", methods: "GET"},
			"tasks.CreateTask": {access: "auth", path: "/tasks/tasks", methods: "POST"},
			"tasks.UpdateTask": {access: "auth", path: "/tasks/tasks/:id", methods: "PATCH"},
		}
		if len(generated) != len(want) {
			t.Fatalf("generated endpoints = %+v, want %+v", generated, want)
		}
		for id, wantEndpoint := range want {
			got, ok := generated[id]
			if !ok || got != wantEndpoint {
				t.Fatalf("generated[%s] = %+v, want %+v (all %+v)", id, got, wantEndpoint, generated)
			}
		}
		if _, ok := generated["tasks.DeleteTask"]; ok {
			t.Fatalf("disabled delete endpoint appeared: %+v", generated)
		}
	})
}
