package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// A process host keeps the state that must outlive its own incarnation, such as
// durable receipt authorizations and assistant run scopes, in bounded JSON-lines
// journals inside the session's private host state directory. A host replays a
// journal when it starts, appends each record it adds, and replaces the journal
// with its bounded records when it grows. A torn or malformed record, such as
// the last line of an interrupted append, is skipped by its reader. A journal
// the host cannot write loses only the records it could not append, which then
// fail closed after the next host replacement.

const (
	processHostReceiptsJournal = "durable-receipts.jsonl"
	processHostRunsJournal     = "assistant-runs.jsonl"
	// processHostJournalMaxLine bounds one replayed record.
	processHostJournalMaxLine = 1 << 20
)

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
	// lines counts the records the journal holds.
	lines int
}

// replay calls apply for every record of the journal at path, which may not
// exist yet.
func (journal *processHostJournal) replay(path string, apply func([]byte)) error {
	journal.path, journal.lines = path, 0
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("runtime: open process host journal: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), processHostJournalMaxLine)
	for scanner.Scan() {
		journal.lines++
		apply(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("runtime: read process host journal: %w", err)
	}
	return nil
}

// append adds one record; a host without a journal records nothing.
func (journal *processHostJournal) append(record any) {
	if journal.path == "" {
		return
	}
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	file, err := os.OpenFile(journal.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err == nil {
		_, err = file.Write(append(line, '\n'))
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		logTrace(context.Background(), fmt.Sprintf("process host journal %s append failed: %v", filepath.Base(journal.path), err))
		return
	}
	journal.lines++
}

// rewrite atomically replaces the journal with records.
func (journal *processHostJournal) rewrite(records []any) {
	if journal.path == "" {
		return
	}
	var body bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return
		}
		body.Write(append(line, '\n'))
	}
	temporary, err := os.CreateTemp(filepath.Dir(journal.path), ".journal-*")
	if err == nil {
		defer func() { _ = os.Remove(temporary.Name()) }()
		_, err = temporary.Write(body.Bytes())
		if closeErr := temporary.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(temporary.Name(), journal.path)
		}
	}
	if err != nil {
		logTrace(context.Background(), fmt.Sprintf("process host journal %s compaction failed: %v", filepath.Base(journal.path), err))
		return
	}
	journal.lines = len(records)
}
