package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/doctor"
)

func TestDoctorTextReportsPreflightRatherThanRuntimeReadiness(t *testing.T) {
	t.Parallel()
	resp := doctorResponse{OK: true, Summary: doctor.Summary{OK: 2, Warnings: 1, Skipped: 1}}
	var out bytes.Buffer
	if err := writeDoctorText(&out, resp); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"preflight summary", "2 ok, 1 warnings, 0 errors, 1 skipped", "not application runtime readiness", "remain unverified"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor summary missing %q: %s", want, out.String())
		}
	}
}

func TestDoctorPreflightFailureKeepsExitClassification(t *testing.T) {
	root := t.TempDir()
	deps := doctor.ProbeDeps{
		LookPath:      func(string) (string, error) { return "", errors.New("missing tool") },
		RunCommand:    func(context.Context, string, ...string) ([]byte, error) { return nil, nil },
		ResourceProbe: fakeDoctorResourceProbe{},
		Getwd:         func() (string, error) { return root, nil },
		CacheRoot:     func() (string, error) { return filepath.Join(root, "cache"), nil },
		AgentHome:     func() (string, error) { return root, nil },
		DiscoverApp: func(string) (doctor.AppInfo, appcfg.Config, bool, error) {
			return doctor.AppInfo{}, appcfg.Config{}, false, nil
		},
	}
	for _, args := range [][]string{nil, {"-o", "json"}} {
		var out bytes.Buffer
		err := runSceneryDoctorWithDeps(t.Context(), &out, args, deps)
		if cliExitCode(err) != 3 {
			t.Fatalf("doctor failure exit = %d; %v", cliExitCode(err), err)
		}
		if strings.Contains(out.String(), "SCN9000") {
			t.Fatalf("known preflight failure became internal: %s", out.String())
		}
	}
}
