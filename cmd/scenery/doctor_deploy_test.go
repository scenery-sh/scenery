package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
)

type fakeDoctorResourceProbe struct{}

func (fakeDoctorResourceProbe) Runtime() doctorRuntimeInfo {
	return doctorRuntimeInfo{GOOS: "darwin", GOARCH: "arm64", NumCPU: 8}
}

func (fakeDoctorResourceProbe) Memory(ctx context.Context) (doctorMemoryInfo, error) {
	return doctorMemoryInfo{TotalBytes: 8 * 1024 * 1024 * 1024}, nil
}

func (fakeDoctorResourceProbe) Disk(ctx context.Context, path string) (doctorDiskInfo, error) {
	return doctorDiskInfo{Path: path, FreeBytes: 20 * 1024 * 1024 * 1024, TotalBytes: 40 * 1024 * 1024 * 1024}, nil
}

func TestDoctorIncludesDeployDiagnosticsSection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	stubDeployDiagnostics(t, nil)
	paths := localagent.PathsForHome(home)
	registry := localagent.EmptyDeployRegistry()
	registry.Targets = []localagent.DeployTarget{{
		Domain:  "onlv.dev",
		AppRoot: t.TempDir(),
		Enabled: true,
	}}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}
	tmp := t.TempDir()
	resp := buildDoctorResponse(context.Background(), doctorOptions{}, doctorProbeDeps{
		LookPath: func(file string) (string, error) {
			if file == "go" {
				return "/usr/local/go/bin/go", nil
			}
			return "", errors.New("not found")
		},
		RunCommand: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("go version go1.26.3 darwin/arm64"), nil
		},
		ResourceProbe: fakeDoctorResourceProbe{},
		Getwd:         func() (string, error) { return tmp, nil },
		CacheRoot:     func() (string, error) { return filepath.Join(tmp, "cache"), nil },
		AgentHome:     func() (string, error) { return home, nil },
		DiscoverApp: func(start string) (doctorAppInfo, appcfg.Config, bool, error) {
			return doctorAppInfo{}, appcfg.Config{}, false, nil
		},
	})
	if resp.Deploy == nil {
		t.Fatal("doctor deploy section is nil")
	}
	if resp.Deploy.Kind != "scenery.doctor.deploy" || resp.Deploy.SchemaRevision != newCLIPayloadIdentity("scenery.doctor.deploy").SchemaRevision || resp.Deploy.RegistryPath != paths.DeployPath || len(resp.Deploy.Targets) != 1 {
		t.Fatalf("doctor deploy section = %+v", resp.Deploy)
	}
	if resp.Deploy.Diagnostics.LANIP != "192.168.1.20" || resp.Deploy.Diagnostics.PublicIP != "203.0.113.10" {
		t.Fatalf("doctor deploy diagnostics = %+v", resp.Deploy.Diagnostics)
	}
	if !doctorHasCheck(resp.Checks, "deploy.dns.onlv.dev") {
		t.Fatalf("doctor checks missing deploy DNS check: %+v", resp.Checks)
	}
}

func doctorHasCheck(checks []doctorCheck, id string) bool {
	for _, check := range checks {
		if check.ID == id {
			return true
		}
	}
	return false
}
