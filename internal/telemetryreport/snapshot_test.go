package telemetryreport

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCapturedFileRetainsItsSelectedExtent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, initial, after string
		want                 []string
		short                bool
	}{
		{name: "append", initial: "first\nlast", after: "first\nlast\nadded\n", want: []string{"first", "last"}},
		{name: "truncate", initial: "first\nlast", after: "first\n", want: []string{"first"}, short: true},
		{name: "empty", initial: "", after: "added\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, []byte(test.initial), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			snapshot, _, err := captureSnapshot(file)
			if err != nil {
				t.Fatal(err)
			}
			// Mutate the same inode after descriptor-bound size capture.
			if err := os.WriteFile(path, []byte(test.after), 0o600); err != nil {
				t.Fatal(err)
			}
			var lines []string
			_, err = readLines(snapshot, 16, func(line []byte) { lines = append(lines, string(line)) })
			wantBytes := min(len(test.initial), len(test.after))
			if !slices.Equal(lines, test.want) || snapshot.size != int64(len(test.initial)) || snapshot.readBytes != int64(wantBytes) || errors.Is(err, io.ErrUnexpectedEOF) != test.short || (err != nil && !test.short) {
				t.Fatalf("lines=%q snapshot=%+v err=%v", lines, snapshot, err)
			}
		})
	}
}

func TestDuplicateSupervisorSegmentsCountOneLogicalPartial(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "agent", "dev", "owned.log")
	at := time.Now().UTC()
	writeLines(t, path, supervisorEventLine("build.step", at, map[string]any{
		"operation_id": "owned", "name": "build.request", "reason": "source_rebuild", "ok": true, "duration_ms": 0,
	}))
	for _, duplicate := range []string{path + ".1", path + ".2"} {
		if err := os.Link(path, duplicate); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Build(Options{AgentHome: home, Until: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sources.Supervisor) != 1 || r.Sources.SupervisorLogsPartial != 1 || r.Builds.Rebuilds.Count != 1 {
		t.Fatalf("logical coverage=%+v rebuilds=%+v", r.Sources, r.Builds.Rebuilds)
	}
	s := r.Sources.Supervisor[0]
	if s.Status != "partial" || s.Segments != 1 || s.Records != 1 || s.Invalid != 0 || s.SnapshotBytes == nil || *s.SnapshotBytes != info.Size() || s.ReadBytes != info.Size() {
		t.Fatalf("deduplicated physical coverage=%+v", s)
	}
}

type snapshotErrorReader struct {
	data []byte
	err  error
}

func (r *snapshotErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestCapturedReaderCountsAllPhysicalEvidence(t *testing.T) {
	t.Parallel()
	fault := errors.New("owned read failure")
	for _, test := range []struct {
		name, input string
		size        int64
		reader      io.Reader
		want        []string
		oversized   int
		wantErr     error
	}{
		{name: "final JSON", input: "{\"ok\":true}", size: 11, want: []string{"{\"ok\":true}"}},
		{name: "oversized", input: "ok\n" + strings.Repeat("x", 100) + "\nafter\nlast", size: 114, want: []string{"ok", "after", "last"}, oversized: 1},
		{name: "early EOF", input: "ok\n", size: 9, want: []string{"ok"}, wantErr: io.ErrUnexpectedEOF},
		{name: "no captured bytes", input: "", size: 9, wantErr: io.ErrUnexpectedEOF},
		{name: "bytes with error", input: "ok\nlast\n", size: 8, reader: &snapshotErrorReader{data: []byte("ok\nlast\n"), err: fault}, want: []string{"ok", "last"}, wantErr: fault},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := test.reader
			if reader == nil {
				reader = strings.NewReader(test.input)
			}
			snapshot := newSnapshotReader(reader, test.size)
			var lines []string
			oversized, err := readLines(snapshot, 16, func(line []byte) { lines = append(lines, string(line)) })
			if !slices.Equal(lines, test.want) || oversized != test.oversized || !errors.Is(err, test.wantErr) || snapshot.readBytes != int64(len(test.input)) {
				t.Fatalf("lines=%q oversized=%d bytes=%d err=%v", lines, oversized, snapshot.readBytes, err)
			}
		})
	}
}

