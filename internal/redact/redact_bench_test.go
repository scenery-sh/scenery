package redact

import "testing"

func BenchmarkSensitiveKey(b *testing.B) {
	for _, key := range []string{"", "endpoint", "trace_id", "Authorization", "DATABASE_URL", " API.Key ", "\u2003access-token\u2003", "uživatel"} {
		b.Run(key, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = SensitiveKey(key)
			}
		})
	}
}
