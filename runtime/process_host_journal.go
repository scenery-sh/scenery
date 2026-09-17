package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"scenery.sh/errs"
)

// A process host keeps the authority that must outlive its own incarnation,
// durable receipt authorizations and assistant run scopes, in bounded
// JSON-lines journals inside the session's private host state directory. The
// journal is the commit point of that authority: a change is appended before
// the host acts on it, and a host that cannot append a change does not make
// it. A host replays the journals when it starts and replaces a journal with
// its bounded records when it grows.
//
// A journal that can no longer prove its records is poisoned: a failed append
// or rewrite, a record that does not decode before the journal's last newline,
// or an oversized journal. A poisoned journal authorizes nothing. The host
// leaves a marker beside it and removes it, reports the host state as
// unavailable in its generation status, and logs a warning. The supervisor
// starts every later host incarnation of the session over a new, empty host
// state directory (see devProcessModel.hostStateEpoch); because a missing
// record never authorizes anything, that epoch is fail-closed even when the
// marker and the removal both failed.
//
// Bytes after the journal's last newline are a torn record and are discarded:
// an append writes a record and its newline in one write, and an append whose
// write fails poisons the journal, so a record without its newline was never
// committed and no authority was acted on for it.

const (
	processHostReceiptsJournal = "durable-receipts.jsonl"
	processHostRunsJournal     = "assistant-runs.jsonl"
	processHostPoisonedSuffix  = ".poisoned"
	// processHostJournalMaxBytes bounds a replayed journal; compaction keeps
	// every journal far below it.
	processHostJournalMaxBytes = 64 << 20
)

// errProcessHostStateUnavailable is the failure of an operation that needs
// host state a poisoned journal can no longer prove.
var errProcessHostStateUnavailable = errors.New("process host state is unavailable")

// stateAvailable reports whether the host's authority journals can still prove
// their records.
func (h *processHost) stateAvailable() bool {
	h.owners.RLock()
	receipts := h.owners.journal.poisoned == nil
	h.owners.RUnlock()
	h.conversations.Lock()
	runs := h.conversations.journal.poisoned == nil
	h.conversations.Unlock()
	return receipts && runs
}

// openState replays the host state a previous incarnation of the session left
// in directory and journals later changes there.
func (h *processHost) openState(directory string) error {
	if err := h.owners.open(filepath.Join(directory, processHostReceiptsJournal)); err != nil {
		return err
	}
	return h.conversations.open(filepath.Join(directory, processHostRunsJournal))
}

type processHostJournal struct {
	path string
	// lines counts the committed records of the journal.
	lines int
	// poisoned is the reason the journal can no longer prove its records.
	poisoned error
}

// unavailable is the failure of an operation on a poisoned journal.
func (journal *processHostJournal) unavailable() error {
	return &errs.Error{Code: errs.Unavailable, Message: errProcessHostStateUnavailable.Error(), Meta: errs.Metadata{"delivery": "not_sent"}, Cause: errors.Join(errProcessHostStateUnavailable, journal.poisoned)}
}

// replay calls apply for every committed record of the journal at path, which
// may not exist yet. A record apply rejects poisons the journal.
func (journal *processHostJournal) replay(path string, apply func([]byte) error) error {
	journal.path, journal.lines, journal.poisoned = path, 0, nil
	if _, err := os.Lstat(path + processHostPoisonedSuffix); err == nil {
		journal.poisoned = errors.New("an earlier host incarnation poisoned the journal")
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("runtime: inspect process host journal: %w", err)
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("runtime: open process host journal: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, processHostJournalMaxBytes+1))
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("runtime: read process host journal: %w", err)
	}
	if len(data) > processHostJournalMaxBytes {
		journal.poison(errors.New("journal exceeds its bound"))
		return nil
	}
	complete := data
	torn := false
	if end := bytes.LastIndexByte(data, '\n'); end+1 < len(data) {
		complete, torn = data[:end+1], true
	}
	for line := range bytes.Lines(complete) {
		if err := apply(bytes.TrimSuffix(line, []byte{'\n'})); err != nil {
			journal.poison(fmt.Errorf("record %d: %w", journal.lines+1, err))
			return nil
		}
		journal.lines++
	}
	if torn {
		// A later append must not extend the torn record.
		if err := writeProcessHostJournal(path, complete); err != nil {
			journal.poison(err)
		}
	}
	return nil
}

// append commits one record.
func (journal *processHostJournal) append(record any) error {
	if journal.poisoned != nil {
		return journal.unavailable()
	}
	if journal.path == "" {
		return nil
	}
	line, err := json.Marshal(record)
	if err == nil {
		var file *os.File
		file, err = os.OpenFile(journal.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err == nil {
			_, err = file.Write(append(line, '\n'))
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		journal.poison(fmt.Errorf("append: %w", err))
		return journal.unavailable()
	}
	journal.lines++
	return nil
}

// rewrite atomically replaces the journal with records.
func (journal *processHostJournal) rewrite(records []any) error {
	if journal.poisoned != nil {
		return journal.unavailable()
	}
	if journal.path == "" {
		return nil
	}
	var body bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			journal.poison(fmt.Errorf("compaction: %w", err))
			return journal.unavailable()
		}
		body.Write(append(line, '\n'))
	}
	if err := writeProcessHostJournal(journal.path, body.Bytes()); err != nil {
		journal.poison(fmt.Errorf("compaction: %w", err))
		return journal.unavailable()
	}
	journal.lines = len(records)
	return nil
}

// poison records that the journal can no longer prove its records.
func (journal *processHostJournal) poison(cause error) {
	journal.poisoned = cause
	slog.Warn("process host state is unavailable: its journal can no longer prove its records", "journal", filepath.Base(journal.path), "error", cause.Error())
	if journal.path == "" {
		return
	}
	// Either the marker or the removal keeps a later incarnation from
	// replaying a prefix; a missing record never authorizes anything.
	if marker, err := os.OpenFile(journal.path+processHostPoisonedSuffix, os.O_WRONLY|os.O_CREATE, 0o600); err == nil {
		_ = marker.Close()
	}
	_ = os.Remove(journal.path)
}

func writeProcessHostJournal(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	_, err = temporary.Write(data)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
