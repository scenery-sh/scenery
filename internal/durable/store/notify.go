package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
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

// watcher wakes a store's active subscribers. Subscribe before checking the
// database and release after checking or waiting; notifications close the
// current channel while later subscriptions belong to a new observation.
type watcher struct {
	mu     sync.Mutex
	wakes  map[string]*subscription
	closed bool
}

type subscription struct {
	wake chan struct{}
	refs int
}

func newWatcher() *watcher {
	return &watcher{wakes: make(map[string]*subscription)}
}

func (w *watcher) subscribe(key string) (<-chan struct{}, func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		wake := make(chan struct{})
		close(wake)
		return wake, func() {}
	}
	entry := w.wakes[key]
	if entry == nil {
		entry = &subscription{wake: make(chan struct{})}
		w.wakes[key] = entry
	}
	entry.refs++
	var once sync.Once
	return entry.wake, func() {
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.wakes[key] == entry {
				entry.refs--
				if entry.refs == 0 {
					delete(w.wakes, key)
				}
			}
		})
	}
}

func (w *watcher) signal(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if entry, ok := w.wakes[key]; ok {
		close(entry.wake)
		delete(w.wakes, key)
	}
}

func (w *watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	for key, entry := range w.wakes {
		close(entry.wake)
		delete(w.wakes, key)
	}
}

func (w *watcher) dispatch(payload string) {
	var message notification
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return
	}
	if message.Kind == "ready" {
		w.mu.Lock()
		defer w.mu.Unlock()
		for key, entry := range w.wakes {
			close(entry.wake)
			delete(w.wakes, key)
		}
		return
	}
	if message.Task != "" {
		w.signal(taskWakeKey(message.Task))
	}
	if message.Job != "" {
		w.signal(jobWakeKey(message.Job))
	}
}

// listenNotifications owns a dedicated pgx connection, outside the query pool.
// One base store family listens on every registered service's channel.
func listenNotifications(ctx context.Context, databaseURL string, channels []string, dispatch func(string, string)) error {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return err
	}
	config.RuntimeParams["application_name"] = "scenery durable notifications"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	for _, channel := range channels {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return err
		}
	}
	// Recheck observers after connection setup/reconnect: the queries they
	// checked before LISTEN became effective may have missed a transition.
	for _, channel := range channels {
		dispatch(channel, `{"kind":"ready"}`)
	}
	for {
		message, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		dispatch(message.Channel, message.Payload)
	}
}

// startWatch opens the store's LISTEN connection once, on first use, so a
// store that only starts jobs or lists them never listens.
func (s *Store) startWatch() *watcher {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		s.watch = newWatcher()
		if s.closed {
			s.watch.close()
		} else {
			s.notifications.add(notificationChannel(s.Service), s.watch)
		}
	}
	return s.watch
}

func (s *Store) stopWatch() {
	if s == nil {
		return
	}
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.closed = true
	if s.watch != nil {
		s.notifications.remove(notificationChannel(s.Service), s.watch)
		s.watch.close()
	}
}

// TaskWake is closed when a job of the task is queued or finishes. Take it
// before leasing, so a job queued in between still wakes the loop. Call the
// returned release function after the lease check or notification wait.
func (s *Store) TaskWake(taskName string) (<-chan struct{}, func()) {
	return s.startWatch().subscribe(taskWakeKey(taskName))
}

// JobWake is closed when the job reaches a final state or is requeued. Take
// it before reading the job's state, then release after the check or wait.
func (s *Store) JobWake(jobID string) (<-chan struct{}, func()) {
	return s.startWatch().subscribe(jobWakeKey(jobID))
}
