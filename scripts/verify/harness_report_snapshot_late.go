package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// An ordinary large supervisor row is consumed before its JSON decoding ends.
// Observe that actual endpoint, then reject it with the same inventory predicate
// used after confirmed stops. Rejection needs no additional stop or mutation.
// Neither elapsed time nor a product hook establishes this schedule.
func proveHarnessReportLateObservation(ctx context.Context, repo string) (proof map[string]any, resultErr error) {
	f, err := newHarnessReportFixture()
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup := os.RemoveAll(f.root)
		_, absence := os.Lstat(f.root)
		if !os.IsNotExist(absence) {
			cleanup = errors.Join(cleanup, fmt.Errorf("late fixture remains: %v", absence))
		}
		resultErr = errors.Join(resultErr, cleanup)
		if proof != nil && cleanup == nil {
			proof["cleanup"] = "rejected without stop or mutation; owned child waited; private root removed and absence verified"
		}
	}()
	row := bytes.TrimSuffix(f.supervisorRow(), []byte("}}}"))
	row = append(row, []byte(`,"ignored":[`)...)
	row = append(row, bytes.Repeat([]byte("0,"), (7<<20)/2)...)
	row = append(row, []byte("0]}}}\n")...)
	if err := f.write(f.log, row); err != nil {
		return nil, err
	}
	info, err := os.Stat(f.log)
	if err != nil {
		return nil, err
	}
	selected, err := harnessReportSelectedDescriptor(f.log, info)
	if err != nil {
		return nil, err
	}
	mutate := func(c *harnessReportChild) (map[string]any, error) {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			rows, err := observeHarnessReportDescriptors(ctx, c.command.Process.Pid, []string{f.log})
			if err != nil {
				return nil, err
			}
			if len(rows) == 1 && rows[0].Mode == "read" && rows[0].Offset == selected.Size {
				err := validateHarnessReportRead(rows, []harnessReportDescriptor{selected})
				if err == nil || !strings.Contains(err.Error(), "remaining selected bytes") {
					return nil, fmt.Errorf("late observation was not rejected: %v", err)
				}
				return map[string]any{"descriptors": rows, "selected": []harnessReportDescriptor{selected}, "rejected_observation": err.Error(), "rejected_before_stop": true, "mutation_performed": false}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.done:
				return nil, errors.New("late read endpoint was not observed; no mutation")
			case <-ticker.C:
			}
		}
	}
	r, proof, err := f.run(ctx, repo, "json", false, mutate)
	if err != nil {
		return proof, err
	}
	retained, err := os.ReadFile(f.log)
	if err != nil || !bytes.Equal(retained, row) || len(r.Sources.Supervisor) != 1 || r.Sources.Supervisor[0].Status != "complete" || r.Sources.Supervisor[0].Invalid != 0 || r.Builds.Rebuilds.Count != 1 {
		return proof, errors.New("late rejection changed input or valid report outcome")
	}
	proof["input_unchanged"], proof["sources"] = true, r.Sources
	return proof, nil
}
