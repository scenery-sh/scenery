package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/doctor"
)

func TestAppConfigurationDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"unknown field", `{"name":"demo","dev":{"services":{"private-password":{}}}}`, "remove dev.services"},
		{"syntax", `{"name":`, "decode .scenery.json"},
		{"type", `{"name":{}}`, "cannot unmarshal object"},
		{"missing name", `{}`, "non-empty name or id"},
		{"validation", `{"name":"demo","envs":{"local":{"default":false}}}`, "must be the default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, appcfg.PrimaryConfigFilename), []byte(tc.source), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := appcfg.DiscoverRoot(root)
			diagnostic := assertSafeRuntimeDiagnostic(t, fmt.Errorf("discover app: %w", err), 3, "SCN8003")
			if !strings.Contains(diagnostic.Message, tc.want) || diagnostic.ReportToken != "" {
				t.Fatalf("configuration diagnostic = %+v", diagnostic)
			}
		})
	}
}

func TestDoctorReportsImplicitAppDiscoveryFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, tc := range []struct {
		name     string
		explicit bool
		err      error
		want     bool
	}{
		{"implicit missing", false, fmt.Errorf("discover: %w", appcfg.ErrRootNotFound), false},
		{"explicit missing", true, appcfg.ErrRootNotFound, true},
		{"implicit invalid", false, errors.New("invalid .scenery.json: remove dev.services"), true},
		{"implicit unreadable", false, os.ErrPermission, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := doctorOptions{}
			if tc.explicit {
				opts.AppRoot = root
			}
			resp := buildDoctorResponse(t.Context(), opts, doctor.ProbeDeps{
				ResourceProbe: fakeDoctorResourceProbe{},
				Getwd:         func() (string, error) { return root, nil },
				CacheRoot:     func() (string, error) { return filepath.Join(root, "cache"), nil },
				AgentHome:     func() (string, error) { return filepath.Join(root, "agent"), nil },
				LookPath:      func(string) (string, error) { return "", os.ErrNotExist },
				RunCommand: func(context.Context, string, ...string) ([]byte, error) {
					return nil, os.ErrNotExist
				},
				DiscoverApp: func(start string) (doctor.AppInfo, appcfg.Config, bool, error) {
					if start != root {
						t.Fatalf("discovery start = %q", start)
					}
					return doctor.AppInfo{}, appcfg.Config{}, false, tc.err
				},
			})
			if got := doctorHasCheck(resp.Checks, "app.root"); got != tc.want {
				t.Fatalf("app.root present = %v, want %v", got, tc.want)
			}
			for _, check := range resp.Checks {
				if check.ID == "app.root" && (check.Status != doctor.StatusError || resp.OK || !strings.Contains(check.Message, tc.err.Error())) {
					t.Fatalf("discovery failure was not reported: %+v", check)
				}
			}
		})
	}
}
