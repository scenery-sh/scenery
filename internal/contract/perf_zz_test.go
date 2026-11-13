package contract

import "testing"

// These benchmarks cover the per-request contract codec path: a generated
// package marshals/validates record fields with constant type expressions,
// constant constraint patterns, and a constant compiled validation program.

func BenchmarkMarshalContractValueWireTypeParse(b *testing.B) {
	value, err := ParseRelativePath("assets/logo.svg")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := MarshalContractValue(value, "relative_path"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalContractValueNestedWireType(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := MarshalContractValue(Some("value"), "optional(string)"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidateContractValuePattern(b *testing.B) {
	constraints := ContractConstraints{Pattern: `^[a-z][a-z0-9_]*$`}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := ValidateContractValue("service_name", "string", constraints); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkContractCacheBeforeAfter(b *testing.B) {
	b.Run("wire-type/uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := parseContractWireTypeUncached("optional(list(relative_path))"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("wire-type/cached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := parseContractWireType("optional(list(relative_path))"); err != nil {
				b.Fatal(err)
			}
		}
	})
	const pattern = `^[a-z][a-z0-9_]*$`
	b.Run("pattern/uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			compiled, err := regexpCompileForBench(pattern)
			if err != nil || !compiled.MatchString("service_name") {
				b.Fatal("unexpected pattern result")
			}
		}
	})
	b.Run("pattern/cached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !contractPatternMatches(pattern, "service_name") {
				b.Fatal("unexpected pattern result")
			}
		}
	})
}
