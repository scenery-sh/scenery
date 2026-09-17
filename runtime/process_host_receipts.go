package runtime

import (
	"encoding/json"
	"strings"
	"sync"
)

// A durable receipt outlives the host incarnation that accepted it: a contract
// change replaces the host while the process running the execution stays. Its
// authorization records therefore live in the session's receipt journal (see
// process_host_journal.go), which every host incarnation replays.

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
	err := owners.journal.replay(path, func(line []byte) {
		var record processHostDurableRecord
		if json.Unmarshal(line, &record) == nil && record.Principal != "" && record.ExecutionID != "" {
			owners.apply(record)
		}
	})
	if err == nil && owners.journal.lines > processHostDurableOwnerLimit {
		owners.compact()
	}
	return err
}

func (owners *processHostDurableOwners) store(principal, executionID string, owner processHostDurableOwner) {
	owners.Lock()
	defer owners.Unlock()
	record := processHostDurableRecord{Principal: principal, ExecutionID: executionID, Process: owner.process, Service: owner.service, TaskName: owner.taskName}
	if !owners.apply(record) {
		return
	}
	owners.journal.append(owners.record(principal + "\x00" + executionID))
	if owners.journal.lines >= 2*processHostDurableOwnerLimit {
		owners.compact()
	}
}

// apply adds one record and reports whether it changed the authorized
// receipts; the caller holds the lock.
func (owners *processHostDurableOwners) apply(record processHostDurableRecord) bool {
	key := record.Principal + "\x00" + record.ExecutionID
	owner := processHostDurableOwner{process: record.Process, service: record.Service, taskName: record.TaskName, ambiguous: record.Ambiguous}
	if owners.values == nil {
		owners.values = map[string]processHostDurableOwner{}
	}
	if existing, exists := owners.values[key]; exists {
		if existing.ambiguous || !owner.ambiguous && existing.process == owner.process && existing.service == owner.service && existing.taskName == owner.taskName {
			return false
		}
		existing.ambiguous = true
		owners.values[key] = existing
		return true
	}
	if len(owners.order) >= processHostDurableOwnerLimit {
		delete(owners.values, owners.order[0])
		owners.order = owners.order[1:]
	}
	owners.values[key] = owner
	owners.order = append(owners.order, key)
	return true
}

// record is the journal record of an authorized key; the caller holds the lock.
func (owners *processHostDurableOwners) record(key string) processHostDurableRecord {
	principal, executionID, _ := strings.Cut(key, "\x00")
	owner := owners.values[key]
	return processHostDurableRecord{Principal: principal, ExecutionID: executionID, Process: owner.process, Service: owner.service, TaskName: owner.taskName, Ambiguous: owner.ambiguous}
}

// compact replaces the journal with the authorized receipts; the caller holds
// the lock.
func (owners *processHostDurableOwners) compact() {
	records := make([]any, 0, len(owners.order))
	for _, key := range owners.order {
		records = append(records, owners.record(key))
	}
	owners.journal.rewrite(records)
}

func (owners *processHostDurableOwners) load(principal, executionID string) (processHostDurableOwner, bool) {
	owners.RLock()
	defer owners.RUnlock()
	owner, ok := owners.values[principal+"\x00"+executionID]
	return owner, ok && !owner.ambiguous
}