func TestCapturedSegmentStreamPreservesSplitsAndRejectsGaps(t *testing.T) {
	t.Parallel()
	prefix := "{\"before\":1}\n{\"joined\":"
	suffix := "\"value\"}"
	for _, gap := range []bool{false, true} {
		size := len(prefix)
		if gap {
			size += 3
		}
		first := newSnapshotReader(strings.NewReader(prefix), int64(size))
		second := newSnapshotReader(strings.NewReader(suffix), int64(len(suffix)))
		var lines []string
		_, err := readLines(io.MultiReader(first, second), 128, func(line []byte) { lines = append(lines, string(line)) })
		if gap {
			if !errors.Is(err, io.ErrUnexpectedEOF) || !slices.Equal(lines, []string{"{\"before\":1}"}) || second.readBytes != 0 {
				t.Fatalf("parsed across gap: lines=%q second=%+v err=%v", lines, second, err)
			}
		} else if err != nil || !slices.Equal(lines, []string{"{\"before\":1}", "{\"joined\":\"value\"}"}) || first.readBytes+second.readBytes != int64(len(prefix)+len(suffix)) {
			t.Fatalf("intact stream: lines=%q err=%v", lines, err)
		}
	}
}

func TestReportSnapshotDistinguishesEmptyAndUnknown(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	empty := filepath.Join(root, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, status string
		known        bool
	}{{empty, "complete", true}, {filepath.Join(root, "missing.jsonl"), "missing", false}, {root, "unreadable", false}} {
		report, err := Build(Options{CLITelemetryPath: test.path})
		if err != nil {
			t.Fatal(err)
		}
		source := report.Sources.CLI[0]
		if source.Status != test.status || (source.SnapshotBytes != nil) != test.known || source.ReadBytes != 0 || (source.SnapshotBytes != nil && *source.SnapshotBytes != 0) {
			t.Fatalf("coverage=%+v", source)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		want := `"snapshot_bytes":null`
		if test.known {
			want = `"snapshot_bytes":0`
		}
		if !strings.Contains(string(encoded), want) || !strings.Contains(string(encoded), `"read_bytes":0`) {
			t.Fatalf("zero/unknown serialization=%s", encoded)
		}
	}
	for _, path := range []string{empty, filepath.Join(root, "missing.jsonl"), root} {
		tally := readTranscript(Options{}, path, readClaudeTranscript)
		if path == empty {
			if tally.sources.Read != 1 || tally.sources.SnapshotBytes == nil || *tally.sources.SnapshotBytes != 0 {
				t.Fatalf("known empty transcript=%+v", tally.sources)
			}
		} else if tally.sources.SnapshotBytes != nil || tally.sources.ReadBytes != 0 || tally.sources.Failed+tally.sources.Partial != 1 {
			t.Fatalf("unknown transcript=%+v", tally.sources)
		}
	}
}

func TestTranscriptByteCoverageUsesCheckedTotals(t *testing.T) {
	t.Parallel()
	two, three := int64(2), int64(3)
	source := TranscriptSources{SnapshotBytes: &two, ReadBytes: 2}
	source.add(TranscriptSources{SnapshotBytes: &three, ReadBytes: 1, Partial: 1})
	if source.SnapshotBytes == nil || *source.SnapshotBytes != 5 || source.ReadBytes != 3 || source.Partial != 1 || source.byteCountOverflow {
		t.Fatalf("known totals=%+v", source)
	}
	source.add(TranscriptSources{Failed: 1})
	source.add(TranscriptSources{SnapshotBytes: &three, ReadBytes: 3})
	if source.SnapshotBytes != nil || source.ReadBytes != 6 || source.Failed != 1 || source.byteCountOverflow {
		t.Fatalf("unknown selected extent=%+v", source)
	}
	largest, one := int64(math.MaxInt64), int64(1)
	source = TranscriptSources{SnapshotBytes: &largest, ReadBytes: math.MaxInt64}
	source.add(TranscriptSources{SnapshotBytes: &one, ReadBytes: 1})
	if !source.byteCountOverflow || source.SnapshotBytes != nil || source.ReadBytes < 0 {
		t.Fatalf("wrapped byte total=%+v", source)
	}
	if _, valid := addReadBytes(1, -1); valid {
		t.Fatal("accepted negative byte contribution")
	}
}
