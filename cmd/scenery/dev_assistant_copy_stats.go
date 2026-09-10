package main

import (
	"context"
	"time"
)

// Copy timing is diagnostic evidence, not a cache-validity input. Traversal
// includes metadata, membership validation and callback overhead; all copied
// bytes still receive the same digest verification before publication.
type assistantCopyStats struct {
	Started                     time.Time
	Duration, Read, Hash, Write time.Duration
	Entries, Files              int
	Bytes                       int64
}

func (s *assistantSupervisor) traceAssistantCache(ctx context.Context, definition assistantDefinition, cache *assistantOverlayCache) {
	cache.onCopy = func(stats assistantCopyStats, err error) {
		milliseconds := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		s.emit(ctx, definition, "info", "assistant.step", map[string]any{
			"name": "assistant.cache_copy", "assistant": definition.Address,
			"started_at":  stats.Started.UTC().Format(time.RFC3339Nano),
			"duration_ms": milliseconds(stats.Duration), "cache": "hit",
			"reason": "verified_independent_copy", "ok": err == nil,
			"entries": stats.Entries, "files": stats.Files, "bytes": stats.Bytes,
			"read_ms": milliseconds(stats.Read), "hash_ms": milliseconds(stats.Hash),
			"write_ms":     milliseconds(stats.Write),
			"traversal_ms": milliseconds(stats.Duration - stats.Read - stats.Hash - stats.Write),
		})
	}
	cache.onRelocate = func(started time.Time, err error) {
		s.emitStep(ctx, definition, "assistant.cache_relocate", started, "hit", "private_manifest_relocation", err)
	}
}
