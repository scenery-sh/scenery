package store

import (
	"context"
	"sort"
	"sync"
	"time"
)

// notificationHub shares one listener among the service views of a base store.
// Channel membership changes cancel the observation and reconnect immediately;
// connection failures retry after a second. Callers retain their fallback poll
// because notification delivery itself is not durable.
type notificationHub struct {
	mu        sync.Mutex
	watchers  map[string]map[*watcher]struct{}
	revision  uint64
	interrupt context.CancelFunc
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	changed   chan struct{}
	done      chan struct{}
	listen    func(context.Context, []string, func(string, string)) error
}

func newNotificationHub(listen func(context.Context, []string, func(string, string)) error) *notificationHub {
	ctx, cancel := context.WithCancel(context.Background())
	hub := &notificationHub{
		watchers: make(map[string]map[*watcher]struct{}), ctx: ctx, cancel: cancel,
		changed: make(chan struct{}, 1), done: make(chan struct{}), listen: listen,
	}
	go hub.run()
	return hub
}

func (h *notificationHub) add(channel string, w *watcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		w.close()
		return
	}
	group := h.watchers[channel]
	if group == nil {
		group = make(map[*watcher]struct{})
		h.watchers[channel] = group
		group[w] = struct{}{}
		h.membershipChangedLocked()
		return
	}
	group[w] = struct{}{}
}

func (h *notificationHub) remove(channel string, w *watcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	group := h.watchers[channel]
	delete(group, w)
	if len(group) == 0 {
		delete(h.watchers, channel)
		h.membershipChangedLocked()
	}
}

func (h *notificationHub) membershipChangedLocked() {
	h.revision++
	if h.interrupt != nil {
		h.interrupt()
	}
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func (h *notificationHub) dispatch(channel, payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.watchers[channel] {
		w.dispatch(payload)
	}
}

func (h *notificationHub) run() {
	defer close(h.done)
	for h.ctx.Err() == nil {
		h.mu.Lock()
		channels := make([]string, 0, len(h.watchers))
		for channel := range h.watchers {
			channels = append(channels, channel)
		}
		revision := h.revision
		ctx, cancel := context.WithCancel(h.ctx)
		h.interrupt = cancel
		h.mu.Unlock()
		sort.Strings(channels)
		if len(channels) == 0 {
			select {
			case <-ctx.Done():
			case <-h.changed:
			}
		} else {
			_ = h.listen(ctx, channels, h.dispatch)
		}
		cancel()
		h.mu.Lock()
		h.interrupt = nil
		changed := h.revision != revision
		h.mu.Unlock()
		if len(channels) == 0 || changed {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-h.ctx.Done():
		case <-h.changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (h *notificationHub) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	for _, group := range h.watchers {
		for w := range group {
			w.close()
		}
	}
	clear(h.watchers)
	h.cancel()
	h.mu.Unlock()
	<-h.done
}
