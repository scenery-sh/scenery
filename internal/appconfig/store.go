package appconfig

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetainedUnpinnedRevisions is how many newest unpinned history revisions
// survive pruning besides the desired and pinned ones.
const RetainedUnpinnedRevisions = 16

// ErrRevisionConflict reports a failed expected-revision precondition. The
// store is unchanged.
var ErrRevisionConflict = errors.New("environment revision changed")

// Store is the authoritative configuration store of one application:
//
//	<home>/apps/<app-id>/
//	  environments/<env>.json
//	  environment-history/<env>/<revision>.json
//	  environment-history/<env>/pins/<holder>.json
//	  environment-locks/<env>.lock
//	  secrets/<env>/...
type Store struct {
	appID string
	dir   string
	// flush makes a written file or directory durable. Unit tests replace it;
	// real durability is proved by the configuration probe.
	flush func(*os.File) error
}

// OpenStore opens the store of appID under a Scenery home. Nothing is created
// until a mutation.
func OpenStore(home, appID string) (*Store, error) {
	if !ValidIdentifier(appID) {
		return nil, fmt.Errorf("application id %q is not a path-safe identifier; set an explicit lowercase \"id\" in .scenery.json", appID)
	}
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("scenery home %q is not absolute", home)
	}
	return &Store{appID: appID, dir: filepath.Join(home, "apps", appID), flush: (*os.File).Sync}, nil
}

// AppID returns the application the store belongs to.
func (s *Store) AppID() string { return s.appID }

// Dir returns the application's store directory.
func (s *Store) Dir() string { return s.dir }

// SecretsDir returns the backend-owned secret directory of an environment.
func (s *Store) SecretsDir(environment string) string {
	return filepath.Join(s.dir, "secrets", environment)
}

func checkEnvironment(environment string) error {
	if !ValidIdentifier(environment) {
		return fmt.Errorf("environment %q is not a path-safe identifier", environment)
	}
	return nil
}

// root opens the application directory, creating it privately when create
// is set. It refuses a symlinked application directory.
func (s *Store) root(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("configuration store %s is not a directory", s.dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if !create {
			return nil, fmt.Errorf("configuration store %s is accessible by other users; restrict it to mode 0700", s.dir)
		}
		if err := os.Chmod(s.dir, 0o700); err != nil {
			return nil, err
		}
	}
	return os.OpenRoot(s.dir)
}

// mkdirs creates a relative directory path inside root. Concurrent creators
// of the same directory are expected, so an existing directory is success.
func mkdirs(root *os.Root, name string) error {
	current := ""
	for part := range strings.SplitSeq(name, "/") {
		current = path.Join(current, part)
		if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", current)
		}
	}
	return nil
}

func readRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return root.ReadFile(name)
}

// Read returns the desired document of an environment. A missing document is
// the empty environment (exists=false); an unreadable or malformed one is an
// error, never an empty environment.
func (s *Store) Read(environment string) (Document, bool, error) {
	if err := checkEnvironment(environment); err != nil {
		return Document{}, false, err
	}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return NewDocument(s.appID, environment), false, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	defer func() { _ = root.Close() }()
	return s.readDesired(root, environment)
}

func (s *Store) readDesired(root *os.Root, environment string) (Document, bool, error) {
	data, err := readRegular(root, path.Join("environments", environment+".json"), MaxDocumentBytes)
	if errors.Is(err, os.ErrNotExist) {
		return NewDocument(s.appID, environment), false, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	document, err := DecodeDocument(data, s.appID, environment)
	if err != nil {
		return Document{}, false, err
	}
	return document, true, nil
}

// ReadRevision returns one retained revision of an environment.
func (s *Store) ReadRevision(environment, revision string) (Document, error) {
	if err := checkEnvironment(environment); err != nil {
		return Document{}, err
	}
	if !ValidRevision(revision) {
		return Document{}, fmt.Errorf("configuration revision %q is malformed", revision)
	}
	if revision == NewDocument(s.appID, environment).Revision {
		return NewDocument(s.appID, environment), nil
	}
	root, err := s.root(false)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = root.Close() }()
	data, err := readRegular(root, path.Join("environment-history", environment, revision+".json"), MaxDocumentBytes)
	if err != nil {
		return Document{}, fmt.Errorf("configuration revision %s of %s is not retained: %w", revision, environment, err)
	}
	return DecodeDocument(data, s.appID, environment)
}

// Mutation changes one key. Exactly one of Value, Secret or Unset applies.
type Mutation struct {
	Key            string
	Value          json.RawMessage
	Secret         *SecretVersion
	Unset          bool
	ExpectRevision string
}

// MutationResult reports a mutation's revisions. Changed is false for a no-op.
type MutationResult struct {
	PreviousRevision string `json:"previous_revision"`
	Revision         string `json:"revision"`
	Changed          bool   `json:"changed"`
}

