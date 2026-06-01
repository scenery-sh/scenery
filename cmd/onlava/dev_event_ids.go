package main

import (
	"sync/atomic"
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
)

var devEventIDSequence atomic.Int64

func assignDevEventID(event devdash.DevEvent) devdash.DevEvent {
	if event.ID > 0 {
		devEventIDSequence.CompareAndSwap(0, event.ID+1)
		return event
	}
	for {
		now := time.Now().UnixNano()
		current := devEventIDSequence.Load()
		next := current + 1
		if now > next {
			next = now
		}
		if devEventIDSequence.CompareAndSwap(current, next) {
			event.ID = next
			return event
		}
	}
}
