// Package stateupgrade applies explicit, root-bound artifact identity changes.
// Domain owners validate payloads and hold their lifecycle locks before use.
package stateupgrade

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

const (
	PendingName = "spec-upgrade.json"
	backupRoot  = "spec-upgrades"
	backupKind  = "scenery.state-upgrade.backup"
	markerKind  = "scenery.state-upgrade.pending"
	backupShape = `{"identity":"artifact","root":"absolute-private-root","revision":"digest","changes":[{"path":"relative-metadata-path","kind":"artifact-kind","before":"base64-bytes","after":"base64-bytes"}]}`
	markerShape = `{"identity":"artifact","revision":"digest"}`
)

var ErrPrecondition = errors.New("retained-state upgrade precondition failed")

// Change contains private metadata, potentially including credentials. Never
// serialize changes into a public CLI response or a diagnostic.
type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Before []byte `json:"before"`
	After  []byte `json:"after"`
}

type Plan struct {
	Revision string
	Changes  []Change
	Pending  bool
	Updated  int
}

func (p Plan) ChangedFiles() int {
	count := 0
	for _, change := range p.Changes {
		if !bytes.Equal(change.Before, change.After) {
			count++
		}
	}
	return count
}

type backup struct {
	machine.ArtifactIdentity
	Root     string   `json:"root"`
	Revision string   `json:"revision"`
	Changes  []Change `json:"changes"`
}

type marker struct {
	machine.ArtifactIdentity
	Revision string `json:"revision"`
}

// Resolve binds a new preview or validates the original interrupted transaction
// against the complete currently observed selection. It never creates files.
func (s *Store) Resolve(observed []Change) (Plan, error) {
	current, err := newPlan(s.path, observed)
	if err != nil {
		return Plan{}, err
	}
	pending, err := s.pending()
	if err != nil || pending == nil {
		return current, err
	}
	if len(current.Changes) != len(pending.Changes) {
		return Plan{}, fmt.Errorf("%w: pending metadata selection changed", ErrPrecondition)
	}
	for i, expected := range pending.Changes {
		actual := current.Changes[i]
		if actual.Path != expected.Path || actual.Kind != expected.Kind ||
			(!bytes.Equal(actual.Before, expected.Before) && !bytes.Equal(actual.Before, expected.After)) {
			return Plan{}, fmt.Errorf("%w: pending metadata no longer matches %s", ErrPrecondition, expected.Path)
		}
		if !machine.ArtifactPayloadEqual(actual.Before, expected.After) {
			return Plan{}, fmt.Errorf("%w: pending payload differs for %s", ErrPrecondition, expected.Path)
		}
		if !bytes.Equal(expected.Before, expected.After) && bytes.Equal(actual.Before, expected.After) {
			pending.Updated++
		}
	}
	return *pending, nil
}

func newPlan(root string, changes []Change) (Plan, error) {
	if len(changes) == 0 {
		return Plan{}, fmt.Errorf("%w: no retained metadata selected", ErrPrecondition)
	}
	seen := make(map[string]bool, len(changes))
	var size int64
	for _, change := range changes {
		if !validMetadataPath(change.Path) || seen[change.Path] {
			return Plan{}, fmt.Errorf("%w: invalid or duplicate metadata path", ErrPrecondition)
		}
		seen[change.Path] = true
		if err := validateChange(change); err != nil {
			return Plan{}, err
		}
		size += int64(len(change.Before)) + int64(len(change.After))
		if size > maxBackupBytes/2 {
			return Plan{}, fmt.Errorf("%w: metadata backup exceeds its bounded transaction size", ErrPrecondition)
		}
	}
	encoded, err := json.Marshal(struct {
		Root   string   `json:"root"`
		Spec   string   `json:"spec"`
		Change []Change `json:"changes"`
	}{root, string(spec.CurrentRevision()), changes})
	if err != nil {
		return Plan{}, err
	}
	sum := sha256.Sum256(encoded)
	return Plan{Revision: "sha256:" + hex.EncodeToString(sum[:]), Changes: changes}, nil
}

func validateChange(change Change) error {
	if len(change.Before) == 0 || len(change.After) == 0 || len(change.Before) > maxMetadataBytes || len(change.After) > maxMetadataBytes || !machine.ArtifactPayloadEqual(change.Before, change.After) {
		return fmt.Errorf("%w: upgrade must preserve metadata payload at %s", ErrPrecondition, change.Path)
	}
	var before, after machine.ArtifactIdentity
	if json.Unmarshal(change.Before, &before) != nil || json.Unmarshal(change.After, &after) != nil || before.Kind != change.Kind || after.Kind != change.Kind ||
		!validDigest(before.SpecRevision) || !validDigest(before.SchemaRevision) || after.SchemaRevision != before.SchemaRevision ||
		after.SpecRevision != string(spec.CurrentRevision()) ||
		strings.TrimSpace(before.Producer.Version) == "" || strings.TrimSpace(before.Producer.Toolchain.GoVersion) == "" ||
		strings.TrimSpace(after.Producer.Version) == "" || strings.TrimSpace(after.Producer.Toolchain.GoVersion) == "" {
		return fmt.Errorf("%w: invalid metadata upgrade identity at %s", ErrPrecondition, change.Path)
	}
	return nil
}

