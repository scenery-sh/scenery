package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Every transition that lets a task loop or a waiting caller make progress
// runs pg_notify inside its transaction, so Postgres delivers the message on
// commit. One channel per service carries JSON payloads: a queued job names
// its task, a finished job names itself and its task.

// maxIdentifierLength is Postgres NAMEDATALEN - 1.
const maxIdentifierLength = 63

const notificationPrefix = "scenery_durable_"

type notification struct {
	Kind string `json:"kind"`
	Task string `json:"task,omitempty"`
	Job  string `json:"job,omitempty"`
}

// notificationChannel names the service's channel. Long service names are
// hashed so the identifier never exceeds what Postgres keeps.
func notificationChannel(service string) string {
	channel := notificationPrefix + service
	if len(channel) <= maxIdentifierLength {
		return channel
	}
	sum := sha256.Sum256([]byte(service))
	return notificationPrefix + hex.EncodeToString(sum[:])[:32]
}

func taskWakeKey(task string) string { return "task:" + task }

func jobWakeKey(job string) string { return "job:" + job }

func notifyTx(ctx context.Context, tx *sql.Tx, service string, message notification) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("durable store: encode notification: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, notificationChannel(service), string(payload)); err != nil {
		return fmt.Errorf("durable store: notify %s: %w", message.Kind, err)
	}
	return nil
}

// watcher holds one LISTEN connection for a store and wakes subscribers by
// key. A subscription is a channel closed by the next matching notification;
// subscribe before checking the database, so no notification is missed.
type watcher struct {
	mu     sync.Mutex
	wakes  map[string]chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func newWatcher() *watcher {
	return &watcher{wakes: make(map[string]chan struct{})}
}

func (w *watcher) subscribe(key string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, ok := w.wakes[key]
	if !ok {
		ch = make(chan struct{})
		w.wakes[key] = ch
	}
	return ch
}

func (w *watcher) signal(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ch, ok := w.wakes[key]; ok {
		close(ch)
		delete(w.wakes, key)
	}
}

func (w *watcher) dispatch(payload string) {
	var message notification
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return
	}
	if message.Task != "" {
		w.signal(taskWakeKey(message.Task))
	}
	if message.Job != "" {
		w.signal(jobWakeKey(message.Job))
	}
}

// listen keeps one connection listening on channel until ctx ends, reconnecting
// after a failure. Waiters are never stuck on a broken connection: they also
// wake on their own fallback timers.
func (w *watcher) listen(ctx context.Context, db *sql.DB, channel string) {
	defer close(w.done)
	for {
		err := listenOnce(ctx, db, channel, w.dispatch)
		if ctx.Err() != nil {
			return
		}
		_ = err
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func listenOnce(ctx context.Context, db *sql.DB, channel string, dispatch func(string)) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(driverConn any) error {
		raw, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return errors.New("durable store: notifications need the pgx driver")
		}
		pgConn := raw.Conn()
		if _, err := pgConn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return err
		}
		for {
			message, err := pgConn.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			dispatch(message.Payload)
		}
	})
}

// startWatch opens the store's LISTEN connection once, on first use, so a
// store that only starts jobs or lists them never listens.
func (s *Store) startWatch() *watcher {
	s.watchOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		w := newWatcher()
		w.cancel = cancel
		w.done = make(chan struct{})
		s.watch = w
		go w.listen(ctx, s.db, notificationChannel(s.Service))
	})
	return s.watch
}

func (s *Store) stopWatch() {
	if s == nil || s.watch == nil {
		return
	}
	s.watch.cancel()
	select {
	case <-s.watch.done:
	case <-time.After(5 * time.Second):
	}
}

// TaskWake is closed when a job of the task is queued or finishes. Take it
// before leasing, so a job queued in between still wakes the loop.
func (s *Store) TaskWake(taskName string) <-chan struct{} {
	return s.startWatch().subscribe(taskWakeKey(taskName))
}

// JobWake is closed when the job reaches a final state or is requeued. Take
// it before reading the job's state.
func (s *Store) JobWake(jobID string) <-chan struct{} {
	return s.startWatch().subscribe(jobWakeKey(jobID))
}
