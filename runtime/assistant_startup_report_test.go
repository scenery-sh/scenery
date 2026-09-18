package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/runtimeassets"
)

func readAssistantStartupOutput(t *testing.T, path, address string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Assistants []assistantStartupEntry `json:"assistants"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	for _, entry := range report.Assistants {
		if entry.AssistantAddress == address {
			return entry.Output
		}
	}
	return nil
}

func TestAssistantStartupOutputKeepsLinesSplitAcrossWritesAndTheLastUnterminatedLine(t *testing.T) {
	root := t.TempDir()
	report := &assistantStartupReport{path: filepath.Join(root, assistantStartupReportFile), entries: map[string]*assistantStartupEntry{}}
	writer := &assistantStartupOutput{report: report, address: "app/assistant/support"}
	for _, chunk := range []string{"listen", "ing on 127.0.0.1\nstart", "ed\n", "fatal: provider failed"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	// The unterminated last line is not a line yet while the helper may still
	// write; once it stopped, it is kept.
	if got := readAssistantStartupOutput(t, report.path, "app/assistant/support"); !slices.Equal(got, []string{"listening on 127.0.0.1", "started"}) {
		t.Fatalf("output before the helper stopped = %q", got)
	}
	writer.flush()
	want := []string{"listening on 127.0.0.1", "started", "fatal: provider failed"}
	if got := readAssistantStartupOutput(t, report.path, "app/assistant/support"); !slices.Equal(got, want) {
		t.Fatalf("output after the helper stopped = %q, want %q", got, want)
	}
	writer.flush()
	if got := readAssistantStartupOutput(t, report.path, "app/assistant/support"); len(got) != len(want) {
		t.Fatalf("a second flush repeated output: %q", got)
	}
}

func TestAssistantInstallDetailNamesExtractionAndReuse(t *testing.T) {
	for _, test := range []struct {
		node, capsule bool
		want          string
	}{
		{false, false, "node=extracted capsule=extracted"},
		{true, true, "node=reused capsule=reused"},
		{true, false, "node=reused capsule=extracted"},
	} {
		if got := assistantInstallDetail(runtimeassets.InstallResult{Reused: test.node}, runtimeassets.InstallResult{Reused: test.capsule}); got != test.want {
			t.Fatalf("detail = %q, want %q", got, test.want)
		}
	}
}
