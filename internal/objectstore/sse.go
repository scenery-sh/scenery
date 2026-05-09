package objectstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	sseHeartbeatInterval  = 25 * time.Second
	sseOutboxPollInterval = time.Second
)

func (s *Store) ServeEvents(ctx context.Context, actor Actor, w http.ResponseWriter, req *http.Request) error {
	subRequests, afterSeq, err := parseSubscriptionRequests(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return err
	}
	if len(subRequests) == 0 {
		err := fmt.Errorf("at least one subscription is required")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return err
	}

	var subs []*liveSubscription
	tenantIDs := map[string]bool{}
	for _, subReq := range subRequests {
		sub, err := s.resolveSubscription(ctx, actor, subReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return err
		}
		subs = append(subs, sub)
		tenantIDs[sub.tenantID] = true
		if subReq.AfterSeq > afterSeq {
			afterSeq = subReq.AfterSeq
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)

	lastSeq := afterSeq
	write := func(event *Event) error {
		if event == nil {
			return nil
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: data\ndata: %s\n\n", event.Seq, data); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
		return nil
	}

	unsubs := make([]func(), 0, len(subs))
	merged := make(chan *Event, 32)
	for _, sub := range subs {
		unsub := s.router.subscribe(sub)
		unsubs = append(unsubs, unsub)
		go func(ch <-chan *Event) {
			for event := range ch {
				select {
				case merged <- event:
				case <-req.Context().Done():
					return
				}
			}
		}(sub.ch)
	}
	defer func() {
		for _, unsub := range unsubs {
			unsub()
		}
	}()

	replay, err := s.eventsAfter(ctx, afterSeq, tenantIDs, 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	for _, event := range replay {
		for _, sub := range subs {
			if deliver := eventForSubscription(event, sub); deliver != nil {
				if err := write(deliver); err != nil {
					return err
				}
			}
		}
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
	}
	if _, err := fmt.Fprint(w, "event: ready\ndata: {}\n\n"); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()
	pollTicker := time.NewTicker(sseOutboxPollInterval)
	defer pollTicker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return nil
		case event := <-merged:
			if err := write(event); err != nil {
				return err
			}
		case <-pollTicker.C:
			events, err := s.eventsAfter(ctx, lastSeq, tenantIDs, 1000)
			if err != nil {
				return err
			}
			for _, event := range events {
				for _, sub := range subs {
					if deliver := eventForSubscription(event, sub); deliver != nil {
						if err := write(deliver); err != nil {
							return err
						}
					}
				}
				if event.Seq > lastSeq {
					lastSeq = event.Seq
				}
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func parseSubscriptionRequests(req *http.Request) ([]SubscriptionRequest, int64, error) {
	query := req.URL.Query()
	afterSeq := parseSeq(firstNonEmpty(query.Get("after_seq"), req.Header.Get("Last-Event-ID")))
	if raw := strings.TrimSpace(query.Get("subscriptions")); raw != "" {
		var subs []SubscriptionRequest
		if err := json.Unmarshal([]byte(raw), &subs); err != nil {
			return nil, 0, fmt.Errorf("decode subscriptions: %w", err)
		}
		return subs, afterSeq, nil
	}
	sub := SubscriptionRequest{
		QueryID:        firstNonEmpty(query.Get("query_id"), "default"),
		TenantKey:      query.Get("tenant_key"),
		Object:         query.Get("object"),
		SelectedFields: splitCSV(query.Get("fields")),
		AfterSeq:       afterSeq,
	}
	if rawFilter := strings.TrimSpace(query.Get("filter")); rawFilter != "" {
		var filter Filter
		if err := json.Unmarshal([]byte(rawFilter), &filter); err != nil {
			return nil, 0, fmt.Errorf("decode filter: %w", err)
		}
		sub.Filter = &filter
	}
	return []SubscriptionRequest{sub}, afterSeq, nil
}

func parseSeq(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, _ := strconv.ParseInt(raw, 10, 64)
	return value
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
