package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestNotificationSubscriptionsReleaseOnlyTheirObservation(t *testing.T) {
	w := newWatcher()
	first, releaseFirst := w.subscribe("job:a")
	second, releaseSecond := w.subscribe("job:a")
	if first != second {
		t.Fatal("concurrent waiters do not share the observation")
	}
	releaseFirst()
	releaseFirst()
	if len(w.wakes) != 1 {
		t.Fatal("one waiter released another waiter's observation")
	}
	w.signal("job:a")
	select {
	case <-second:
	default:
		t.Fatal("notification did not wake the remaining waiter")
	}
	next, releaseNext := w.subscribe("job:a")
	releaseSecond()
	if len(w.wakes) != 1 || next == second {
		t.Fatal("old release removed the new observation")
	}
	releaseNext()
	for index := range 1000 {
		_, release := w.subscribe(fmt.Sprintf("job:%d", index))
		release()
	}
	if len(w.wakes) != 0 {
		t.Fatalf("abandoned observations retained: %d", len(w.wakes))
	}
}

func TestNotificationWatcherCloseReleasesWaiters(t *testing.T) {
	w := newWatcher()
	wake, release := w.subscribe("job:a")
	w.close()
	release()
	late, releaseLate := w.subscribe("job:b")
	releaseLate()
	for _, channel := range []<-chan struct{}{wake, late} {
		select {
		case <-channel:
		default:
			t.Fatal("closed watcher left a waiter blocked")
		}
	}
	if len(w.wakes) != 0 {
		t.Fatal("closed watcher retained observations")
	}
}

func TestNotificationHubSharesListenerAndDispatchesByService(t *testing.T) {
	observed := make(chan []string, 16)
	hub := newNotificationHub(func(ctx context.Context, channels []string, dispatch func(string, string)) error {
		observed <- channels
		dispatch("service-b", `{"job":"done"}`)
		<-ctx.Done()
		return ctx.Err()
	})
	t.Cleanup(hub.close)
	a, b := newWatcher(), newWatcher()
	aWake, releaseA := a.subscribe(jobWakeKey("done"))
	defer releaseA()
	bWake, releaseB := b.subscribe(jobWakeKey("done"))
	defer releaseB()
	hub.add("service-a", a)
	hub.add("service-b", b)
	awaitChannels := func(expected []string) {
		t.Helper()
		deadline := time.NewTimer(50 * time.Millisecond)
		defer deadline.Stop()
		for {
			select {
			case actual := <-observed:
				if slices.Equal(actual, expected) {
					return
				}
			case <-deadline.C:
				t.Fatalf("listener did not observe channels %v", expected)
			}
		}
	}
	awaitChannels([]string{"service-a", "service-b"})
	select {
	case <-bWake:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("service notification was not dispatched")
	}
	select {
	case <-aWake:
		t.Fatal("notification crossed service boundaries")
	default:
	}
	hub.remove("service-a", a)
	awaitChannels([]string{"service-b"})
	// A second view of an already-listened service does not need a new connection.
	other := newWatcher()
	hub.add("service-b", other)
	otherWake, releaseOther := other.subscribe(jobWakeKey("done"))
	defer releaseOther()
	hub.dispatch("service-b", `{"job":"done"}`)
	select {
	case <-otherWake:
	default:
		t.Fatal("second service view did not receive the shared notification")
	}
}
