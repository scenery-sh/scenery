package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPostgresPrerequisiteScopeIsNotRuntimeReadiness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                          string
		configured, docker, reachable bool
		status, proof                 string
	}{
		{"not configured", false, false, false, StatusSkipped, ""},
		{"missing Docker", true, false, false, StatusError, "none"},
		{"unreachable Docker", true, true, false, StatusError, "none"},
		{"reachable Docker", true, true, true, StatusOK, "docker_engine_reachability_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			deps := ProbeDeps{
				LookPath: func(string) (string, error) {
					if tc.docker {
						return "/docker", nil
					}
					return "", errors.New("not found")
				},
				RunCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
					calls++
					if name != "/docker" || !slices.Equal(args, []string{"info", "--format", "{{json .}}"}) {
						t.Fatalf("non-read-only or unexpected doctor command: %s %v", name, args)
					}
					if tc.reachable {
						return []byte(`{"ServerVersion":"test"}`), nil
					}
					return []byte("private-password in untrusted Docker stderr"), errors.New("private-password")
				},
			}
			check := PostgresServerCheck(t.Context(), deps, AppFeatures{PostgresServices: tc.configured})
			if check.Status != tc.status {
				t.Fatalf("check = %+v", check)
			}
			if tc.configured && (check.Observed["runtime_verified"] != false || check.Observed["proof"] != tc.proof) {
				t.Fatalf("overstated proof: %+v", check)
			}
			if tc.reachable && !strings.Contains(check.Message, "were not checked") {
				t.Fatalf("missing scope limitation: %+v", check)
			}
			if (!tc.configured || !tc.docker) && calls != 0 {
				t.Fatalf("unexpected Docker calls: %d", calls)
			}
			encoded, _ := json.Marshal(check)
			if strings.Contains(string(encoded), "private-password") || strings.Contains(string(encoded), "database_url_env") {
				t.Fatalf("unsafe or obsolete recovery advice: %s", encoded)
			}
		})
	}
}

func TestDockerChecksDoNotPublishRawFailureOutput(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{true, false} {
		deps := ProbeDeps{
			LookPath: func(string) (string, error) { return "/docker", nil },
			RunCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "context" && !fail {
					return []byte("desktop-linux"), nil
				}
				if fail {
					return []byte("private-password in untrusted stderr"), errors.New("private-password")
				}
				return []byte("private-password in invalid engine JSON"), nil
			},
		}
		checks := DockerChecks(t.Context(), deps)
		encoded, _ := json.Marshal(checks)
		if strings.Contains(string(encoded), "private-password") {
			t.Fatalf("Docker output escaped: %s", encoded)
		}
		if checks[len(checks)-1].Status != StatusWarn {
			t.Fatalf("failed/malformed probe reported ready: %+v", checks)
		}
	}
}
