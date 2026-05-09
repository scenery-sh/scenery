package objectstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

type LiveRouter struct {
	mu     sync.RWMutex
	nextID int64
	subs   map[int64]*liveSubscription
}

type liveSubscription struct {
	id       int64
	request  SubscriptionRequest
	tenantID string
	actor    Actor
	ch       chan *Event
}

func newLiveRouter() *LiveRouter {
	return &LiveRouter{subs: make(map[int64]*liveSubscription)}
}

func (r *LiveRouter) subscribe(sub *liveSubscription) func() {
	r.mu.Lock()
	r.nextID++
	sub.id = r.nextID
	if sub.ch == nil {
		sub.ch = make(chan *Event, 32)
	}
	r.subs[sub.id] = sub
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if existing := r.subs[sub.id]; existing != nil {
			delete(r.subs, sub.id)
			close(existing.ch)
		}
		r.mu.Unlock()
	}
}

func (r *LiveRouter) publish(event *Event) {
	if event == nil {
		return
	}
	r.mu.RLock()
	subs := make([]*liveSubscription, 0, len(r.subs))
	for _, sub := range r.subs {
		subs = append(subs, sub)
	}
	r.mu.RUnlock()
	for _, sub := range subs {
		deliver := eventForSubscription(event, sub)
		if deliver == nil {
			continue
		}
		select {
		case sub.ch <- deliver:
		default:
		}
	}
}

func (s *Store) resolveSubscription(ctx context.Context, actor Actor, req SubscriptionRequest) (*liveSubscription, error) {
	if req.QueryID == "" {
		return nil, fmt.Errorf("subscription query_id is required")
	}
	if req.Object == "" {
		return nil, fmt.Errorf("subscription object is required")
	}
	state, err := s.loadState(ctx, req.TenantKey, req.Object)
	if err != nil {
		return nil, err
	}
	if err := s.perms.CanReadObject(ctx, actor, objectRef(state)); err != nil {
		return nil, err
	}
	permissionFilter, err := s.perms.RowFilter(ctx, actor, objectRef(state))
	if err != nil {
		return nil, err
	}
	req.Filter = andFilters(req.Filter, permissionFilter)
	for _, fieldName := range req.SelectedFields {
		field, ok := state.Fields[fieldName]
		if !ok {
			return nil, fmt.Errorf("subscription field %q does not exist on object %s", fieldName, req.Object)
		}
		if err := s.perms.CanReadField(ctx, actor, fieldRef(state, field)); err != nil {
			return nil, err
		}
	}
	if req.Filter != nil {
		if _, err := compileFilter(state, req.Filter, &[]any{state.Tenant.ID}); err != nil {
			return nil, err
		}
	}
	return &liveSubscription{
		request:  req,
		tenantID: state.Tenant.ID,
		actor:    actor,
		ch:       make(chan *Event, 32),
	}, nil
}

func eventForSubscription(event *Event, sub *liveSubscription) *Event {
	if event == nil || sub == nil {
		return nil
	}
	if event.TenantID != sub.tenantID || event.Object != sub.request.Object {
		return nil
	}
	if sub.request.Filter != nil && !eventMatchesFilter(event, sub.request.Filter) {
		return nil
	}
	deliver := cloneEvent(event)
	deliver.QueryIDs = []string{sub.request.QueryID}
	stripEventFields(deliver, sub.request.SelectedFields)
	return deliver
}

func eventMatchesFilter(event *Event, filter *Filter) bool {
	switch event.Action {
	case "created":
		return evalFilter(filter, event.After)
	case "updated":
		return evalFilter(filter, event.After) || evalFilter(filter, event.Before)
	case "deleted":
		return evalFilter(filter, event.Before)
	default:
		return true
	}
}

func evalFilter(filter *Filter, record Record) bool {
	if filter == nil {
		return true
	}
	op := filter.Op
	switch op {
	case "", "and":
		for i := range filter.Filters {
			if !evalFilter(&filter.Filters[i], record) {
				return false
			}
		}
		return true
	case "or":
		for i := range filter.Filters {
			if evalFilter(&filter.Filters[i], record) {
				return true
			}
		}
		return false
	case "not":
		return len(filter.Filters) == 1 && !evalFilter(&filter.Filters[0], record)
	case "eq":
		return fmt.Sprint(record[filter.Field]) == fmt.Sprint(filter.Value)
	case "neq":
		return fmt.Sprint(record[filter.Field]) != fmt.Sprint(filter.Value)
	case "is_null":
		isNull := record[filter.Field] == nil
		if boolValue(filter.Value, true) {
			return isNull
		}
		return !isNull
	case "contains":
		return containsString(fmt.Sprint(record[filter.Field]), fmt.Sprint(filter.Value))
	case "in":
		value := fmt.Sprint(record[filter.Field])
		for _, candidate := range filter.Values {
			if value == fmt.Sprint(candidate) {
				return true
			}
		}
		return false
	case "gt", "gte", "lt", "lte":
		return compareNumber(record[filter.Field], filter.Value, op)
	default:
		return false
	}
}

func cloneEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	clone := *event
	clone.ChangedFields = append([]string(nil), event.ChangedFields...)
	clone.QueryIDs = append([]string(nil), event.QueryIDs...)
	clone.Before = cloneRecord(event.Before)
	clone.After = cloneRecord(event.After)
	clone.Diff = cloneRecord(event.Diff)
	return &clone
}

func stripEventFields(event *Event, selected []string) {
	if len(selected) == 0 {
		return
	}
	keep := map[string]bool{
		"id":         true,
		"created_at": true,
		"updated_at": true,
	}
	for _, field := range selected {
		keep[field] = true
	}
	stripRecord := func(record Record) {
		for key := range record {
			if !keep[key] {
				delete(record, key)
			}
		}
	}
	stripRecord(event.Before)
	stripRecord(event.After)
	stripRecord(event.Diff)
	filtered := event.ChangedFields[:0]
	for _, field := range event.ChangedFields {
		if keep[field] {
			filtered = append(filtered, field)
		}
	}
	event.ChangedFields = filtered
}

func containsString(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && containsFold(haystack, needle))
}

func containsFold(haystack, needle string) bool {
	haystack = strings.ToLower(haystack)
	needle = strings.ToLower(needle)
	return strings.Contains(haystack, needle)
}

func compareNumber(left, right any, op string) bool {
	l, lok := numberValue(left)
	r, rok := numberValue(right)
	if !lok || !rok {
		return false
	}
	switch op {
	case "gt":
		return l > r
	case "gte":
		return l >= r
	case "lt":
		return l < r
	case "lte":
		return l <= r
	default:
		return false
	}
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		return n, err == nil
	}
}
