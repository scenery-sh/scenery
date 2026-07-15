package doctor

import "testing"

func TestParseGoToolchainVersion(t *testing.T) {
	cases := []struct {
		output string
		want   string
		ok     bool
	}{
		{"go version go1.26.3 darwin/arm64", "go1.26.3", true},
		{"go version go1.26 linux/amd64", "go1.26", true},
		{"go version devel +abcdef", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := parseGoToolchainVersion(tc.output)
		if ok != tc.ok {
			t.Fatalf("parseGoToolchainVersion(%q) ok = %v, want %v", tc.output, ok, tc.ok)
		}
		if ok && got.String() != tc.want {
			t.Fatalf("parseGoToolchainVersion(%q) = %s, want %s", tc.output, got.String(), tc.want)
		}
	}
}

func TestGoVersionCompare(t *testing.T) {
	minimum := goVersion{Major: minGoMajor, Minor: minGoMinor}
	if v := (goVersion{Major: 1, Minor: 25, Patch: 9}); v.compare(minimum) >= 0 {
		t.Fatalf("go1.25.9 should be below the minimum")
	}
	if v := (goVersion{Major: 1, Minor: 26}); v.compare(minimum) != 0 {
		t.Fatalf("go1.26 should equal the minimum")
	}
	if v := (goVersion{Major: 1, Minor: 27, Patch: 1}); v.compare(minimum) <= 0 {
		t.Fatalf("go1.27.1 should be above the minimum")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{2 * 1024, "2.0 KiB"},
		{5 * 1024 * 1024 * 1024, "5.0 GiB"},
		{20 * 1024 * 1024 * 1024, "20 GiB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.in); got != tc.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	summary := Summarize([]Check{
		{Status: StatusOK},
		{Status: StatusOK},
		{Status: StatusWarn},
		{Status: StatusError},
		{Status: StatusSkipped},
	})
	want := Summary{OK: 2, Warnings: 1, Errors: 1, Skipped: 1}
	if summary != want {
		t.Fatalf("Summarize = %+v, want %+v", summary, want)
	}
}

func TestCPUCheckWarnsOnSingleCPU(t *testing.T) {
	if check := CPUCheck(8); check.Status != StatusOK || check.ID != "resource.cpu" {
		t.Fatalf("CPUCheck(8) = %+v", check)
	}
	check := CPUCheck(1)
	if check.Status != StatusWarn || check.Severity != SeverityOptional {
		t.Fatalf("CPUCheck(1) = %+v", check)
	}
}

func TestMemoryCheckThresholds(t *testing.T) {
	if check := MemoryCheck(MemoryInfo{TotalBytes: 8 * 1024 * 1024 * 1024}); check.Status != StatusOK {
		t.Fatalf("8 GiB memory check = %+v", check)
	}
	if check := MemoryCheck(MemoryInfo{TotalBytes: 3 * 1024 * 1024 * 1024}); check.Status != StatusWarn {
		t.Fatalf("3 GiB memory check = %+v", check)
	}
	if check := MemoryCheck(MemoryInfo{TotalBytes: 1 * 1024 * 1024 * 1024}); check.Status != StatusError {
		t.Fatalf("1 GiB memory check = %+v", check)
	}
}
