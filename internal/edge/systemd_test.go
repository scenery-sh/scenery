package edge

import (
	"os"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestSystemdEdgeUnitRunsManagedCaddyWithManagedConfig(t *testing.T) {
	t.Parallel()
	paths := localagent.PathsForHome("/root/.scenery")
	unit := SystemdEdgeUnit("/root/.scenery/toolchain/artifacts/caddy/2.11.4/linux-amd64/bin/caddy", paths, "/root/.scenery/run/caddy-admin.sock")
	for _, want := range []string{
		"Description=Scenery managed public edge (Caddy)",
		"After=network-online.target",
		"ExecStart=/root/.scenery/toolchain/artifacts/caddy/2.11.4/linux-amd64/bin/caddy run --config /root/.scenery/agent/edge/Caddyfile --adapter caddyfile",
		"ExecReload=/root/.scenery/toolchain/artifacts/caddy/2.11.4/linux-amd64/bin/caddy reload --config /root/.scenery/agent/edge/Caddyfile --adapter caddyfile --address unix///root/.scenery/run/caddy-admin.sock",
		"Restart=always",
		"Environment=HOME=/root",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("edge unit missing %q:\n%s", want, unit)
		}
	}
}

func TestSystemdEdgeStatusParsesSystemctlShow(t *testing.T) {
	prevRun := systemctlEdgeRunFunc
	prevDir := systemdEdgeUnitDirVar
	t.Cleanup(func() { systemctlEdgeRunFunc = prevRun; systemdEdgeUnitDirVar = prevDir })
	dir := t.TempDir()
	systemdEdgeUnitDirVar = dir
	status := SystemdEdgeStatus()
	if status.Installed || status.Active {
		t.Fatalf("missing unit must report uninstalled: %+v", status)
	}
	if err := writeTestFile(dir+"/"+SystemdEdgeUnitName, "[Unit]\n"); err != nil {
		t.Fatal(err)
	}
	systemctlEdgeRunFunc = func(args ...string) ([]byte, error) {
		return []byte("LoadState=loaded\nActiveState=active\nMainPID=4321\n"), nil
	}
	status = SystemdEdgeStatus()
	if !status.Installed || !status.Loaded || !status.Active || status.PID != 4321 {
		t.Fatalf("status = %+v", status)
	}
}
