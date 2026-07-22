package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestDeploySSHRunsCheckAndCommandsInOrder(t *testing.T) {
	root := copyDeploySSHTestApp(t)
	logPath := installDeploySSHTestCommands(t)

	var stdout bytes.Buffer
	if err := runDeployCommand(&stdout, []string{"some-id", "--app-root", root}); err != nil {
		t.Fatalf("runDeployCommand: %v\n%s", err, stdout.String())
	}
	log := readDeploySSHTestLog(t, logPath)
	for _, want := range []string{
		"ssh:preflight",
		"ssh:down",
		"$HOME/.scenery/run/agent.sock",
		"rsync:" + root,
		"ssh:up",
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
	if order := commandOrder(log); order != "ssh:preflight\nssh:down\nrsync\nssh:up" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if !strings.Contains(stdout.String(), "remote ready") {
		t.Fatalf("stdout did not stream remote output:\n%s", stdout.String())
	}
}

func TestDeploySSHRejectsBeforeCommands(t *testing.T) {
	logPath := installDeploySSHTestCommands(t)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)

	err := runDeploySSH(&bytes.Buffer{}, "other-id", []string{"--app-root", root})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unlisted target error = %v", err)
	}
	if log := readDeploySSHTestLog(t, logPath); log != "" {
		t.Fatalf("unlisted target ran commands:\n%s", log)
	}

	writeTestAppFile(t, root, testAppFilename, "not valid scenery source")
	err = runDeploySSH(&bytes.Buffer{}, "some-id", []string{"--app-root", root})
	if err == nil || !strings.Contains(err.Error(), "local scenery check") {
		t.Fatalf("invalid app error = %v", err)
	}
	if log := readDeploySSHTestLog(t, logPath); log != "" {
		t.Fatalf("failed local check ran commands:\n%s", log)
	}
}

func TestDeploySSHStopsAfterChildFailureAndPreservesExitCode(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		wantOrder string
	}{
		{name: "preflight", env: "DEPLOY_PREFLIGHT_EXIT", wantOrder: "ssh:preflight"},
		{name: "down", env: "DEPLOY_DOWN_EXIT", wantOrder: "ssh:preflight\nssh:down"},
		{name: "rsync", env: "DEPLOY_RSYNC_EXIT", wantOrder: "ssh:preflight\nssh:down\nrsync"},
		{name: "up", env: "DEPLOY_UP_EXIT", wantOrder: "ssh:preflight\nssh:down\nrsync\nssh:up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "app with spaces")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			logPath := installDeploySSHTestCommands(t)
			t.Setenv(tt.env, "7")
			err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", false)
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || cliExitCode(err) != 7 {
				t.Fatalf("error = %v, want child exit 7", err)
			}
			log := readDeploySSHTestLog(t, logPath)
			if order := commandOrder(log); order != tt.wantOrder {
				t.Fatalf("command order = %q, want %q\n%s", order, tt.wantOrder, log)
			}
			if strings.Contains(tt.wantOrder, "rsync") && !strings.Contains(log, "rsync:"+root) {
				t.Fatalf("rsync cwd did not preserve spaced path:\n%s", log)
			}
		})
	}
}

func TestDeploySSHRunsRemotePublishAfterUp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := installDeploySSHTestCommands(t)
	if err := runDeploySSHCommands(&bytes.Buffer{}, root, "basicapp", "some-id", "production", true); err != nil {
		t.Fatalf("runDeploySSHCommands: %v", err)
	}
	log := readDeploySSHTestLog(t, logPath)
	if order := commandOrder(log); order != "ssh:preflight\nssh:down\nrsync\nssh:up\nssh:publish" {
		t.Fatalf("command order = %q\n%s", order, log)
	}
	if !strings.Contains(log, `scenery deploy publish --env "production" --app-root "$HOME/.scenery/apps/basicapp" -o json`) {
		t.Fatalf("publish command missing app root:\n%s", log)
	}
}

func copyDeploySSHTestApp(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app with spaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(appcfg.RepoRoot(), "testdata", "apps", "basic")
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, "go.mod", strings.ReplaceAll(string(goMod), "../../..", appcfg.RepoRoot()))
	writeTestAppFile(t, root, ".scenery.json", `{"name":"basicapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["some-id"]}}}}`)
	return root
}

func installDeploySSHTestCommands(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "commands.log")
	t.Setenv("DEPLOY_COMMAND_LOG", logPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeTestAppFile(t, bin, "ssh", `#!/bin/sh
command="$*"
case "$command" in
  *"command -v scenery"*) kind=preflight; code=${DEPLOY_PREFLIGHT_EXIT:-0} ;;
  *"scenery down"*) kind=down; code=${DEPLOY_DOWN_EXIT:-0} ;;
  *"scenery up"*) kind=up; code=${DEPLOY_UP_EXIT:-0} ;;
  *"scenery deploy publish"*) kind=publish; code=${DEPLOY_PUBLISH_EXIT:-0} ;;
  *) kind=unknown; code=99 ;;
esac
printf 'ssh:%s\nssh-args:%s\n' "$kind" "$command" >> "$DEPLOY_COMMAND_LOG"
if [ "$kind" = up ]; then printf 'remote ready\n'; fi
exit "$code"
`)
	writeTestAppFile(t, bin, "rsync", `#!/bin/sh
printf 'rsync\nrsync:%s\nrsync-args:%s\n' "$PWD" "$*" >> "$DEPLOY_COMMAND_LOG"
exit "${DEPLOY_RSYNC_EXIT:-0}"
`)
	for _, name := range []string{"ssh", "rsync"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return logPath
}

func readDeploySSHTestLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func commandOrder(log string) string {
	var order []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "ssh:") || line == "rsync" {
			order = append(order, line)
		}
	}
	return strings.Join(order, "\n")
}
