package gotarget

import (
	"slices"
	"testing"
)

func TestWithTrimpathPrecedesConfiguredFlagsOnce(t *testing.T) {
	t.Parallel()
	if got := WithTrimpath([]string{"-tags=native"}); !slices.Equal(got, []string{"-trimpath", "-tags=native"}) {
		t.Fatalf("WithTrimpath = %q", got)
	}
	if got := WithTrimpath(nil); !slices.Equal(got, []string{"-trimpath"}) {
		t.Fatalf("WithTrimpath(nil) = %q", got)
	}
	for _, configured := range [][]string{{"-trimpath"}, {"-v", "-trimpath=false"}} {
		if got := WithTrimpath(configured); !slices.Equal(got, configured) {
			t.Fatalf("configured %q became %q", configured, got)
		}
	}
}
