package main

import (
	"strings"
	"testing"
)

func TestReportSnapshotReadObservation(t *testing.T) {
	t.Parallel()
	want := harnessReportDescriptor{Path: "/owned", Size: 100, Inode: "17", Device: "2"}
	valid := want
	valid.FD, valid.Offset, valid.Mode = "4", 50, "read"
	for name, change := range map[string]func(*harnessReportDescriptor){
		"interior":     func(*harnessReportDescriptor) {},
		"zero":         func(r *harnessReportDescriptor) { r.Offset = 0 },
		"endpoint":     func(r *harnessReportDescriptor) { r.Offset = 100 },
		"beyond":       func(r *harnessReportDescriptor) { r.Offset = 101 },
		"negative":     func(r *harnessReportDescriptor) { r.Offset = -1 },
		"writer":       func(r *harnessReportDescriptor) { r.Mode = "write" },
		"unknown-mode": func(r *harnessReportDescriptor) { r.Mode = "" },
		"wrong-inode":  func(r *harnessReportDescriptor) { r.Inode = "18" },
		"wrong-device": func(r *harnessReportDescriptor) { r.Device = "3" },
		"wrong-path":   func(r *harnessReportDescriptor) { r.Path = "/other" },
		"wrong-size":   func(r *harnessReportDescriptor) { r.Size = 101 },
	} {
		t.Run(name, func(t *testing.T) {
			row := valid
			change(&row)
			if err := validateHarnessReportRead([]harnessReportDescriptor{row}, []harnessReportDescriptor{want}); (err == nil) != (name == "interior") {
				t.Fatalf("observation %+v: %v", row, err)
			}
		})
	}
	for _, rows := range [][]harnessReportDescriptor{nil, {valid, valid}} {
		if validateHarnessReportRead(rows, []harnessReportDescriptor{want}) == nil {
			t.Fatal("missing/duplicate accepted")
		}
	}
	second := want
	second.Path, second.Inode = "/second", "18"
	row := second
	row.FD, row.Mode = "5", "read"
	first := valid
	first.Offset = 100
	if err := validateHarnessReportRead([]harnessReportDescriptor{first, row}, []harnessReportDescriptor{want, second}); err != nil {
		t.Fatalf("captured segment stream with unread suffix: %v", err)
	}
	row.FD = first.FD
	if validateHarnessReportRead([]harnessReportDescriptor{first, row}, []harnessReportDescriptor{want, second}) == nil {
		t.Fatal("duplicate descriptor accepted")
	}
}

func TestReportSnapshotHumanCoverage(t *testing.T) {
	t.Parallel()
	const coverage = "  Transcripts: 0 read, 1 read in part, 0 unreadable; read 512 / captured 1024 bytes; 0 invalid and 0 oversized records skipped, 0 results without a call, 0 calls without a result.\n"
	const agent = "Agents: 1 tool calls, 0 errors; 1 shell commands ran Scenery (1 invocations): 1 recorded Scenery's own outcome (0 failed); as shell commands 0 failed and 0 recorded no outcome\n  help  1  attributable 1  failed 0  waited 0ms  p50 -\n"
	for name, output := range map[string]string{
		"valid":         coverage + agent,
		"zero-partial":  strings.Replace(coverage, "0 read, 1 read in part", "1 read, 0 read in part", 1) + agent,
		"wrong-owner":   strings.Replace(coverage, "Transcripts:", "CLI /other:", 1) + agent,
		"wrong-failed":  strings.Replace(coverage, "0 unreadable", "1 unreadable", 1) + agent,
		"wrong-outcome": coverage + strings.Replace(agent, "attributable 1", "attributable 0", 1),
	} {
		if err := validateHarnessReportHuman(output, "codex-truncate", "/owned", 512, 1024, 512); (err == nil) != (name == "valid") {
			t.Errorf("%s: %v", name, err)
		}
	}
	const supervisor = "  Supervisor /owned (partial): read 512 / captured 1024 bytes across 1 segments; retained 1, in window 1, invalid 0.\n  rebuilds 1 failed 0 timed successes n=1 p50 0ms p95 0ms\n"
	if err := validateHarnessReportHuman(supervisor, "supervisor-truncate", "/owned", 512, 1024, 512); err != nil {
		t.Fatal(err)
	}
	if validateHarnessReportHuman(strings.Replace(supervisor, "/owned", "/other", 1), "supervisor-truncate", "/owned", 512, 1024, 512) == nil {
		t.Fatal("unrelated supervisor ratio accepted")
	}
}