// Mutate applies one key change under the environment lock: it rereads the
// desired document, changes only that key, publishes the immutable history
// revision and then atomically replaces the desired document. Concurrent
// writers of different keys therefore never lose each other's changes.
func (s *Store) Mutate(ctx context.Context, environment string, mutation Mutation) (MutationResult, error) {
	if err := checkEnvironment(environment); err != nil {
		return MutationResult{}, err
	}
	if !keyPattern.MatchString(mutation.Key) {
		return MutationResult{}, fmt.Errorf("configuration key %q is malformed", mutation.Key)
	}
	operations := 0
	for _, set := range []bool{mutation.Value != nil, mutation.Secret != nil, mutation.Unset} {
		if set {
			operations++
		}
	}
	if operations != 1 {
		return MutationResult{}, fmt.Errorf("a mutation sets a value, sets a secret or unsets")
	}
	if mutation.Value != nil {
		var compact bytes.Buffer
		if len(mutation.Value) > MaxValueBytes || json.Compact(&compact, mutation.Value) != nil {
			return MutationResult{}, fmt.Errorf("configuration value for %s is not bounded JSON", mutation.Key)
		}
		mutation.Value = compact.Bytes()
	}
	if mutation.Secret != nil && (!ValidIdentifier(mutation.Secret.Backend) || !versionPattern.MatchString(mutation.Secret.Version)) {
		return MutationResult{}, fmt.Errorf("secret version reference is malformed")
	}
	root, err := s.root(true)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = root.Close() }()
	unlock, err := lockEnvironment(ctx, root, environment)
	if err != nil {
		return MutationResult{}, err
	}
	defer unlock()
	current, _, err := s.readDesired(root, environment)
	if err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{PreviousRevision: current.Revision, Revision: current.Revision}
	if mutation.ExpectRevision != "" && mutation.ExpectRevision != current.Revision {
		return result, fmt.Errorf("%w: expected %s, current %s", ErrRevisionConflict, mutation.ExpectRevision, current.Revision)
	}
	next := current.clone()
	delete(next.Values, mutation.Key)
	delete(next.Secrets, mutation.Key)
	switch {
	case mutation.Value != nil:
		next.Values[mutation.Key] = append(json.RawMessage(nil), mutation.Value...)
	case mutation.Secret != nil:
		next.Secrets[mutation.Key] = *mutation.Secret
	}
	next.Revision = next.contentRevision()
	if next.Revision == current.Revision {
		return result, nil
	}
	encoded, err := next.encode()
	if err != nil {
		return result, err
	}
	historyDir := path.Join("environment-history", environment)
	if err := mkdirs(root, historyDir); err != nil {
		return result, err
	}
	if err := s.writeAtomic(root, path.Join(historyDir, next.Revision+".json"), encoded); err != nil {
		return result, err
	}
	if err := mkdirs(root, "environments"); err != nil {
		return result, err
	}
	if err := s.writeAtomic(root, path.Join("environments", environment+".json"), encoded); err != nil {
		return result, err
	}
	result.Revision, result.Changed = next.Revision, true
	return result, nil
}

// writeAtomic writes a private temporary file, flushes it, renames it over
// name and flushes the directory, so readers observe either the old or the
// new complete file.
func (s *Store) writeAtomic(root *os.Root, name string, data []byte) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temporary := name + ".tmp-" + hex.EncodeToString(suffix)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := s.flush(file)
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = root.Remove(temporary)
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		_ = root.Remove(temporary)
		return err
	}
	if directory, err := root.Open(path.Dir(name)); err == nil {
		_ = s.flush(directory)
		_ = directory.Close()
	}
	return nil
}

