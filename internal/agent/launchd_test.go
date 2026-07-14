package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type launchctlRecorder struct {
	calls   [][]string
	results map[string]launchctlResult
}

type launchctlResult struct {
	out []byte
	err error
}

func (r *launchctlRecorder) run(args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	if result, ok := r.results[args[0]]; ok {
		return result.out, result.err
	}
	return nil, nil
}

func (r *launchctlRecorder) commands() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, strings.Join(call, " "))
	}
	return out
}

func withLaunchdHooks(t *testing.T, dir string, recorder *launchctlRecorder) {
	t.Helper()
	oldRun := launchctlRunFunc
	oldDir := launchAgentsDirFunc
	oldSleep := launchdSleepFunc
	oldUID := launchdUserIDFunc
	oldSupported := launchdSupportedFunc
	t.Cleanup(func() {
		launchctlRunFunc = oldRun
		launchAgentsDirFunc = oldDir
		launchdSleepFunc = oldSleep
		launchdUserIDFunc = oldUID
		launchdSupportedFunc = oldSupported
	})
	launchctlRunFunc = recorder.run
	launchAgentsDirFunc = func() (string, error) { return dir, nil }
	launchdSleepFunc = func(time.Duration) {}
	launchdUserIDFunc = func() int { return 501 }
	launchdSupportedFunc = func() bool { return true }
}

func TestAgentLaunchdPlistPinsSupervisedInvocation(t *testing.T) {
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	plist := AgentLaunchdPlist("/usr/local/bin/scenery", paths, StartOptions{RouterHTTP: true, RouterAddr: "127.0.0.1:9440"})
	for _, want := range []string{
		"<string>dev.scenery.agent</string>",
		"<string>/usr/local/bin/scenery</string>",
		"<string>system</string>",
		"<string>agent</string>",
		"<string>--socket</string>",
		"<string>" + paths.SocketPath + "</string>",
		"<string>--router-listen</string>",
		"<string>127.0.0.1:9440</string>",
		"<string>--router-http</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		// Interactive + a short throttle keep launchd respawning a killed
		// agent promptly instead of pending the spawn as "inefficient".
		"<key>ProcessType</key>",
		"<string>Interactive</string>",
		"<key>ThrottleInterval</key>",
		"<integer>2</integer>",
		"<string>" + paths.LogPath + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	// KeepAlive must be true: it is the continuous supervision contract.
	keepAliveAt := strings.Index(plist, "<key>KeepAlive</key>")
	if keepAliveAt < 0 || !strings.Contains(plist[keepAliveAt:keepAliveAt+60], "<true/>") {
		t.Fatalf("plist KeepAlive is not <true/>:\n%s", plist)
	}
}

func TestInstallAgentLaunchdWritesPlistAndBootstraps(t *testing.T) {
	dir := t.TempDir()
	recorder := &launchctlRecorder{}
	withLaunchdHooks(t, dir, recorder)
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))

	plistPath, err := InstallAgentLaunchd("/usr/local/bin/scenery", paths, StartOptions{RouterHTTP: true})
	if err != nil {
		t.Fatalf("InstallAgentLaunchd: %v", err)
	}
	if plistPath != filepath.Join(dir, "dev.scenery.agent.plist") {
		t.Fatalf("plist path = %s", plistPath)
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	commands := recorder.commands()
	// launchd can pend a RunAtLoad spawn, so install must kickstart after
	// bootstrapping instead of trusting RunAtLoad.
	if len(commands) != 3 || commands[0] != "bootout gui/501/dev.scenery.agent" || commands[1] != "bootstrap gui/501 "+plistPath || commands[2] != "kickstart gui/501/dev.scenery.agent" {
		t.Fatalf("launchctl calls = %v", commands)
	}
}

