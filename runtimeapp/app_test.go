package runtimeapp

import "testing"

func TestStandaloneLocalProxyDisabledValues(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off"} {
		t.Setenv("ONLAVA_LOCAL_PROXY", value)
		if !standaloneLocalProxyDisabled() {
			t.Fatalf("standaloneLocalProxyDisabled() = false for %q", value)
		}
	}
	for _, value := range []string{"", "1", "true", "yes", "on", "garbage"} {
		t.Setenv("ONLAVA_LOCAL_PROXY", value)
		if standaloneLocalProxyDisabled() {
			t.Fatalf("standaloneLocalProxyDisabled() = true for %q", value)
		}
	}
}
