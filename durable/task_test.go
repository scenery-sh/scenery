package durable

import (
	"context"
	"testing"
)

func TestNewTaskTrimsNameAndService(t *testing.T) {
	task := NewTask(" maps.echo.v1 ", TaskConfig{Service: " maps "}, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	if task.Name() != "maps.echo.v1" {
		t.Fatalf("Name = %q, want maps.echo.v1", task.Name())
	}
	if task.Config().Service != "maps" {
		t.Fatalf("Service = %q, want maps", task.Config().Service)
	}
}

func TestNewTaskPanicsForMissingRequiredFields(t *testing.T) {
	tests := map[string]func(){
		"name": func() {
			NewTask("", TaskConfig{Service: "maps"}, func(context.Context, string) (string, error) { return "", nil })
		},
		"service": func() {
			NewTask("maps.echo.v1", TaskConfig{}, func(context.Context, string) (string, error) { return "", nil })
		},
		"handler": func() {
			NewTask[string, string]("maps.echo.v1", TaskConfig{Service: "maps"}, nil)
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			run()
		})
	}
}

func TestStartRejectsNilTask(t *testing.T) {
	if _, err := Start[string, string](context.Background(), nil, "input"); err == nil {
		t.Fatal("expected nil task error")
	}
}
