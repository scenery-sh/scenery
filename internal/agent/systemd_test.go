package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSystemdUnitPinsSupervisedInvocation(t *testing.T) {
	paths := PathsForHome("/root/.scenery")
	unit := AgentSystemdUnit("/usr/local/bin/scenery", paths, StartOptions{RouterHTTP: true, RouterAddr: "127.0.0.1:9440"})
	for _, want := range []string{
		"ExecStart=/usr/local/bin/scenery system agent --socket /root/.scenery/run/agent.sock --router-listen 127.0.0.1:9440 --router-http",
		"Restart=always",
		"Environment=HOME=/root",
		"After=network-online.target",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("agent unit missing %q:\n%s", want, unit)
		}
	}
}

func TestDeployResumeSystemdUnitOrdersAfterAgentAndEdge(t *testing.T) {
	paths := PathsForHome("/root/.scenery")
	unit := DeployResumeSystemdUnit("/usr/local/bin/scenery", paths)
	for _, want := range []string{
		"Type=oneshot",
		"After=network-online.target scenery-agent.service scenery-edge.service",
		"ExecStart=/usr/local/bin/scenery deploy resume -o json",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("resume unit missing %q:\n%s", want, unit)
		}
	}
}

func TestAgentSystemdStatusForSocketMatchesSupervisedSocket(t *testing.T) {
	prevDir := systemdUnitDirFunc
	prevRun := systemctlRunFunc
	prevSupported := systemdSupportedFunc
	t.Cleanup(func() {
		systemdUnitDirFunc = prevDir
		systemctlRunFunc = prevRun
		systemdSupportedFunc = prevSupported
	})
	dir := t.TempDir()
	systemdUnitDirFunc = func() string { return dir }
	systemdSupportedFunc = func() bool { return true }
	systemctlRunFunc = func(args ...string) ([]byte, error) {
		return []byte("LoadState=loaded\nActiveState=active\nMainPID=77\n"), nil
	}
	paths := PathsForHome("/root/.scenery")
	unit := AgentSystemdUnit("/usr/local/bin/scenery", paths, StartOptions{RouterHTTP: true})
	if err := os.WriteFile(filepath.Join(dir, AgentSystemdUnitName), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	status := AgentSystemdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.SupervisesSocket || !status.Loaded || !status.Running || status.PID != 77 {
		t.Fatalf("status = %+v", status)
	}
	foreign := AgentSystemdStatusForSocket("/somewhere/else.sock")
	if foreign.SupervisesSocket {
		t.Fatal("foreign socket must not be treated as supervised")
	}
}
