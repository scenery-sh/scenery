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

// deploySSHFakeTarget answers deployment requests like a healthy target and
// records every operation, optionally failing one.
type deploySSHFakeTarget struct {
	operations []string
	fail       string
	previous   string
}

func (f *deploySSHFakeTarget) exchange(_ string, request deployRemoteRequest) (deployRemoteResponse, error) {
	f.operations = append(f.operations, request.Operation)
	response := deployRemoteResponse{Kind: deployResponseKind, Protocol: deployProtocolVersion, OK: true, SourceRoot: "/home/deploy/.scenery/deployments/basicapp/production/source", ConfigRevision: "cfg-00000000000000000000000000000001", Previous: f.previous}
	if request.Operation == "begin" {
		response.StagingPath = ".scenery/deployments/basicapp/production/releases/" + request.DeploymentID + "/source/"
	}
	if request.Operation == f.fail {
		response.OK, response.Error, response.Problems = false, "rejected", []string{"designs.api_token: required input is not configured"}
	}
	return response, nil
}

func TestDeploySSHRunsCheckAndCommandsInOrder(t *testing.T) {
	t.Parallel()

	root := deploySSHTestApp(t)
	var recorder deploySSHTestRecorder
	target := &deploySSHFakeTarget{}
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", Check: recorder.check, RunCommand: recorder.run, Exchange: target.exchange}

	var stdout bytes.Buffer
	if err := runDeploySSH(&stdout, "some-id", []string{"--app-root", root}, tools); err != nil {
		t.Fatalf("runDeploySSH: %v\n%s", err, stdout.String())
	}
	log := recorder.log()
	for _, want := range []string{
		"local scenery check",
		"SSH preflight",
		"remote scenery down",
		"rsync",
		root,
		"remote scenery up",
		"--delete",
		"--filter=:- .gitignore",
		"--exclude=.git/",
		"--exclude=.scenery/",
		"--exclude=.env",
		"--exclude=node_modules/",
		"some-id:.scenery/deployments/basicapp/production/releases/",
		`--app-root "/home/deploy/.scenery/deployments/basicapp/production/source"`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("command log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, ".scenery/apps/") {
		t.Fatalf("deploy synchronized into the configuration store directory:\n%s", log)
	}
	if order := recorder.order(); order != "local scenery check\nSSH preflight\nrsync\nremote scenery down\nremote scenery up" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if got := strings.Join(target.operations, ","); got != "begin,validate,activate,commit" {
		t.Fatalf("target operations = %s", got)
	}
	if !strings.Contains(stdout.String(), "remote ready") || !strings.Contains(stdout.String(), "is active with configuration revision") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestDeploySSHRejectsBeforeCommands(t *testing.T) {
	t.Parallel()

	var recorder deploySSHTestRecorder
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", RunCommand: recorder.run, Exchange: (&deploySSHFakeTarget{}).exchange}
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","id":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)

	err := runDeploySSH(&bytes.Buffer{}, "other-id", []string{"--app-root", root}, tools)
	if err == nil || !strings.Contains(err.Error(), "not configured") || cliExitCode(err) != 2 {
		t.Fatalf("unlisted target error = %v (exit %d)", err, cliExitCode(err))
	}
	if targets := configuredDeployTargets([]string{"--app-root", root}); len(targets) != 1 || targets[0] != "some-id" {
		t.Fatalf("configured targets = %q", targets)
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

// A candidate the target rejects never stops the healthy runtime; a failed
// activation restores the previous release.
func TestDeploySSHKeepsOrRestoresTheHealthyRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failStep   string
		failTarget string
		previous   string
		wantSteps  string
		wantOps    string
		wantError  string
	}{
		{name: "preflight", failStep: "SSH preflight", wantSteps: "SSH preflight", wantOps: "", wantError: "exit status 7"},
		{name: "rsync", failStep: "rsync", wantSteps: "SSH preflight\nrsync", wantOps: "begin,abort", wantError: "exit status 7"},
		{name: "invalid candidate", failTarget: "validate", wantSteps: "SSH preflight\nrsync", wantOps: "begin,validate,abort", wantError: "designs.api_token"},
		{name: "up restores previous", failStep: "remote scenery up", previous: "prev", wantSteps: "SSH preflight\nrsync\nremote scenery down\nremote scenery up\nremote scenery down\nremote scenery up", wantOps: "begin,validate,activate,rollback", wantError: "restored previous release prev"},
		{name: "first release stops", failStep: "remote scenery up", wantSteps: "SSH preflight\nrsync\nremote scenery down\nremote scenery up\nremote scenery down", wantOps: "begin,validate,activate,rollback", wantError: "first release was not activated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "app with spaces")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			target := &deploySSHFakeTarget{fail: tt.failTarget, previous: tt.previous}
			var steps []string
			failed := false
			tools := deploySSHTools{
				SSH: "/fake/ssh", Rsync: "/fake/rsync", Exchange: target.exchange,
				RunCommand: func(name string, cmd *exec.Cmd) error {
					steps = append(steps, name)
					if cmd.Dir != root {
						t.Fatalf("%s command dir = %q, want %q", name, cmd.Dir, root)
					}
					if name == tt.failStep && !failed {
						failed = true
						return deploySSHTestExitError(7)
					}
					return nil
				},
			}
			err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", false, tools)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
			if got := strings.Join(steps, "\n"); got != tt.wantSteps {
				t.Fatalf("command steps = %q, want %q", got, tt.wantSteps)
			}
			if got := strings.Join(target.operations, ","); got != tt.wantOps {
				t.Fatalf("target operations = %q, want %q", got, tt.wantOps)
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
	target := &deploySSHFakeTarget{}
	tools := deploySSHTools{SSH: "/fake/ssh", Rsync: "/fake/rsync", RunCommand: recorder.run, Exchange: target.exchange}
	if err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", true, tools); err != nil {
		t.Fatalf("runDeploySSHCommands: %v", err)
	}
	log := recorder.log()
	if order := recorder.order(); order != "SSH preflight\nrsync\nremote scenery down\nremote scenery up\nremote scenery deploy publish" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if !strings.Contains(log, `scenery deploy publish --env "production" --app-root "/home/deploy/.scenery/deployments/basicapp/production/source" -o json`) {
		t.Fatalf("publish command missing app root:\n%s", log)
	}
	if got := strings.Join(target.operations, ","); got != "begin,validate,activate,commit" {
		t.Fatalf("target operations = %s", got)
	}
}

func deploySSHTestApp(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app with spaces")
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","id":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)
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
