package runtime

import (
	"strings"
	"testing"
)

func TestTraceParentRejectsMalformedIdentities(t *testing.T) {
	for _, input := range []string{"", "00-" + strings.Repeat("0", 32) + "-1111111111111111-01", "00-" + strings.Repeat("A", 32) + "-1111111111111111-01", "00-" + strings.Repeat("a", 32) + "-0000000000000000-01"} {
		if _, _, ok := parseTraceParent(input); ok {
			t.Fatalf("accepted %q", input)
		}
	}
}