func (s *Store) pending() (*Plan, error) {
	data, err := s.read(PendingName, maxMetadataBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending marker
	if err := machine.DecodeArtifact(data, &pending, &pending.ArtifactIdentity, markerKind, markerShape, "resume with the matching upgrade CLI"); err != nil {
		return nil, err
	}
	if !validDigest(pending.Revision) {
		return nil, fmt.Errorf("%w: invalid pending revision", ErrPrecondition)
	}
	data, err = s.read(backupFile(pending.Revision), maxBackupBytes)
	if err != nil {
		return nil, err
	}
	var saved backup
	if err := machine.DecodeArtifact(data, &saved, &saved.ArtifactIdentity, backupKind, backupShape, "resume with the matching upgrade CLI"); err != nil {
		return nil, err
	}
	plan, err := newPlan(s.path, saved.Changes)
	if err != nil {
		return nil, err
	}
	if saved.Root != s.path || saved.Revision != pending.Revision || plan.Revision != pending.Revision {
		return nil, fmt.Errorf("%w: backup does not match the selected root and revision", ErrPrecondition)
	}
	plan.Pending = true
	return &plan, nil
}

// Apply publishes only the original before/after bytes. Repeated calls complete
// interrupted work; backups remain after the pending guard is durably removed.
func (s *Store) Apply(plan Plan, expected string) (string, error) {
	checked, err := newPlan(s.path, plan.Changes)
	if err != nil {
		return "", err
	}
	if !validDigest(expected) || plan.Revision != checked.Revision || expected != plan.Revision {
		return "", fmt.Errorf("%w: preview revision changed; inspect the upgrade again", ErrPrecondition)
	}
	if _, err := s.Resolve(plan.Changes); err != nil {
		return "", err
	}
	if err := s.checkFiles(plan.Changes, plan.Pending); err != nil {
		return "", err
	}
	if plan.ChangedFiles() == 0 && !plan.Pending {
		return "", nil
	}
	name := backupFile(plan.Revision)
	saved := backup{ArtifactIdentity: machine.NewArtifactIdentity(backupKind, backupShape), Root: s.path, Revision: plan.Revision, Changes: plan.Changes}
	data, err := json.Marshal(saved)
	if err != nil {
		return "", err
	}
	if err := s.ensureBackup(name, data); err != nil {
		return "", err
	}
	pending := marker{ArtifactIdentity: machine.NewArtifactIdentity(markerKind, markerShape), Revision: plan.Revision}
	data, err = json.Marshal(pending)
	if err != nil {
		return "", err
	}
	if err := s.write(PendingName, data); err != nil {
		return "", err
	}
	for _, change := range plan.Changes {
		actual, err := s.read(change.Path, maxMetadataBytes)
		if err != nil {
			return "", err
		}
		if bytes.Equal(actual, change.After) {
			continue
		}
		if !bytes.Equal(actual, change.Before) {
			return "", fmt.Errorf("%w: metadata changed while applying %s", ErrPrecondition, change.Path)
		}
		if err := s.write(change.Path, change.After); err != nil {
			return "", err
		}
	}
	if err := s.checkAfter(plan.Changes); err != nil {
		return "", err
	}
	if err := s.write(filepath.Join(filepath.Dir(name), "completed"), []byte(plan.Revision+"\n")); err != nil {
		return "", err
	}
	if err := s.remove(PendingName); err != nil {
		return "", err
	}
	if err := s.syncDir("."); err != nil {
		return "", err
	}
	return filepath.Join(s.path, name), nil
}

func (s *Store) checkFiles(changes []Change, allowAfter bool) error {
	for _, change := range changes {
		data, err := s.read(change.Path, maxMetadataBytes)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, change.Before) && (!allowAfter || !bytes.Equal(data, change.After)) {
			return fmt.Errorf("%w: preview metadata changed at %s", ErrPrecondition, change.Path)
		}
	}
	return nil
}

func (s *Store) checkAfter(changes []Change) error {
	for _, change := range changes {
		data, err := s.read(change.Path, maxMetadataBytes)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, change.After) {
			return fmt.Errorf("%w: published metadata differs at %s", ErrPrecondition, change.Path)
		}
	}
	return nil
}

func backupFile(revision string) string {
	return filepath.Join(backupRoot, strings.TrimPrefix(revision, "sha256:"), "metadata.json")
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	for _, c := range value[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validMetadataPath(name string) bool {
	return filepath.IsLocal(name) && filepath.Clean(name) == name && name != "." && name != PendingName && name != backupRoot && !strings.HasPrefix(name, backupRoot+string(filepath.Separator))
}
