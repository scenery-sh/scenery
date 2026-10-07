package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/machine"
)

// Build block reasons. A blocked build failed for a cause that no ordinary
// source edit can resolve; building again repeats the same failure until the
// dependency the cause names changes.
const (
	// buildBlockFrameworkMismatch: the app selects framework source other than
	// this producer's, and no handoff to the selected producer succeeded. Only
	// a changed selection (go.mod) resolves it.
	buildBlockFrameworkMismatch = "framework_mismatch"
	// buildBlockMigrationPending: a schema migration is pending, which a
	// running supervisor cannot apply. Changed migration inputs, or down, db
	// migrate and up, resolve it.
	buildBlockMigrationPending = "migration_pending"
	buildBlockSeedChanged      = "seed_changed"
	buildBlockGeneratedClients = "generated_clients_stale"
)

// Applied seeds are immutable; unrelated edits cannot repair their ledger.
type changedAppliedSeedError struct{ Path string }

func (e *changedAppliedSeedError) Error() string {
	return fmt.Sprintf("seed %s changed after it was applied; restore the applied seed and put new data changes in a migration", e.Path)
}

func (*changedAppliedSeedError) ExitCode() int { return 3 }

// devBuildBlock is the state of a supervisor whose builds are blocked. The
// runtime keeps serving its last good generation while the requested changes
// stay unapplied.
type devBuildBlock struct {
	Reason string
	Cause  string
	// Key is the digest of the inputs that can resolve the block; a rebuild
	// that finds them unchanged is prevented instead of repeated.
	Key       string
	Since     time.Time
	Prevented int
}

// deterministicBuildBlock names the block a build failure starts, or "" for
// a failure an ordinary edit may resolve.
func deterministicBuildBlock(err error) string {
	var mismatch *build.FrameworkMismatchError
	if errors.As(err, &mismatch) {
		return buildBlockFrameworkMismatch
	}
	var pending *pendingMigrationError
	if errors.As(err, &pending) {
		return buildBlockMigrationPending
	}
	var seed *changedAppliedSeedError
	if errors.As(err, &seed) {
		return buildBlockSeedChanged
	}
	var contract *build.ContractError
	if errors.As(err, &contract) && contract.Diagnostic.Code == "SCN6204" && strings.Contains(contract.Diagnostic.Message, "generated TypeScript clients are stale") {
		return buildBlockGeneratedClients
	}
	return ""
}

