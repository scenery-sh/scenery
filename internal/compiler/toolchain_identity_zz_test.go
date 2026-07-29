package compiler

import (
	"runtime"
	"testing"

	"scenery.sh/internal/parse"
)

func newToolchainIdentityTarget() parse.GoTargetContext {
	return parse.GoTargetContext{
		ModuleRoot: ".",
		Patterns:   []string{"./..."},
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	}
}

// TestResolveGoToolIdentitiesIsStableAcrossCalls pins that memoizing the
// toolchain resolution returns byte-identical identities, since those digests
// feed implementation revisions.
func TestResolveGoToolIdentitiesIsStableAcrossCalls(t *testing.T) {
	first := newToolchainIdentityTarget()
	if err := resolveGoToolIdentities(&first); err != nil {
		t.Skipf("toolchain not resolvable in this environment: %v", err)
	}
	second := newToolchainIdentityTarget()
	if err := resolveGoToolIdentities(&second); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if len(first.ToolchainIdentity) == 0 {
		t.Fatal("first resolve produced no toolchain identity")
	}
	if len(first.ToolchainIdentity) != len(second.ToolchainIdentity) {
		t.Fatalf("identity key count changed: %v vs %v", first.ToolchainIdentity, second.ToolchainIdentity)
	}
	for key, want := range first.ToolchainIdentity {
		if got := second.ToolchainIdentity[key]; got != want {
			t.Fatalf("toolchain identity %q = %q, want %q", key, got, want)
		}
	}
}

func BenchmarkResolveGoToolIdentities(b *testing.B) {
	warm := newToolchainIdentityTarget()
	if err := resolveGoToolIdentities(&warm); err != nil {
		b.Skipf("toolchain not resolvable in this environment: %v", err)
	}
	clearCaches := func() {
		goToolEnvCache.Range(func(k, _ any) bool { goToolEnvCache.Delete(k); return true })
		executableDigestCache.Range(func(k, _ any) bool { executableDigestCache.Delete(k); return true })
	}
	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			clearCaches()
			target := newToolchainIdentityTarget()
			if err := resolveGoToolIdentities(&target); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		warm := newToolchainIdentityTarget()
		if err := resolveGoToolIdentities(&warm); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			target := newToolchainIdentityTarget()
			if err := resolveGoToolIdentities(&target); err != nil {
				b.Fatal(err)
			}
		}
	})
}
