package edge

import "testing"

func TestParseHelperLaunchStatusUsesTopLevelState(t *testing.T) {
	t.Parallel()

	state, pid, err := ParseHelperLaunchStatus(`system/dev.scenery.edge-helper = {
	state = spawn scheduled

	resource coalition = {
		state = active
	}
	pid = 1234
}`)
	if err != nil {
		t.Fatal(err)
	}
	if state != "spawn scheduled" || pid != 1234 {
		t.Fatalf("ParseHelperLaunchStatus() = %q, %d", state, pid)
	}
}