// buildBlockKey digests the captured inputs that can resolve a block.
func buildBlockKey(reason string, snapshot fileSnapshot) string {
	var paths []string
	for path := range snapshot.files {
		slash := filepath.ToSlash(path)
		switch reason {
		case buildBlockFrameworkMismatch:
			if slash == "go.mod" || slash == "go.sum" {
				paths = append(paths, path)
			}
		case buildBlockMigrationPending, buildBlockSeedChanged:
			if strings.HasSuffix(slash, ".sql") || strings.Contains(slash, "/migrations/") || strings.HasPrefix(slash, "migrations/") || strings.HasPrefix(filepath.Base(slash), ".scenery") {
				paths = append(paths, path)
			}
			if reason == buildBlockSeedChanged && strings.HasSuffix(slash, ".scn") {
				paths = append(paths, path)
			}
		case buildBlockGeneratedClients:
			if strings.HasSuffix(slash, ".scn") || slash == ".scenery.json" || slash == "go.mod" || slash == "go.sum" {
				paths = append(paths, path)
			}
		}
	}
	if reason == buildBlockGeneratedClients {
		for path := range snapshot.generatedContent {
			if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, "scenery.typescript-client-generated.json") {
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte(reason))
	for _, path := range paths {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		stamp, ok := snapshot.files[path]
		if !ok {
			stamp = snapshot.generatedContent[path]
		}
		_, _ = h.Write([]byte(stamp.hash))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// recordBuildOutcome starts, keeps or ends the build block after a build of
// the captured snapshot, and reports whether a block was or is in effect, so
// the status that carries it must be persisted again.
func (s *devSupervisor) recordBuildOutcome(captured fileSnapshot, err error) bool {
	s.mu.Lock()
	previous := s.buildBlock
	hadBlock := s.buildBlock != nil
	reason := ""
	if err != nil {
		reason = deterministicBuildBlock(err)
	}
	switch {
	case reason == "":
		s.buildBlock = nil
	case s.buildBlock != nil && s.buildBlock.Reason == reason && s.buildBlock.Key == buildBlockKey(reason, captured):
		s.buildBlock.Cause = err.Error()
	default:
		s.buildBlock = &devBuildBlock{Reason: reason, Cause: err.Error(), Key: buildBlockKey(reason, captured), Since: time.Now().UTC()}
	}
	record := s.publishBuildBlockLocked()
	current := s.buildBlock
	changed := previous != current
	var previousValue, currentValue devBuildBlock
	if previous != nil {
		previousValue = *previous
	}
	if current != nil {
		currentValue = *current
	}
	blocked := hadBlock || s.buildBlock != nil
	s.mu.Unlock()
	record.write()
	if s.console != nil && changed {
		if previous != nil {
			s.console.Event("build.unblocked", map[string]any{"since": previousValue.Since.Format(time.RFC3339Nano), "reason": previousValue.Reason, "cause": previousValue.Cause, "prevented_builds": previousValue.Prevented})
		}
		if current != nil {
			s.console.BuildBlocked(currentValue)
		}
	}
	return blocked
}

// preventBlockedBuild reports whether a build of snapshot would repeat the
// failure that blocked the last one, and counts it as prevented.
func (s *devSupervisor) preventBlockedBuild(snapshot fileSnapshot) (devBuildBlock, bool) {
	s.mu.Lock()
	if s.buildBlock == nil || buildBlockKey(s.buildBlock.Reason, snapshot) != s.buildBlock.Key {
		s.mu.Unlock()
		return devBuildBlock{}, false
	}
	s.buildBlock.Prevented++
	block := *s.buildBlock
	record := s.publishBuildBlockLocked()
	s.mu.Unlock()
	record.write()
	return block, true
}

// publishBuildBlockLocked copies the block into the status the supervisor
// persists for the runtime status, and returns the session record that ps and
// doctor read.
func (s *devSupervisor) publishBuildBlockLocked() pendingBuildBlockRecord {
	s.status.UpdatedAt = time.Now().UTC()
	pending := pendingBuildBlockRecord{}
	if s.agentSession != nil && strings.TrimSpace(s.agentSession.StateRoot) != "" {
		pending.path = sessionBuildBlockPath(s.agentSession.StateRoot)
	}
	if s.buildBlock == nil {
		s.status.BuildBlock = nil
		return pending
	}
	s.status.BuildBlock = &devdash.BuildBlock{Reason: s.buildBlock.Reason, Cause: s.buildBlock.Cause, Since: s.buildBlock.Since.Format(time.RFC3339), PreventedBuilds: s.buildBlock.Prevented}
	record := sessionBuildBlock{ArtifactIdentity: sessionBuildBlockIdentity(), Reason: s.buildBlock.Reason, Cause: s.buildBlock.Cause, Since: s.buildBlock.Since, PreventedBuilds: s.buildBlock.Prevented}
	if s.agentSession != nil {
		record.SessionID = s.agentSession.SessionID
		record.Owner = s.agentSession.Owner
	}
	pending.record = &record
	return pending
}

const (
	sessionBuildBlockKind             = "scenery.dev.build-block"
	sessionBuildBlockSchemaDescriptor = `{"identity":"artifact","record":"session-build-block"}`
	sessionBuildBlockFile             = "build-block.json"
)

// sessionBuildBlock is the record a blocked session keeps in its state root,
// so that ps and doctor can report the block without asking the supervisor.
// It is removed when the block ends; a record whose owner is gone is stale.
type sessionBuildBlock struct {
	machine.ArtifactIdentity
	SessionID       string           `json:"session_id"`
	Owner           localagent.Owner `json:"owner"`
	Reason          string           `json:"reason"`
	Cause           string           `json:"cause"`
	Since           time.Time        `json:"since"`
	PreventedBuilds int              `json:"prevented_builds"`
}

func sessionBuildBlockIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(sessionBuildBlockKind, sessionBuildBlockSchemaDescriptor)
}

func sessionBuildBlockPath(stateRoot string) string {
	return filepath.Join(stateRoot, sessionBuildBlockFile)
}

type pendingBuildBlockRecord struct {
	path   string
	record *sessionBuildBlock
}

// write stores or removes the record. The record only reports the block, so
// a failed write never fails the build loop.
func (p pendingBuildBlockRecord) write() {
	if p.path == "" {
		return
	}
	if p.record == nil {
		_ = os.Remove(p.path)
		return
	}
	data, err := json.MarshalIndent(p.record, "", "  ")
	if err != nil {
		return
	}
	_ = atomicWriteFile(p.path, append(data, '\n'), 0o600)
}

// liveSessionBuildBlock reads the build block of the session whose state root
// is given; it reports false when there is none or its owner is gone.
func liveSessionBuildBlock(stateRoot string) (sessionBuildBlock, bool) {
	data, err := os.ReadFile(sessionBuildBlockPath(stateRoot))
	if err != nil {
		return sessionBuildBlock{}, false
	}
	var record sessionBuildBlock
	if machine.DecodeArtifact(data, &record, &record.ArtifactIdentity, sessionBuildBlockKind, sessionBuildBlockSchemaDescriptor, "restart scenery up") != nil {
		return sessionBuildBlock{}, false
	}
	if localagent.VerifyOwner(record.Owner) != nil {
		return sessionBuildBlock{}, false
	}
	return record, true
}
