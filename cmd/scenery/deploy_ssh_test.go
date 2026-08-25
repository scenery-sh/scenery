package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploySSHRunsCheckAndCommandsInOrder(t *testing.T) {
	t.Parallel()

	root := deploySSHTestApp(t)
	var recorder deploySSHTestRecorder
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", Check: recorder.check, RunCommand: recorder.run}

	var stdout bytes.Buffer
	if err := runDeploySSH(&stdout, "some-id", []string{"--app-root", root}, tools); err != nil {
		t.Fatalf("runDeploySSH: %v\n%s", err, stdout.String())
	}
	log := recorder.log()
	for _, want := range []string{
		"local scenery check",
		"SSH preflight",
		"remote scenery down",
		"$HOME/.scenery/run/agent.sock",
		"rsync",
		root,
		"remote scenery up",
		"--delete",
		"--filter=:- .gitignore",
		"--exclude=.git/",
		"--exclude=.scenery/",
		"--exclude=.env",
		"--exclude=node_modules/",
		"--exclude=go.work",
		"--exclude=go.work.sum",
		"some-id:.scenery/apps/basicapp/",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("command log missing %q:\n%s", want, log)
		}
	}
	if order := recorder.order(); order != "local scenery check\nSSH preflight\nremote scenery down\nrsync\nremote scenery up" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if !strings.Contains(stdout.String(), "remote ready") {
		t.Fatalf("stdout did not stream remote output:\n%s", stdout.String())
	}
}

func TestDeploySSHRejectsBeforeCommands(t *testing.T) {
	t.Parallel()

	var recorder deploySSHTestRecorder
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", RunCommand: recorder.run}
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)

	err := runDeploySSH(&bytes.Buffer{}, "other-id", []string{"--app-root", root}, tools)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unlisted target error = %v", err)
	}
	if log := recorder.log(); log != "" {
		t.Fatalf("unlisted target ran commands:\n%s", log)
	}

	writeTestAppFile(t, root, testAppFilename, "not valid scenery source")
	err = runDeploySSH(&bytes.Buffer{}, "some-id", []string{"--app-root", root}, tools)
	if err == nil || !strings.Contains(err.Error(), "local scenery check") {
		t.Fatalf("invalid app error = %v", err)
	}
	if log := recorder.log(); log != "" {
		t.Fatalf("failed local check ran commands:\n%s", log)
	}
}

func TestDeploySSHStopsAfterChildFailureAndPreservesExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failStep  string
		wantSteps []string
	}{
		{name: "preflight", failStep: "SSH preflight", wantSteps: []string{"SSH preflight"}},
		{name: "down", failStep: "remote scenery down", wantSteps: []string{"SSH preflight", "remote scenery down"}},
		{name: "rsync", failStep: "rsync", wantSteps: []string{"SSH preflight", "remote scenery down", "rsync"}},
		{name: "up", failStep: "remote scenery up", wantSteps: []string{"SSH preflight", "remote scenery down", "rsync", "remote scenery up"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "app with spaces")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			var steps []string
			tools := deploySSHTools{
				SSH:   "/fake/ssh",
				Rsync: "/fake/rsync",
				RunCommand: func(name string, cmd *exec.Cmd) error {
					steps = append(steps, name)
					if cmd.Dir != root {
						t.Fatalf("%s command dir = %q, want %q", name, cmd.Dir, root)
					}
					if name == tt.failStep {
						return deploySSHTestExitError(7)
					}
					return nil
				},
			}
			err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", false, tools)
			var exitErr deploySSHTestExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || cliExitCode(err) != 7 {
				t.Fatalf("error = %v, want child exit 7", err)
			}
			if got, want := strings.Join(steps, "\n"), strings.Join(tt.wantSteps, "\n"); got != want {
				t.Fatalf("command steps = %q, want %q", got, want)
			}
		})
	}
}

func TestDeploySSHRunnerStreamsFailureOutputAndPreservesExitCodeInProcess(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "app with spaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tools := deploySSHTools{
		SSH:   "/fake/ssh",
		Rsync: "/unused/rsync",
		RunCommand: func(name string, cmd *exec.Cmd) error {
			if name != "SSH preflight" {
				t.Fatalf("unexpected command %q", name)
			}
			if _, err := fmt.Fprintln(cmd.Stdout, "preflight output"); err != nil {
				return err
			}
			return deploySSHTestExitError(7)
		},
	}
	var stdout bytes.Buffer
	err := runDeploySSHCommands(&stdout, root, "basicapp", "some-id", "production", false, tools)
	var exitErr deploySSHTestExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || cliExitCode(err) != 7 {
		t.Fatalf("error = %v, want child process exit 7", err)
	}
	if got := stdout.String(); got != "preflight output\n" {
		t.Fatalf("child stdout = %q, want streamed preflight output", got)
	}
}

type deploySSHTestExitError int

func (e deploySSHTestExitError) Error() string { return fmt.Sprintf("exit status %d", e) }
func (e deploySSHTestExitError) ExitCode() int { return int(e) }

func TestDeploySSHRunsRemotePublishAfterUp(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var recorder deploySSHTestRecorder
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", RunCommand: recorder.run}
	if err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", true, tools); err != nil {
		t.Fatalf("runDeploySSHCommands: %v", err)
	}
	log := recorder.log()
	if order := recorder.order(); order != "SSH preflight\nremote scenery down\nrsync\nremote scenery up\nremote scenery deploy publish" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if !strings.Contains(log, `scenery deploy publish --env "production" --app-root "$HOME/.scenery/apps/basicapp" -o json`) {
		t.Fatalf("publish command missing app root:\n%s", log)
	}
}

func deploySSHTestApp(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app with spaces")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)
	return root
}

type deploySSHRecordedCommand struct {
	Name string
	Dir  string
	Args []string
}

type deploySSHTestRecorder struct {
	Commands []deploySSHRecordedCommand
}

func (r *deploySSHTestRecorder) check(_ context.Context, _ io.Writer, args []string) error {
	r.Commands = append(r.Commands, deploySSHRecordedCommand{
		Name: "local scenery check",
		Args: append([]string(nil), args...),
	})
	return nil
}

func (r *deploySSHTestRecorder) run(name string, cmd *exec.Cmd) error {
	r.Commands = append(r.Commands, deploySSHRecordedCommand{
		Name: name,
		Dir:  cmd.Dir,
		Args: append([]string(nil), cmd.Args...),
	})
	if name == "remote scenery up" {
		_, err := fmt.Fprintln(cmd.Stdout, "remote ready")
		return err
	}
	return nil
}

func (r *deploySSHTestRecorder) log() string {
	var log strings.Builder
	for _, command := range r.Commands {
		fmt.Fprintf(&log, "%s\ndir:%s\nargs:%s\n", command.Name, command.Dir, strings.Join(command.Args, " "))
	}
	return log.String()
}

func (r *deploySSHTestRecorder) order() string {
	order := make([]string, 0, len(r.Commands))
	for _, command := range r.Commands {
		order = append(order, command.Name)
	}
	return strings.Join(order, "\n")
}
