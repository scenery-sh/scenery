package main

import (
	"math"
	"reflect"
	"testing"
)

func TestNativeAttributionPairStatisticsPreserveFirstExecution(t *testing.T) {
	t.Parallel()
	pair := nativeAttributionPair{
		First:                   nativeReloadSample{BuildMS: 500, LaunchAttestMS: 390, ActivationMS: 10, InputsUnchanged: true},
		Repeated:                nativeAttributionExecution{LaunchAttestMS: 29, ActivationMS: 1, SameArtifact: true},
		FirstMinusRepeatReadyMS: 370,
	}
	got := nativeAttributionPairStats([]nativeAttributionPair{pair})
	if got["valid"] != true {
		t.Fatalf("valid pair rejected: %+v", got)
	}
	// Compare with the same quantile owner without depending on its representation.
	first := nativeReloadStats([]float64{400})
	repeated := nativeReloadStats([]float64{30})
	if !reflect.DeepEqual(got["first_launch_ready"], first) || !reflect.DeepEqual(got["repeated_launch_ready"], repeated) {
		t.Fatalf("first execution replaced or pooled: %+v", got)
	}
	for _, invalidate := range []func(*nativeAttributionPair){
		func(p *nativeAttributionPair) { p.First.Error = "failed first execution" },
		func(p *nativeAttributionPair) { p.Repeated.Error = "failed repeat" },
		func(p *nativeAttributionPair) { p.First.InputsUnchanged = false },
		func(p *nativeAttributionPair) { p.Repeated.SameArtifact = false },
	} {
		invalid := pair
		invalidate(&invalid)
		if got := nativeAttributionPairStats([]nativeAttributionPair{pair, invalid}); got["valid"] != false {
			t.Fatalf("invalid pair silently omitted: %+v", got)
		}
	}
}

func TestNativeAttributionUsesUnionsNotEnclosingTotals(t *testing.T) {
	t.Parallel()
	ledger := nativeAttributionLedger{Clock: "parent monotonic", DurationMS: 100,
		Spans: []nativeAttributionSpan{
			{ID: "driver", Category: "build", Source: "parent", StartMS: 0, EndMS: 100, Enclosing: true},
			{ID: "a", Parent: "driver", Category: "tool", Source: "trace", StartMS: 10, EndMS: 40},
			{ID: "b", Parent: "driver", Category: "tool", Source: "trace", StartMS: 20, EndMS: 50},
			{ID: "c", Parent: "driver", Category: "artifact", Source: "trace", StartMS: 45, EndMS: 60},
		}, Unknown: []nativeAttributionUnknown{{Category: "cache", Reason: "not instrumented"}}}
	got, err := ledger.account()
	if err != nil || got.CoveredUnionMS != 50 || got.ResidualMS != 50 || got.OverlapMS != 25 ||
		got.CategoryUnion["tool"] != 40 || got.CategoryUnion["artifact"] != 15 {
		t.Fatalf("incorrect overlapping accounting: %+v, %v", got, err)
	}
	if _, exists := got.CategoryUnion["cache"]; exists {
		t.Fatal("missing observation represented as measured zero")
	}
}

func TestNativeAttributionRejectsInvalidEvidence(t *testing.T) {
	t.Parallel()
	span := nativeAttributionSpan{ID: "a", Category: "tool", Source: "trace", StartMS: 1, EndMS: 5}
	for _, edit := range []func(*nativeAttributionLedger){
		func(l *nativeAttributionLedger) { l.Clock = "" },
		func(l *nativeAttributionLedger) { l.DurationMS = math.NaN() },
		func(l *nativeAttributionLedger) { l.Spans[0].StartMS = -1 },
		func(l *nativeAttributionLedger) { l.Spans[0].EndMS = 11 },
		func(l *nativeAttributionLedger) { l.Spans[0].EndMS = 0 },
		func(l *nativeAttributionLedger) { l.Spans[0].EndMS = math.Inf(1) },
		func(l *nativeAttributionLedger) { l.Spans[0].Parent = "absent" },
		func(l *nativeAttributionLedger) { l.Spans[0].Parent = "a" },
		func(l *nativeAttributionLedger) { l.Spans = append(l.Spans, span) },
		func(l *nativeAttributionLedger) { l.Unknown = []nativeAttributionUnknown{{Category: "cache"}} },
	} {
		ledger := nativeAttributionLedger{Clock: "parent", DurationMS: 10, Spans: []nativeAttributionSpan{span}}
		edit(&ledger)
		if _, err := ledger.account(); err == nil {
			t.Fatalf("accepted invalid evidence: %+v", ledger)
		}
	}
}

func TestNativeGoTraceKeepsClockAndLoadingUnion(t *testing.T) {
	t.Parallel()
	ledger, err := nativeAttributionGoTrace([]byte(`[
		{"name":"Running build command","ph":"B","ts":1000000,"tid":1},
		{"name":"load.PackagesAndErrors","ph":"B","ts":1001000,"tid":1},
		{"name":"load.PackagesAndErrors","ph":"E","ts":1006000,"tid":1},
		{"name":"Running build command","ph":"E","ts":1010000,"tid":1}
	]`))
	if err != nil || ledger.DurationMS != 10 || len(ledger.Spans) != 2 || ledger.Spans[0].Category != "package_loading" ||
		ledger.Spans[0].StartMS != 1 || ledger.Spans[0].EndMS != 6 || ledger.Spans[0].Parent != "" {
		t.Fatalf("trace parsing: %+v, %v", ledger, err)
	}
	accounting, err := ledger.account()
	if err != nil || accounting.ResidualMS != 5 || accounting.CategoryUnion["package_loading"] != 5 {
		t.Fatalf("containers falsely explained driver work: %+v, %v", accounting, err)
	}
}

func TestNativeGoTraceCollapsesConcurrentSameThreadIntervals(t *testing.T) {
	t.Parallel()
	ledger, err := nativeAttributionGoTrace([]byte(`[
		{"name":"a","ph":"B","ts":1000},
		{"name":"b","ph":"B","ts":2000},
		{"name":"a","ph":"B","ts":2100},
		{"name":"a","ph":"E","ts":3000},
		{"name":"b","ph":"E","ts":4000},
		{"name":"a","ph":"E","ts":5000}
	]`))
	if err != nil || len(ledger.Spans) != 2 || ledger.Spans[1].Instances != 2 ||
		ledger.Spans[1].StartMS != 0 || ledger.Spans[1].EndMS != 4 || ledger.Spans[1].Parent != "" {
		t.Fatalf("concurrent trace falsely nested or summed: %+v, %v", ledger, err)
	}
}

func TestNativeGoTraceRejectsIncompleteOrChangedFormat(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		`[]`, `[`, `[{"name":"a","ph":"B","ts":1}]`, `[{"name":"a","ph":"E","ts":1}]`,
		`[{"name":"a","ph":"X","ts":1}]`,
		`[{"name":"a","ph":"B","ts":3},{"name":"a","ph":"E","ts":2}]`,
		`[{"name":"a","ph":"B","ts":1},{"name":"b","ph":"E","ts":2}]`,
	} {
		if _, err := nativeAttributionGoTrace([]byte(data)); err == nil {
			t.Fatalf("accepted incomplete trace: %s", data)
		}
	}
}