func TestInstallAgentLaunchdRetriesTransientBootstrapFailure(t *testing.T) {
	dir := t.TempDir()
	attempts := 0
	recorder := &launchctlRecorder{}
	withLaunchdHooks(t, dir, recorder)
	launchctlRunFunc = func(args ...string) ([]byte, error) {
		recorder.calls = append(recorder.calls, args)
		if args[0] == "bootstrap" {
			attempts++
			if attempts < 3 {
				return []byte("Bootstrap failed: 5: Input/output error"), fmt.Errorf("exit status 5")
			}
		}
		return nil, nil
	}
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	if _, err := InstallAgentLaunchd("/usr/local/bin/scenery", paths, StartOptions{}); err != nil {
		t.Fatalf("InstallAgentLaunchd: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("bootstrap attempts = %d, want 3", attempts)
	}
}

func TestRemoveAgentLaunchdBootsOutBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	recorder := &launchctlRecorder{}
	withLaunchdHooks(t, dir, recorder)
	plistPath := filepath.Join(dir, "dev.scenery.agent.plist")
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveAgentLaunchd()
	if err != nil || !removed {
		t.Fatalf("RemoveAgentLaunchd = %v, %v", removed, err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist still present: %v", err)
	}
	commands := recorder.commands()
	if len(commands) != 1 || commands[0] != "bootout gui/501/dev.scenery.agent" {
		t.Fatalf("launchctl calls = %v", commands)
	}

	removed, err = RemoveAgentLaunchd()
	if err != nil || removed {
		t.Fatalf("second RemoveAgentLaunchd = %v, %v", removed, err)
	}
}

func TestAgentLaunchdStatusDistinguishesPlistPresenceFromLoadedJob(t *testing.T) {
	dir := t.TempDir()
	recorder := &launchctlRecorder{results: map[string]launchctlResult{
		"print": {out: []byte("Could not find service"), err: fmt.Errorf("exit status 113")},
	}}
	withLaunchdHooks(t, dir, recorder)
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))

	status := AgentLaunchdStatusForSocket(paths.SocketPath)
	if status.PlistPresent || status.Loaded || status.Running {
		t.Fatalf("missing plist status = %+v", status)
	}

	if err := os.WriteFile(filepath.Join(dir, "dev.scenery.agent.plist"), []byte(AgentLaunchdPlist("/usr/local/bin/scenery", paths, StartOptions{})), 0o644); err != nil {
		t.Fatal(err)
	}
	status = AgentLaunchdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.SupervisesSocket || status.Loaded || status.Running {
		t.Fatalf("unloaded plist status = %+v", status)
	}

	recorder.results["print"] = launchctlResult{out: []byte("service = {\n\tstate = running\n\tpid = 4242\n}\n")}
	status = AgentLaunchdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.Loaded || !status.Running || status.PID != 4242 {
		t.Fatalf("loaded status = %+v", status)
	}

	// A plist that manages a different agent home never counts as this
	// socket's supervisor.
	other := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	status = AgentLaunchdStatusForSocket(other.SocketPath)
	if status.SupervisesSocket {
		t.Fatalf("foreign socket claimed supervised: %+v", status)
	}
}

func TestStartSupervisedAgentProcessCooperatesWithLaunchd(t *testing.T) {
	dir := t.TempDir()
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	plist := AgentLaunchdPlist("/usr/local/bin/scenery", paths, StartOptions{})
	if err := os.WriteFile(filepath.Join(dir, "dev.scenery.agent.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}

	// Loaded job: start routes through kickstart, never a raw spawn.
	recorder := &launchctlRecorder{results: map[string]launchctlResult{
		"print": {out: []byte("state = running\npid = 77\n")},
	}}
	withLaunchdHooks(t, dir, recorder)
	if !startSupervisedAgentProcess(paths) {
		t.Fatal("expected supervised start for loaded job")
	}
	if got := recorder.commands()[len(recorder.commands())-1]; got != "kickstart gui/501/dev.scenery.agent" {
		t.Fatalf("last launchctl call = %s", got)
	}

	// Unloaded job with plist present: start repairs supervision by
	// bootstrapping instead of spawning an unsupervised agent.
	recorder = &launchctlRecorder{results: map[string]launchctlResult{
		"print": {out: []byte("Could not find service"), err: fmt.Errorf("exit status 113")},
	}}
	withLaunchdHooks(t, dir, recorder)
	if !startSupervisedAgentProcess(paths) {
		t.Fatal("expected supervised start via bootstrap")
	}
	found := false
	for _, command := range recorder.commands() {
		if strings.HasPrefix(command, "bootstrap gui/501 ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bootstrap not called: %v", recorder.commands())
	}

	// A KeepAlive respawn can beat kickstart; a running job is success.
	recorder = &launchctlRecorder{results: map[string]launchctlResult{
		"print":     {out: []byte("state = running\npid = 88\n")},
		"kickstart": {out: []byte("Already running"), err: fmt.Errorf("exit status 37")},
	}}
	withLaunchdHooks(t, dir, recorder)
	if !startSupervisedAgentProcess(paths) {
		t.Fatal("expected running respawn to count as success")
	}

	// No supervising plist for this socket: unsupervised path.
	other := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	recorder = &launchctlRecorder{}
	withLaunchdHooks(t, dir, recorder)
	if startSupervisedAgentProcess(other) {
		t.Fatal("foreign socket must not use the supervisor")
	}
	launchdSupportedFunc = func() bool { return false }
	if startSupervisedAgentProcess(paths) {
		t.Fatal("unsupported platform must not use the supervisor")
	}
}