// Pin is one holder's use of a revision. A running local generation also
// records the desired revision it last considered and whether it applied it.
type Pin struct {
	Revision string `json:"revision"`
	Desired  string `json:"desired,omitempty"`
	State    string `json:"state,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

// Pin records that holder (a running generation, an active or rollback
// deployment) uses revision, which keeps it and its secret versions retained.
func (s *Store) Pin(environment, holder, revision string) error {
	return s.PinRecord(environment, holder, Pin{Revision: revision})
}

// PinRecord writes holder's complete pin.
func (s *Store) PinRecord(environment, holder string, pin Pin) error {
	if err := checkEnvironment(environment); err != nil {
		return err
	}
	if !ValidIdentifier(holder) || !ValidRevision(pin.Revision) || (pin.Desired != "" && !ValidRevision(pin.Desired)) || len(pin.Problem) > 4096 {
		return fmt.Errorf("configuration pin %q is malformed", holder)
	}
	root, err := s.root(true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	pins := path.Join("environment-history", environment, "pins")
	if err := mkdirs(root, pins); err != nil {
		return err
	}
	encoded, err := json.Marshal(pin)
	if err != nil {
		return err
	}
	return s.writeAtomic(root, path.Join(pins, holder+".json"), encoded)
}

// Unpin releases holder's pin.
func (s *Store) Unpin(environment, holder string) error {
	if err := checkEnvironment(environment); err != nil {
		return err
	}
	if !ValidIdentifier(holder) {
		return fmt.Errorf("configuration pin holder %q is malformed", holder)
	}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = root.Remove(path.Join("environment-history", environment, "pins", holder+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Pins returns every holder's pinned revision.
func (s *Store) Pins(environment string) (map[string]string, error) {
	records, err := s.PinRecords(environment)
	if err != nil {
		return nil, err
	}
	pins := make(map[string]string, len(records))
	for holder, record := range records {
		pins[holder] = record.Revision
	}
	return pins, nil
}

// PinRecords returns every holder's complete pin.
func (s *Store) PinRecords(environment string) (map[string]Pin, error) {
	if err := checkEnvironment(environment); err != nil {
		return nil, err
	}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Pin{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return readPins(root, environment)
}

func readPins(root *os.Root, environment string) (map[string]Pin, error) {
	pins := map[string]Pin{}
	directory := path.Join("environment-history", environment, "pins")
	entries, err := fs.ReadDir(root.FS(), directory)
	if errors.Is(err, os.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		holder, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !ValidIdentifier(holder) {
			continue
		}
		data, err := readRegular(root, path.Join(directory, entry.Name()), 4096)
		if err != nil {
			return nil, err
		}
		var pin Pin
		if err := json.Unmarshal(data, &pin); err != nil || !ValidRevision(pin.Revision) {
			return nil, fmt.Errorf("configuration pin %s is malformed", holder)
		}
		pins[holder] = pin
	}
	return pins, nil
}

// PruneResult reports what pruning removed and which secret versions no
// retained revision references any more.
type PruneResult struct {
	RemovedRevisions []string
	OrphanSecrets    map[string][]SecretVersion
}

// Prune keeps the desired revision, every pinned revision and the newest
// RetainedUnpinnedRevisions others, removes the rest and reports secret
// versions that are referenced by none of the kept revisions. known lists
// secret versions the backend holds, keyed by configuration key; versions it
// holds that no kept revision references are orphans. Pins are never removed
// by age.
func (s *Store) Prune(ctx context.Context, environment string, known map[string][]SecretVersion) (PruneResult, error) {
	result := PruneResult{OrphanSecrets: map[string][]SecretVersion{}}
	if err := checkEnvironment(environment); err != nil {
		return result, err
	}
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	unlock, err := lockEnvironment(ctx, root, environment)
	if err != nil {
		return result, err
	}
	defer unlock()
	desired, _, err := s.readDesired(root, environment)
	if err != nil {
		return result, err
	}
	pins, err := readPins(root, environment)
	if err != nil {
		return result, err
	}
	keep := map[string]bool{desired.Revision: true}
	for _, pin := range pins {
		keep[pin.Revision] = true
	}
	directory := path.Join("environment-history", environment)
	entries, err := fs.ReadDir(root.FS(), directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	type revisionFile struct {
		revision string
		modified time.Time
	}
	var unpinned []revisionFile
	for _, entry := range entries {
		revision, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !ValidRevision(revision) || keep[revision] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		unpinned = append(unpinned, revisionFile{revision, info.ModTime()})
	}
	sort.Slice(unpinned, func(i, j int) bool {
		if !unpinned[i].modified.Equal(unpinned[j].modified) {
			return unpinned[i].modified.After(unpinned[j].modified)
		}
		return unpinned[i].revision > unpinned[j].revision
	})
	for index, file := range unpinned {
		if index < RetainedUnpinnedRevisions {
			keep[file.revision] = true
			continue
		}
		if err := root.Remove(path.Join(directory, file.revision+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		result.RemovedRevisions = append(result.RemovedRevisions, file.revision)
	}
	referenced := map[SecretVersion]bool{}
	for _, version := range desired.Secrets {
		referenced[version] = true
	}
	for revision := range keep {
		if revision == desired.Revision {
			continue
		}
		data, err := readRegular(root, path.Join(directory, revision+".json"), MaxDocumentBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		document, err := DecodeDocument(data, s.appID, environment)
		if err != nil {
			// An undecodable retained revision may still be live somewhere; its
			// secrets must not be collected.
			return result, fmt.Errorf("retained revision %s: %w", revision, err)
		}
		for _, version := range document.Secrets {
			referenced[version] = true
		}
	}
	for key, versions := range known {
		for _, version := range versions {
			if !referenced[version] {
				result.OrphanSecrets[key] = append(result.OrphanSecrets[key], version)
			}
		}
	}
	return result, nil
}

// NewSecretVersion returns a fresh opaque secret version identifier.
func NewSecretVersion(backend string) (SecretVersion, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return SecretVersion{}, err
	}
	return SecretVersion{Backend: backend, Version: hex.EncodeToString(raw)}, nil
}
