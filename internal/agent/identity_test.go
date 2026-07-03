package agent

import (
	"encoding/json"
	"testing"
)

func TestShouldReplaceAgent(t *testing.T) {
	cases := []struct {
		name    string
		current Identity
		running Identity
		want    bool
	}{
		{
			name:    "zero current identity accepts any agent",
			current: Identity{},
			running: Identity{},
			want:    false,
		},
		{
			name:    "zero current identity accepts newer agent",
			current: Identity{},
			running: Identity{Version: "0.4.0"},
			want:    false,
		},
		{
			name:    "missing running identity triggers replace",
			current: Identity{Version: "0.4.0"},
			running: Identity{},
			want:    true,
		},
		{
			name:    "newer semver replaces",
			current: Identity{Version: "0.4.1"},
			running: Identity{Version: "0.4.0"},
			want:    true,
		},
		{
			name:    "older semver does not replace",
			current: Identity{Version: "0.4.0"},
			running: Identity{Version: "0.4.1"},
			want:    false,
		},
		{
			name:    "equal semver does not replace",
			current: Identity{Version: "0.4.0"},
			running: Identity{Version: "0.4.0"},
			want:    false,
		},
		{
			name:    "v-prefixed and bare semver compare",
			current: Identity{Version: "v0.5.0"},
			running: Identity{Version: "0.4.9"},
			want:    true,
		},
		{
			name:    "equal semver with newer built_at replaces",
			current: Identity{Version: "0.4.0", BuiltAt: "2026-07-03T12:00:00Z"},
			running: Identity{Version: "0.4.0", BuiltAt: "2026-07-01T12:00:00Z"},
			want:    true,
		},
		{
			name:    "dev builds compare by built_at",
			current: Identity{Version: "dev", Commit: "bbb", BuiltAt: "2026-07-03T12:00:00Z"},
			running: Identity{Version: "dev", Commit: "aaa", BuiltAt: "2026-07-01T12:00:00Z"},
			want:    true,
		},
		{
			name:    "dev builds with older built_at do not replace",
			current: Identity{Version: "dev", BuiltAt: "2026-07-01T12:00:00Z"},
			running: Identity{Version: "dev", BuiltAt: "2026-07-03T12:00:00Z"},
			want:    false,
		},
		{
			name:    "dev builds without built_at do not replace",
			current: Identity{Version: "dev", Commit: "bbb"},
			running: Identity{Version: "dev", Commit: "aaa"},
			want:    false,
		},
		{
			name:    "unparseable running built_at does not replace",
			current: Identity{Version: "dev", BuiltAt: "2026-07-03T12:00:00Z"},
			running: Identity{Version: "dev", BuiltAt: "not-a-time"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldReplaceAgent(tc.current, tc.running); got != tc.want {
				t.Fatalf("ShouldReplaceAgent(%+v, %+v) = %v, want %v", tc.current, tc.running, got, tc.want)
			}
		})
	}
}

func TestOldAgentHealthWithoutIdentityTriggersReplace(t *testing.T) {
	// Health payload shape emitted by agents that predate identity reporting.
	raw := `{
		"schema_version": "scenery.agent.state.v1",
		"pid": 4242,
		"socket_path": "/tmp/agent.sock",
		"router_addr": "127.0.0.1:9440",
		"router_scheme": "http",
		"dashboard_backend": {"network": "tcp", "addr": "127.0.0.1:5000"}
	}`
	var health HealthResponse
	if err := json.Unmarshal([]byte(raw), &health); err != nil {
		t.Fatalf("unmarshal old health payload: %v", err)
	}
	if !health.Identity.IsZero() {
		t.Fatalf("expected zero identity from old health payload, got %+v", health.Identity)
	}
	current := Identity{Version: "dev", Commit: "abc123", BuiltAt: "2026-07-03T12:00:00Z"}
	if !ShouldReplaceAgent(current, health.Identity) {
		t.Fatalf("expected old agent without identity to trigger replace")
	}
}
