package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oldAgentLaunchdPlist is the job `scenery deploy setup` installed before the
// agent gained start failure containment: the same template without
// --supervised.
func oldAgentLaunchdPlist(exe string, paths Paths) string {
	return strings.Replace(AgentLaunchdPlist(exe, paths, StartOptions{RouterHTTP: true, RouterAddr: "127.0.0.1:9440"}), "\t\t<string>--supervised</string>\n", "", 1)
}

// An upgraded Scenery restarting a job installed by an earlier one must bring
// the job up to the current invocation, keeping everything the job names;
// otherwise the reloaded agent still runs without failure containment.
func TestReconcileAgentLaunchdUpgradesAnOldJob(t *testing.T) {
	dir := t.TempDir()
	recorder := &launchctlRecorder{}
	withLaunchdHooks(t, dir, recorder)
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	plistPath := filepath.Join(dir, AgentLaunchdLabel+".plist")
	old := oldAgentLaunchdPlist("/opt/scenery/bin/scenery", paths)
	if strings.Contains(old, "--supervised") {
		t.Fatal("the old job fixture still names --supervised")
	}
	if err := os.WriteFile(plistPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if job, ok := InstalledSupervisorJob(paths.SocketPath); !ok || job.FailureContainment {
		t.Fatalf("old job = %+v, %v; want a supervisor without containment", job, ok)
	}

	updated, err := ReconcileAgentLaunchd()
	if err != nil || !updated {
		t.Fatalf("reconcile = %v, %v", updated, err)
	}
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	exe, got, opts, err := parseAgentLaunchdPlist(data)
	if err != nil || exe != "/opt/scenery/bin/scenery" || got.SocketPath != paths.SocketPath || got.LogPath != paths.LogPath || opts.RouterAddr != "127.0.0.1:9440" || !opts.RouterHTTP || !opts.Supervised {
		t.Fatalf("reconciled job = %q %+v %+v, %v", exe, got, opts, err)
	}
	if job, ok := InstalledSupervisorJob(paths.SocketPath); !ok || !job.FailureContainment || job.Path != plistPath {
		t.Fatalf("reconciled job = %+v, %v", job, ok)
	}
	if updated, err := ReconcileAgentLaunchd(); err != nil || updated {
		t.Fatalf("second reconcile = %v, %v; a current job is left as it is", updated, err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("reconcile called launchctl: %v", recorder.commands())
	}
}

// A job Scenery does not render is never rewritten.
func TestReconcileAgentLaunchdLeavesAForeignJobUntouched(t *testing.T) {
	dir := t.TempDir()
	withLaunchdHooks(t, dir, &launchctlRecorder{})
	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	plistPath := filepath.Join(dir, AgentLaunchdLabel+".plist")
	foreign := strings.Replace(oldAgentLaunchdPlist("/opt/scenery/bin/scenery", paths), "<string>--router-http</string>", "<string>--verbose</string>", 1)
	if err := os.WriteFile(plistPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAgentLaunchd(); err == nil || !strings.Contains(err.Error(), "not one Scenery renders") || !strings.Contains(err.Error(), `"--verbose"`) {
		t.Fatalf("reconcile of a foreign job = %v", err)
	}
	if data, _ := os.ReadFile(plistPath); string(data) != foreign {
		t.Fatal("a job Scenery cannot read was rewritten")
	}
	if job, ok := InstalledSupervisorJob(paths.SocketPath); !ok || job.FailureContainment {
		t.Fatalf("foreign job = %+v, %v; it supervises the socket without known containment", job, ok)
	}
}

func TestReconcileAgentSystemdUpgradesAnOldUnit(t *testing.T) {
	prevDir, prevRun := systemdUnitDirFunc, systemctlRunFunc
	t.Cleanup(func() { systemdUnitDirFunc, systemctlRunFunc = prevDir, prevRun })
	dir := t.TempDir()
	systemdUnitDirFunc = func() string { return dir }
	var calls []string
	systemctlRunFunc = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	paths := PathsForHome("/srv/scenery home/.scenery")
	current := AgentSystemdUnit("/usr/local/bin/scenery", paths, StartOptions{Trust: true, RouterAddr: "0.0.0.0:9440"})
	old := strings.Replace(current, " --supervised", "", 1)
	if old == current {
		t.Fatal("the current unit does not name --supervised")
	}
	if err := os.WriteFile(AgentSystemdUnitPath(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := ReconcileAgentSystemd()
	if err != nil || !updated {
		t.Fatalf("reconcile = %v, %v", updated, err)
	}
	if data, _ := os.ReadFile(AgentSystemdUnitPath()); string(data) != current {
		t.Fatalf("reconciled unit:\n%s\nwant:\n%s", data, current)
	}
	if strings.Join(calls, ";") != "daemon-reload" {
		t.Fatalf("systemctl calls = %v", calls)
	}
	if job, ok := InstalledSupervisorJob(paths.SocketPath); !ok || !job.FailureContainment || job.Label != AgentSystemdUnitName {
		t.Fatalf("reconciled job = %+v, %v", job, ok)
	}
	if updated, err := ReconcileAgentSystemd(); err != nil || updated || len(calls) != 1 {
		t.Fatalf("second reconcile = %v, %v, calls %v", updated, err, calls)
	}
}
