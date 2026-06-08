package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const neonRestorePointsSchemaVersion = "onlava.db.neon.restore_points.v1"

type neonBranchRestorePoint struct {
	Ref          string    `json:"ref"`
	BranchID     string    `json:"branch_id"`
	Branch       string    `json:"branch"`
	Project      string    `json:"project"`
	DatabaseName string    `json:"database_name"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
	RestoredFrom string    `json:"restored_from,omitempty"`
}

type neonRestorePointsState struct {
	SchemaVersion string                              `json:"schema_version"`
	Points        map[string][]neonBranchRestorePoint `json:"points"`
	UpdatedAt     time.Time                           `json:"updated_at"`
}

func neonRestorePointsPath(root string) string {
	return filepath.Join(root, "restore-points.json")
}

func readNeonRestorePointsState() (neonRestorePointsState, string, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return neonRestorePointsState{}, "", err
	}
	path := neonRestorePointsPath(root)
	state := neonRestorePointsState{
		SchemaVersion: neonRestorePointsSchemaVersion,
		Points:        map[string][]neonBranchRestorePoint{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, path, nil
	}
	if err != nil {
		return neonRestorePointsState{}, "", err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return neonRestorePointsState{}, "", fmt.Errorf("parse %s: %w", path, err)
	}
	if state.SchemaVersion != neonRestorePointsSchemaVersion {
		return neonRestorePointsState{}, "", fmt.Errorf("%s has unsupported schema_version %q", path, state.SchemaVersion)
	}
	if state.Points == nil {
		state.Points = map[string][]neonBranchRestorePoint{}
	}
	return state, path, nil
}

func writeNeonRestorePointsState(path string, state neonRestorePointsState) error {
	if state.Points == nil {
		state.Points = map[string][]neonBranchRestorePoint{}
	}
	state.SchemaVersion = neonRestorePointsSchemaVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o644)
}

func ensureInitialNeonRestorePoint(pin worktreeDBPin) error {
	state, _, err := readNeonRestorePointsState()
	if err != nil {
		return err
	}
	if len(state.Points[pin.BranchID]) > 0 {
		return nil
	}
	_, err = recordNeonRestorePoint(pin, "branch-created", "")
	return err
}

func recordNeonRestorePoint(pin worktreeDBPin, source string, restoredFrom string) (neonBranchRestorePoint, error) {
	state, path, err := readNeonRestorePointsState()
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	now := time.Now().UTC()
	point := neonBranchRestorePoint{
		Ref:          now.Format("20060102T150405.000000000Z07:00"),
		BranchID:     pin.BranchID,
		Branch:       pin.Branch,
		Project:      pin.Project,
		DatabaseName: pin.Database,
		Source:       strings.TrimSpace(source),
		CreatedAt:    now,
		RestoredFrom: strings.TrimSpace(restoredFrom),
	}
	if point.Source == "" {
		point.Source = "branch-updated"
	}
	state.Points[pin.BranchID] = append(state.Points[pin.BranchID], point)
	sort.Slice(state.Points[pin.BranchID], func(i, j int) bool {
		return state.Points[pin.BranchID][i].CreatedAt.Before(state.Points[pin.BranchID][j].CreatedAt)
	})
	state.UpdatedAt = now
	if err := writeNeonRestorePointsState(path, state); err != nil {
		return neonBranchRestorePoint{}, err
	}
	return point, nil
}

func resolveNeonRestorePoint(branchID, at string) (neonBranchRestorePoint, error) {
	state, _, err := readNeonRestorePointsState()
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	points := append([]neonBranchRestorePoint(nil), state.Points[branchID]...)
	if len(points) == 0 {
		return neonBranchRestorePoint{}, fmt.Errorf("no restore points recorded for Neon branch %s", branchID)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].CreatedAt.Before(points[j].CreatedAt) })
	at = strings.TrimSpace(at)
	if at == "" {
		return points[len(points)-1], nil
	}
	for _, point := range points {
		if point.Ref == at {
			return point, nil
		}
	}
	when, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		when, err = time.Parse(time.RFC3339, at)
	}
	if err != nil {
		return neonBranchRestorePoint{}, fmt.Errorf("unknown Neon restore point %q; use an exact restore point ref or RFC3339 timestamp", at)
	}
	var chosen neonBranchRestorePoint
	for _, point := range points {
		if point.CreatedAt.After(when) {
			break
		}
		chosen = point
	}
	if chosen.Ref == "" {
		return neonBranchRestorePoint{}, fmt.Errorf("no Neon restore point exists at or before %s", at)
	}
	return chosen, nil
}

func deleteNeonRestorePoints(branchID string) error {
	state, path, err := readNeonRestorePointsState()
	if err != nil {
		return err
	}
	if _, ok := state.Points[branchID]; !ok {
		return nil
	}
	delete(state.Points, branchID)
	state.UpdatedAt = time.Now().UTC()
	return writeNeonRestorePointsState(path, state)
}
