package main

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// A ledger has one clock domain. Cross-process wall clocks and child-local
// durations must be reported in separate ledgers, not silently aligned.
type nativeAttributionSpan struct {
	ID        string  `json:"id"`
	Parent    string  `json:"parent,omitempty"`
	Category  string  `json:"category"`
	Source    string  `json:"source"`
	StartMS   float64 `json:"start_ms"`
	EndMS     float64 `json:"end_ms"`
	Enclosing bool    `json:"enclosing,omitempty"`
	Instances int     `json:"instances,omitempty"`
}

type nativeAttributionUnknown struct {
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

type nativeAttributionLedger struct {
	Clock      string                     `json:"clock"`
	DurationMS float64                    `json:"duration_ms"`
	Spans      []nativeAttributionSpan    `json:"spans"`
	Unknown    []nativeAttributionUnknown `json:"unknown,omitempty"`
}

type nativeAttributionAccounting struct {
	CoveredUnionMS float64            `json:"covered_union_ms"`
	ResidualMS     float64            `json:"unattributed_residual_ms"`
	OverlapMS      float64            `json:"overlap_ms"`
	CategoryUnion  map[string]float64 `json:"category_union_ms"`
}

func nativeAttributionFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Account only non-enclosing observations. A total/build/action container is
// useful context but cannot explain its own unobserved interior.
func (ledger nativeAttributionLedger) account() (nativeAttributionAccounting, error) {
	result := nativeAttributionAccounting{CategoryUnion: map[string]float64{}}
	if ledger.Clock == "" || !nativeAttributionFinite(ledger.DurationMS) || ledger.DurationMS < 0 {
		return result, fmt.Errorf("invalid attribution clock or duration")
	}
	byID := make(map[string]nativeAttributionSpan, len(ledger.Spans))
	for _, span := range ledger.Spans {
		if span.ID == "" || span.Category == "" || span.Source == "" ||
			!nativeAttributionFinite(span.StartMS) || !nativeAttributionFinite(span.EndMS) ||
			span.StartMS < 0 || span.EndMS < span.StartMS || span.EndMS > ledger.DurationMS {
			return result, fmt.Errorf("invalid attribution span %q", span.ID)
		}
		if _, exists := byID[span.ID]; exists {
			return result, fmt.Errorf("duplicate attribution span %q", span.ID)
		}
		byID[span.ID] = span
	}
	var leaves []nativeAttributionSpan
	groups := map[string][]nativeAttributionSpan{}
	var summed float64
	for _, span := range ledger.Spans {
		seen := map[string]bool{span.ID: true}
		for parentID := span.Parent; parentID != ""; {
			parent, exists := byID[parentID]
			if !exists || seen[parentID] || !parent.Enclosing ||
				span.StartMS < parent.StartMS || span.EndMS > parent.EndMS {
				return result, fmt.Errorf("invalid attribution parent %q for %q", parentID, span.ID)
			}
			seen[parentID] = true
			parentID = parent.Parent
		}
		if !span.Enclosing {
			leaves = append(leaves, span)
			groups[span.Category] = append(groups[span.Category], span)
			summed += span.EndMS - span.StartMS
		}
	}
	for _, unknown := range ledger.Unknown {
		if unknown.Category == "" || strings.TrimSpace(unknown.Reason) == "" {
			return result, fmt.Errorf("unobserved attribution requires a category and reason")
		}
	}
	result.CoveredUnionMS = nativeAttributionUnion(leaves)
	result.ResidualMS = math.Max(0, ledger.DurationMS-result.CoveredUnionMS)
	result.OverlapMS = math.Max(0, summed-result.CoveredUnionMS)
	for category, spans := range groups {
		result.CategoryUnion[category] = nativeAttributionUnion(spans)
	}
	return result, nil
}

func nativeAttributionUnion(spans []nativeAttributionSpan) float64 {
	ordered := slices.Clone(spans)
	slices.SortFunc(ordered, func(a, b nativeAttributionSpan) int {
		if a.StartMS < b.StartMS {
			return -1
		}
		if a.StartMS > b.StartMS {
			return 1
		}
		return 0
	})
	var total, end float64
	for _, span := range ordered {
		start := math.Max(end, span.StartMS)
		if span.EndMS > start {
			total += span.EndMS - start
		}
		end = math.Max(end, span.EndMS)
	}
	return total
}

type nativeGoTraceEvent struct {
	Name  string  `json:"name"`
	Phase string  `json:"ph"`
	Time  float64 `json:"ts"`
	TID   uint64  `json:"tid"`
}

// Stock Go's diagnostic trace uses Unix microseconds, not a transferable
// monotonic clock. Normalize within that trace only and retain this limitation.
// Go can reuse a TID across concurrent work. Match each name/TID's active
// interval union rather than inventing stack ancestry or individual durations.
// This version-bound parser intentionally rejects unmatched/truncated spans.
func nativeAttributionGoTrace(data []byte) (nativeAttributionLedger, error) {
	ledger := nativeAttributionLedger{Clock: "cmd/go diagnostic Unix microseconds; trace-local origin"}
	var events []nativeGoTraceEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return ledger, err
	}
	type key struct {
		tid  uint64
		name string
	}
	type pending struct {
		start                   float64
		count, instances, index int
	}
	active := map[key]pending{}
	origin, end := math.Inf(1), math.Inf(-1)
	for i, event := range events {
		if !nativeAttributionFinite(event.Time) || event.Time < 0 {
			return ledger, fmt.Errorf("invalid Go trace timestamp at event %d", i)
		}
		if event.Phase != "B" && event.Phase != "E" && event.Phase != "s" && event.Phase != "f" {
			return ledger, fmt.Errorf("unsupported Go trace phase %q", event.Phase)
		}
	}
	// Serialization can lag timestamp capture in concurrent producers. Sorting
	// within this single trace clock preserves unions without parent alignment.
	slices.SortStableFunc(events, func(a, b nativeGoTraceEvent) int {
		if a.Time < b.Time {
			return -1
		}
		if a.Time > b.Time {
			return 1
		}
		if a.Phase == "B" && b.Phase != "B" {
			return -1
		}
		if b.Phase == "B" && a.Phase != "B" {
			return 1
		}
		return 0
	})
	for i, event := range events {
		k := key{event.TID, event.Name}
		switch event.Phase {
		case "s", "f": // Dependency flows remain available in the raw trace.
			continue
		case "B":
			if event.Name == "" {
				return ledger, fmt.Errorf("unnamed Go trace span at event %d", i)
			}
			opened := active[k]
			if opened.count == 0 {
				opened.start, opened.index = event.Time, i
			}
			opened.count++
			opened.instances++
			active[k] = opened
			origin = math.Min(origin, event.Time)
		case "E":
			opened := active[k]
			if opened.count == 0 {
				return ledger, fmt.Errorf("unmatched Go trace end at event %d", i)
			}
			opened.count--
			if opened.count != 0 {
				active[k] = opened
				continue
			}
			delete(active, k)
			category := "driver_container"
			if strings.HasPrefix(event.Name, "load.") || strings.HasPrefix(event.Name, "modload.") {
				category = "package_loading"
			}
			ledger.Spans = append(ledger.Spans, nativeAttributionSpan{
				ID: fmt.Sprintf("go-%d", opened.index), Category: category, Source: event.Name,
				StartMS: opened.start, EndMS: event.Time, Instances: opened.instances,
				Enclosing: event.Name != "load.PackagesAndErrors",
			})
			end = math.Max(end, event.Time)
		default:
			return ledger, fmt.Errorf("unsupported Go trace phase %q", event.Phase)
		}
	}
	if len(active) != 0 {
		return ledger, fmt.Errorf("incomplete Go trace")
	}
	if len(ledger.Spans) == 0 {
		return ledger, fmt.Errorf("go trace contains no completed spans")
	}
	ledger.DurationMS = (end - origin) / 1000
	for i := range ledger.Spans {
		ledger.Spans[i].StartMS = (ledger.Spans[i].StartMS - origin) / 1000
		ledger.Spans[i].EndMS = (ledger.Spans[i].EndMS - origin) / 1000
	}
	if _, err := ledger.account(); err != nil {
		return ledger, err
	}
	return ledger, nil
}
