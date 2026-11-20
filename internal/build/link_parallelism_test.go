package build

import (
	"slices"
	"testing"
)

func TestDevelopmentLinkParallelismFitsLinksInAQuarterOfMemory(t *testing.T) {
	t.Parallel()
	const gib = uint64(1) << 30
	for _, check := range []struct {
		name    string
		pending int
		cpus    int
		memory  uint64
		want    int
	}{
		{name: "memory fits one link per CPU", pending: 44, cpus: 24, memory: 64 * gib, want: 0},
		{name: "small host relinking everything", pending: 44, cpus: 10, memory: 16 * gib, want: 8},
		{name: "small host relinking a few services", pending: 3, cpus: 10, memory: 16 * gib, want: 0},
		{name: "tiny host still links", pending: 44, cpus: 8, memory: gib, want: 1},
		{name: "unknown memory", pending: 44, cpus: 24, memory: 0, want: 0},
	} {
		if got := developmentLinkParallelism(check.pending, check.cpus, check.memory); got != check.want {
			t.Errorf("%s: parallelism = %d, want %d", check.name, got, check.want)
		}
	}
	args := []string{"build", "-trimpath", "-o", "out/", "example.test/app/host"}
	if got := withDevelopmentLinkParallelism(args, nil, 8); !slices.Equal(got, []string{"build", "-p", "8", "-trimpath", "-o", "out/", "example.test/app/host"}) {
		t.Errorf("bounded build args = %v", got)
	}
	for _, flags := range [][]string{{"-p=2"}, {"-p", "2"}} {
		if got := withDevelopmentLinkParallelism(args, flags, 8); !slices.Equal(got, args) {
			t.Errorf("configured %v was overridden: %v", flags, got)
		}
	}
	if got := withDevelopmentLinkParallelism(args, nil, 0); !slices.Equal(got, args) {
		t.Errorf("unbounded build args = %v", got)
	}
}
