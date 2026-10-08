package main

import (
	"sync"
	"time"

	"scenery.sh/internal/devdash"
)

const (
	dashboardTraceEventBufferTTL = 30 * time.Second
	dashboardTraceEventBufferMax = 4096
)

type dashboardTraceEventBuffer struct {
	mu      sync.Mutex
	events  map[dashboardTraceEventKey][]bufferedDashboardTraceEvent
	total   int
	dropped uint64
	maxAge  time.Duration
	maxSize int
}

type dashboardTraceEventKey struct {
	appID     string
	sessionID string
	traceID   string
	spanID    string
}

type bufferedDashboardTraceEvent struct {
	event   devdash.TraceEvent
	addedAt time.Time
}

func newDashboardTraceEventBuffer() *dashboardTraceEventBuffer {
	return &dashboardTraceEventBuffer{
		events:  make(map[dashboardTraceEventKey][]bufferedDashboardTraceEvent),
		maxAge:  dashboardTraceEventBufferTTL,
		maxSize: dashboardTraceEventBufferMax,
	}
}

func (s *dashboardServer) bufferTraceEvent(event *devdash.TraceEvent) {
	if s == nil || event == nil {
		return
	}
	if s.traces == nil {
		s.traces = newDashboardTraceEventBuffer()
	}
	s.traces.add(event)
}

func (s *dashboardServer) drainBufferedTraceEvents(summary *devdash.TraceSummary) []*devdash.TraceEvent {
	if s == nil || s.traces == nil || summary == nil {
		return nil
	}
	return s.traces.drain(dashboardTraceEventKey{
		appID:     summary.AppID,
		sessionID: summary.SessionID,
		traceID:   summary.TraceID,
		spanID:    summary.SpanID,
	})
}

func (b *dashboardTraceEventBuffer) add(event *devdash.TraceEvent) {
	if b == nil || event == nil {
		return
	}
	now := time.Now().UTC()
	key := dashboardTraceEventKey{
		appID:     event.AppID,
		sessionID: event.SessionID,
		traceID:   event.TraceID,
		spanID:    event.SpanID,
	}
	stored := *event
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	b.events[key] = append(b.events[key], bufferedDashboardTraceEvent{event: stored, addedAt: now})
	b.total++
	b.trimLocked()
}

func (b *dashboardTraceEventBuffer) drain(key dashboardTraceEventKey) []*devdash.TraceEvent {
	if b == nil {
		return nil
	}
	now := time.Now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	items := b.events[key]
	if len(items) == 0 {
		return nil
	}
	delete(b.events, key)
	b.total -= len(items)
	out := make([]*devdash.TraceEvent, 0, len(items))
	for _, item := range items {
		event := item.event
		out = append(out, &event)
	}
	return out
}

func (b *dashboardTraceEventBuffer) pruneLocked(now time.Time) {
	if b.maxAge <= 0 {
		return
	}
	cutoff := now.Add(-b.maxAge)
	for key, items := range b.events {
		kept := items[:0]
		for _, item := range items {
			if !item.addedAt.Before(cutoff) {
				kept = append(kept, item)
			}
		}
		removed := len(items) - len(kept)
		b.total -= removed
		b.dropped += uint64(removed)
		if len(kept) == 0 {
			delete(b.events, key)
			continue
		}
		b.events[key] = kept
	}
}

func (b *dashboardTraceEventBuffer) trimLocked() {
	if b.maxSize <= 0 {
		return
	}
	for b.total > b.maxSize {
		var (
			oldestKey   dashboardTraceEventKey
			oldestIndex int
			oldestTime  time.Time
			found       bool
		)
		for key, items := range b.events {
			for i, item := range items {
				if !found || item.addedAt.Before(oldestTime) {
					oldestKey = key
					oldestIndex = i
					oldestTime = item.addedAt
					found = true
				}
			}
		}
		if !found {
			b.total = 0
			return
		}
		items := b.events[oldestKey]
		items = append(items[:oldestIndex], items[oldestIndex+1:]...)
		if len(items) == 0 {
			delete(b.events, oldestKey)
		} else {
			b.events[oldestKey] = items
		}
		b.total--
		b.dropped++
	}
}

// droppedCount counts buffered events evicted by age or capacity, independently
// of export jobs and report/signal failures. A successful drain is not a loss.
func (b *dashboardTraceEventBuffer) droppedCount() uint64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
