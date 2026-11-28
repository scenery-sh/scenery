package repoinfo

import (
	"slices"
	"testing"

	"scenery.sh/internal/harnessreport"
)

func TestPopulateChangedAreaPolyglotSDKs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		path        string
		category    string
		wantCommand string
	}{
		{
			name:        "rust kernel",
			path:        "sdk/rust/scenery-kernel/src/host.rs",
			category:    "rust-kernel",
			wantCommand: "cargo test --locked --manifest-path sdk/rust/scenery-kernel/Cargo.toml",
		},
		{
			name:        "rust sdk",
			path:        "sdk/rust/scenery-sdk/src/lib.rs",
			category:    "rust-sdk",
			wantCommand: "cargo test --locked --manifest-path sdk/rust/scenery-sdk/Cargo.toml",
		},
		{
			name:        "js sdk",
			path:        "sdk/js/scenery-sdk/worker.mjs",
			category:    "js-sdk",
			wantCommand: "go run ./scripts/verify --probe polyglot-tests --summary --write",
		},
		{
			name:        "python sdk",
			path:        "sdk/python/scenery_sdk/worker.py",
			category:    "python-sdk",
			wantCommand: "python3 -B sdk/python/tests/test_worker_wire.py",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var report harnessreport.ChangedAreaReport
			PopulateChangedArea("/repo", &report, []harnessreport.ChangedFile{
				{Path: tc.path, Status: "M"},
			}, nil, nil)

			if len(report.ChangedFiles) != 1 || report.ChangedFiles[0].Category != tc.category {
				t.Fatalf("got changed files %+v, want category %q", report.ChangedFiles, tc.category)
			}
			if !slices.Contains(report.ValidationClasses, ValidationReleaseRuntime) {
				t.Fatalf("validation classes %v missing %q", report.ValidationClasses, ValidationReleaseRuntime)
			}
			if !slices.Contains(report.RecommendedCommands, tc.wantCommand) {
				t.Fatalf("recommended commands %v missing %q", report.RecommendedCommands, tc.wantCommand)
			}
			if !slices.Contains(report.RecommendedCommands, ValidationFullCommand) {
				t.Fatalf("recommended commands %v missing %q", report.RecommendedCommands, ValidationFullCommand)
			}
			if !slices.Contains(report.RelevantDocs, "docs/spec/WORKER-ABI.md") {
				t.Fatalf("relevant docs %v missing docs/spec/WORKER-ABI.md", report.RelevantDocs)
			}
		})
	}
}

func TestPopulateChangedAreaDocsIncludesClaudeMD(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"CLAUDE.md", "AGENTS.md", "docs/CLAUDE.md"} {
		var report harnessreport.ChangedAreaReport
		PopulateChangedArea("/repo", &report, []harnessreport.ChangedFile{
			{Path: path, Status: "M"},
		}, nil, nil)

		if len(report.ChangedFiles) != 1 || report.ChangedFiles[0].Category != "docs" {
			t.Fatalf("%s: got changed files %+v, want category docs", path, report.ChangedFiles)
		}
		if !slices.Equal(report.ValidationClasses, []string{ValidationDocumentation}) {
			t.Fatalf("%s: got validation classes %v, want [%s]", path, report.ValidationClasses, ValidationDocumentation)
		}
		if !slices.Equal(report.RecommendedCommands, []string{ValidationQuickCommand}) {
			t.Fatalf("%s: got recommended commands %v, want [%s]", path, report.RecommendedCommands, ValidationQuickCommand)
		}
	}
}

func TestClassifyHarnessChangedFilePublicRuntimePaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"auth/auth.go",
		"datasource/ds.go",
		"db/db.go",
		"object/object.go",
		"runtime/runtime.go",
		"storage/storage.go",
	} {
		if got := classifyHarnessChangedFile(path); got != "runtime" {
			t.Errorf("classifyHarnessChangedFile(%q) = %q, want runtime", path, got)
		}
	}

	for _, retired := range []string{
		"cron/cron.go",
		"library/lib.go",
		"middleware/mw.go",
		"model/model.go",
		"page/page.go",
		"rlog/log.go",
	} {
		if got := classifyHarnessChangedFile(retired); got != "other" {
			t.Errorf("classifyHarnessChangedFile(%q) = %q, want other", retired, got)
		}
	}
}
