package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// A durable receipt outlives the host incarnation that accepted it: a contract
// change replaces the host while the process running the execution stays. Its
// authorization is committed to the session's receipt journal (see
// process_host_journal.go) before the host authorizes it, which every host
// incarnation replays. An ambiguity, which revokes an authorization, is
// committed the same way; a host that cannot commit it poisons the journal and
// authorizes no receipt, so recovery cannot restore the revoked authorization.

// processHostDurableOwnerLimit bounds the receipts a session authorizes; the
// oldest receipt is forgotten first and then reads as not found.
const processHostDurableOwnerLimit = 4096

type processHostDurableOwners struct {
	sync.RWMutex
	values  map[string]processHostDurableOwner
	order   []string
	journal processHostJournal
}

type processHostDurableOwner struct {
	process  string
	service  string
	taskName string
	// ambiguous marks an execution ID accepted by two owners for one principal;
	// such a receipt fails closed as it does in one application process.
	ambiguous bool
}

type processHostDurableRecord struct {
	Principal   string `json:"principal"`
	ExecutionID string `json:"execution_id"`
	Process     string `json:"process"`
	Service     string `json:"service"`
	TaskName    string `json:"task_name"`
	Ambiguous   bool   `json:"ambiguous,omitempty"`
}

// open replays the session's receipt journal and journals later receipts.
func (owners *processHostDurableOwners) open(path string) error {
	owners.Lock()
	defer owners.Unlock()
	owners.values, owners.order = nil, nil
	err := owners.journal.replay(path, func(line []byte) error {
		var record processHostDurableRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if record.Principal == "" || record.ExecutionID == "" {
			return errors.New("receipt record names no principal or execution")
		}
		owners.apply(record)
		return nil
	})
	if err == nil && owners.journal.lines > processHostDurableOwnerLimit {
		owners.compact()
	}
	return err
}

// store commits and authorizes the owner of an accepted receipt. Its failure
// means the receipt is not authorized; the durable execution was accepted
// regardless.
func (owners *processHostDurableOwners) store(principal, executionID string, owner processHostDurableOwner) error {
	owners.Lock()
	defer owners.Unlock()
	record := processHostDurableRecord{Principal: principal, ExecutionID: executionID, Process: owner.process, Service: owner.service, TaskName: owner.taskName}
	next, changed := owners.next(record)
	if !changed {
		return nil
	}
	if err := owners.journal.append(next); err != nil {
		return err
	}
	owners.apply(next)
	if owners.journal.lines >= 2*processHostDurableOwnerLimit {
		owners.compact()
	}
	return nil
}

// next is the record a new owner commits: the owner itself, or the ambiguity it
// creates; changed reports whether it changes the authorized receipts. The
// caller holds the lock.
func (owners *processHostDurableOwners) next(record processHostDurableRecord) (processHostDurableRecord, bool) {
	existing, exists := owners.values[record.Principal+"\x00"+record.ExecutionID]
	switch {
	case !exists:
		return record, true
	case existing.ambiguous || existing.process == record.Process && existing.service == record.Service && existing.taskName == record.TaskName:
		return record, false
	}
	record.Process, record.Service, record.TaskName, record.Ambiguous = existing.process, existing.service, existing.taskName, true
	return record, true
}

// apply adds one committed record; the caller holds the lock.
func (owners *processHostDurableOwners) apply(record processHostDurableRecord) {
	key := record.Principal + "\x00" + record.ExecutionID
	if owners.values == nil {
		owners.values = map[string]processHostDurableOwner{}
	}
	if existing, exists := owners.values[key]; exists {
		if record.Ambiguous || existing.process != record.Process || existing.service != record.Service || existing.taskName != record.TaskName {
			existing.ambiguous = true
			owners.values[key] = existing
		}
		return
	}
	if len(owners.order) >= processHostDurableOwnerLimit {
		delete(owners.values, owners.order[0])
		owners.order = owners.order[1:]
	}
	owners.values[key] = processHostDurableOwner{process: record.Process, service: record.Service, taskName: record.TaskName, ambiguous: record.Ambiguous}
	owners.order = append(owners.order, key)
}

// compact replaces the journal with the authorized receipts; the caller holds
// the lock. A failed compaction poisons the journal.
func (owners *processHostDurableOwners) compact() {
	records := make([]any, 0, len(owners.order))
	for _, key := range owners.order {
		principal, executionID, _ := strings.Cut(key, "\x00")
		owner := owners.values[key]
		records = append(records, processHostDurableRecord{Principal: principal, ExecutionID: executionID, Process: owner.process, Service: owner.service, TaskName: owner.taskName, Ambiguous: owner.ambiguous})
	}
	_ = owners.journal.rewrite(records)
}

// load returns the authorized owner of a receipt. A receipt that is unknown or
// ambiguous is not found; a poisoned journal authorizes nothing.
func (owners *processHostDurableOwners) load(principal, executionID string) (processHostDurableOwner, bool, error) {
	owners.RLock()
	defer owners.RUnlock()
	if owners.journal.poisoned != nil {
		return processHostDurableOwner{}, false, owners.journal.unavailable()
	}
	owner, ok := owners.values[principal+"\x00"+executionID]
	return owner, ok && !owner.ambiguous, nil
}
