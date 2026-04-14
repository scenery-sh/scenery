package termstyle

import (
	"bytes"
	"testing"
)

func TestCLICOLORFORCEOverridesNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")

	palette := New(&bytes.Buffer{})
	if !palette.Enabled() {
		t.Fatal("palette should enable color when CLICOLOR_FORCE=1")
	}
}
