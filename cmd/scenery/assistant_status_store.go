package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/spec"
)

// assistantStatusSnapshotSchemaRevision identifies the machine-local status
// file. It is deliberately not a public CLI schema: the file is a transient
// supervisor handoff and is never returned by a public inspection command.
const assistantStatusSnapshotSchemaDescriptor = `{"schema_revision":"digest","assistants":"array<assistant{address,name,path,access,session_access,policy,expected_runtime_revision,expected_capability_revision,actual_runtime_revision,actual_capability_revision,ready,status,restart_count,last_failure,log_source,implementation}>"}`

var assistantStatusSnapshotSchemaRevision = string(spec.SchemaRevision(assistantStatusSnapshotSchemaDescriptor))

// assistantStatusRecord is the canonical state written by the dev supervisor
// and consumed by later CLI invocations. Its optional implementation member is
// retained only for the explicit implementation inspection path; default
// payload construction drops it before encoding.
type assistantStatusRecord struct {
	Address                    string                         `json:"address"`
	Name                       string                         `json:"name"`
	Path                       string                         `json:"path"`
	Access                     string                         `json:"access"`
	SessionAccess              string                         `json:"session_access"`
	Policy                     assistantPolicyRecord          `json:"policy"`
	ExpectedRuntimeRevision    string                         `json:"expected_runtime_revision"`
	ExpectedCapabilityRevision string                         `json:"expected_capability_revision"`
	ActualRuntimeRevision      string                         `json:"actual_runtime_revision,omitempty"`
	ActualCapabilityRevision   string                         `json:"actual_capability_revision,omitempty"`
	Ready                      bool                           `json:"ready"`
	Status                     string                         `json:"status"`
	RestartCount               int                            `json:"restart_count"`
	LastFailure                string                         `json:"last_failure,omitempty"`
	LogSource                  string                         `json:"log_source"`
	Implementation             *assistantImplementationRecord `json:"implementation,omitempty"`
}

// assistantStatusSnapshot is atomically replaced as a whole so a CLI reader
// never observes a partially updated set of assistants.
type assistantStatusSnapshot struct {
	SchemaRevision string                  `json:"schema_revision"`
	Assistants     []assistantStatusRecord `json:"assistants"`
}

var assistantStatusWriteSeq atomic.Uint64

func assistantStatusSnapshotPath(appRoot string) (string, error) {
	root := strings.TrimSpace(appRoot)
	if root == "" {
		return "", errors.New("assistant status app root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve assistant status app root: %w", err)
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return "", errors.New("assistant status app root must not be filesystem root")
	}
	return filepath.Join(abs, ".scenery", "run", "assistants.json"), nil
}

func validateAssistantStatusRecord(record assistantStatusRecord) error {
	for field, value := range map[string]string{
		"address":                      record.Address,
		"name":                         record.Name,
		"path":                         record.Path,
		"access":                       record.Access,
		"session_access":               record.SessionAccess,
		"expected_runtime_revision":    record.ExpectedRuntimeRevision,
		"expected_capability_revision": record.ExpectedCapabilityRevision,
		"status":                       record.Status,
		"log_source":                   record.LogSource,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("assistant status %s is required", field)
		}
	}
	if record.RestartCount < 0 {
		return errors.New("assistant status restart_count must not be negative")
	}
	if !strings.HasPrefix(record.Path, "/") || strings.ContainsAny(record.Path, "?#") {
		return fmt.Errorf("assistant status path %q is invalid", record.Path)
	}
	if record.SessionAccess != "initiator" {
		return fmt.Errorf("assistant status session_access %q is unsupported", record.SessionAccess)
	}
	if record.Access != "public" && record.Access != "auth" {
		return fmt.Errorf("assistant status access %q is unsupported", record.Access)
	}
	if record.Policy.Authentication == "" || record.Policy.Authorization == "" || record.Policy.Pipeline == "" {
		return errors.New("assistant status policy is incomplete")
	}
	if record.LastFailure != "" && !assistantStatusCode(record.LastFailure) {
		return errors.New("assistant status last_failure must be a lowercase stable code")
	}
	return nil
}

func validateAssistantStatusSnapshot(snapshot assistantStatusSnapshot) error {
	if snapshot.SchemaRevision != assistantStatusSnapshotSchemaRevision {
		return fmt.Errorf("assistant status snapshot schema_revision %q is unsupported", snapshot.SchemaRevision)
	}
	seen := map[string]bool{}
	for _, record := range snapshot.Assistants {
		if err := validateAssistantStatusRecord(record); err != nil {
			return err
		}
		if seen[record.Address] {
			return fmt.Errorf("assistant status snapshot contains duplicate address %q", record.Address)
		}
		seen[record.Address] = true
	}
	return nil
}

// writeAssistantStatusSnapshot atomically persists the complete provider-
// neutral assistant status set for an app root. The supervisor can call this
// after every lifecycle transition; readers tolerate a missing first snapshot.
func writeAssistantStatusSnapshot(appRoot string, records []assistantStatusRecord) error {
	path, err := assistantStatusSnapshotPath(appRoot)
	if err != nil {
		return err
	}
	ordered := append([]assistantStatusRecord(nil), records...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Address < ordered[j].Address })
	snapshot := assistantStatusSnapshot{SchemaRevision: assistantStatusSnapshotSchemaRevision, Assistants: ordered}
	if err := validateAssistantStatusSnapshot(snapshot); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode assistant status snapshot: %w", err)
	}
	// The sequence gives callers a cheap way to ensure writes are not silently
	// elided by future batching, without exposing it in the persisted shape.
	assistantStatusWriteSeq.Add(1)
	if err := atomicfile.Write(path, append(data, '\n'), 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return fmt.Errorf("write assistant status snapshot: %w", err)
	}
	return nil
}

// readAssistantStatusSnapshot loads the latest valid status set. A missing
// file means no supervisor has published state yet and is not an error.
func readAssistantStatusSnapshot(appRoot string) (map[string]assistantStatusRecord, error) {
	path, err := assistantStatusSnapshotPath(appRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]assistantStatusRecord{}, nil
		}
		return nil, fmt.Errorf("inspect assistant status snapshot: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("assistant status snapshot must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("assistant status snapshot must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("assistant status snapshot must not be group/world accessible")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read assistant status snapshot: %w", err)
	}
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return nil, fmt.Errorf("decode assistant status snapshot: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot assistantStatusSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode assistant status snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, errors.New("assistant status snapshot has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode assistant status snapshot trailing data: %w", err)
	}
	if err := validateAssistantStatusSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("validate assistant status snapshot: %w", err)
	}
	result := make(map[string]assistantStatusRecord, len(snapshot.Assistants))
	for _, record := range snapshot.Assistants {
		result[record.Address] = record
	}
	return result, nil
}

func assistantStatusCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			if index == 0 && (character == '_' || character == '-' || character == '.') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// rejectDuplicateJSONObjects protects strict snapshots from duplicate-member
// ambiguity before encoding/json decodes last-wins values.
func rejectDuplicateJSONObjects(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch delimiter := token.(type) {
		case json.Delim:
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok {
						return errors.New("assistant status snapshot object key is not a string")
					}
					if seen[name] {
						return fmt.Errorf("assistant status snapshot contains duplicate field %q", name)
					}
					seen[name] = true
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = decoder.Token()
				return err
			case '[':
				for decoder.More() {
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = decoder.Token()
				return err
			default:
				return fmt.Errorf("assistant status snapshot has unexpected delimiter %q", delimiter)
			}
		default:
			return nil
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err == nil {
		return errors.New("assistant status snapshot has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
