package telemetryreport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/redact"
)

// failureStreakMinimum is the number of consecutive failed builds of one
// session that a report calls a streak: an agent editing against a runtime that
// stopped accepting every change.
const failureStreakMinimum = 5

// Builds aggregates the build requests recorded by development supervisors.
// Only a supervisor started with --detach keeps its event stream in a log, so
// foreground sessions are absent.
type Builds struct {
	Sessions  int              `json:"sessions"`
	Rebuilds  Timing           `json:"rebuilds"`
	Initial   Timing           `json:"initial_builds"`
	Worktrees []WorktreeBuilds `json:"worktrees"`
	Steps     []StepTiming     `json:"rebuild_steps"`
	Failures  []Count          `json:"failure_causes"`
	Streaks   []FailureStreak  `json:"failure_streaks"`
	logs      int
}

type WorktreeBuilds struct {
	AppRoot  string `json:"app_root"`
	AppName  string `json:"app_name"`
	Sessions int    `json:"sessions"`
	Rebuilds Timing `json:"rebuilds"`
	Initial  Timing `json:"initial_builds"`
	// LongestFailureStreak is the most consecutive failed builds of one session.
	LongestFailureStreak int `json:"longest_failure_streak"`
}

type StepTiming struct {
	Step string `json:"step"`
	Timing
}

// FailureStreak is a run of consecutive failed builds in one session.
type FailureStreak struct {
	AppRoot string `json:"app_root"`
	Cause   string `json:"cause"`
	Count   int    `json:"count"`
	First   string `json:"first"`
	Last    string `json:"last"`
}

type supervisorEvent struct {
	Data struct {
		Type string    `json:"type"`
		Time time.Time `json:"time"`
		App  struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"app"`
		Data json.RawMessage `json:"data"`
	} `json:"data"`
}

type buildStepData struct {
	OperationID string    `json:"operation_id"`
	Name        string    `json:"name"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  float64   `json:"duration_ms"`
	OK          bool      `json:"ok"`
	Reason      string    `json:"reason"`
}

type buildErrorData struct {
	Error      string `json:"error"`
	Diagnostic struct {
		Code string `json:"code"`
	} `json:"diagnostic"`
}

type worktreeAccumulator struct {
	name              string
	sessions          int
	rebuilds, initial timingAccumulator
	longest           int
}

// supervisorLogs lists the detached supervisor logs of a Scenery agent home:
// those of the machine agent and those of every worktree control plane.
func supervisorLogs(home string) ([]string, error) {
	var logs []string
	for _, pattern := range []string{
		filepath.Join(home, "agent", "dev", "*.log"),
		filepath.Join(home, "worktrees", "*", "control", "dev", "*.log"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		logs = append(logs, matches...)
	}
	sort.Strings(logs)
	return logs, nil
}

func readBuilds(opts Options) (Builds, error) {
	builds := Builds{Worktrees: []WorktreeBuilds{}, Steps: []StepTiming{}, Failures: []Count{}, Streaks: []FailureStreak{}}
	if opts.AgentHome == "" {
		return builds, nil
	}
	logs, err := supervisorLogs(opts.AgentHome)
	if err != nil {
		return Builds{}, err
	}
	rebuilds, initial := &timingAccumulator{}, &timingAccumulator{}
	steps := map[string]*timingAccumulator{}
	causes := map[string]int{}
	worktrees := map[string]*worktreeAccumulator{}
	for _, path := range logs {
		inWindow, err := readSupervisorLog(opts, path, func(root, name string) *worktreeAccumulator {
			acc := worktrees[root]
			if acc == nil {
				acc = &worktreeAccumulator{name: name}
				worktrees[root] = acc
			}
			return acc
		}, rebuilds, initial, steps, causes, &builds)
		if err != nil {
			return Builds{}, err
		}
		if inWindow {
			builds.logs++
		}
	}
	builds.Rebuilds, builds.Initial = rebuilds.timing(), initial.timing()
	for step, acc := range steps {
		builds.Steps = append(builds.Steps, StepTiming{Step: step, Timing: acc.timing()})
	}
	sort.Slice(builds.Steps, func(i, j int) bool {
		return builds.Steps[i].P50MS > builds.Steps[j].P50MS || builds.Steps[i].P50MS == builds.Steps[j].P50MS && builds.Steps[i].Step < builds.Steps[j].Step
	})
	builds.Failures = sortedCounts(causes, 15)
	for root, acc := range worktrees {
		builds.Worktrees = append(builds.Worktrees, WorktreeBuilds{
			AppRoot: root, AppName: acc.name, Sessions: acc.sessions,
			Rebuilds: acc.rebuilds.timing(), Initial: acc.initial.timing(), LongestFailureStreak: acc.longest,
		})
	}
	sort.Slice(builds.Worktrees, func(i, j int) bool {
		a, b := builds.Worktrees[i], builds.Worktrees[j]
		return a.Rebuilds.Count+a.Initial.Count > b.Rebuilds.Count+b.Initial.Count || a.Rebuilds.Count+a.Initial.Count == b.Rebuilds.Count+b.Initial.Count && a.AppRoot < b.AppRoot
	})
	sort.Slice(builds.Streaks, func(i, j int) bool { return builds.Streaks[i].Count > builds.Streaks[j].Count })
	return builds, nil
}

// readSupervisorLog folds one session log into the aggregates and reports
// whether the session had events in the window.
func readSupervisorLog(opts Options, path string, worktree func(root, name string) *worktreeAccumulator, rebuilds, initial *timingAccumulator, steps map[string]*timingAccumulator, causes map[string]int, builds *Builds) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read supervisor log: %w", err)
	}
	defer func() { _ = file.Close() }()
	type operation struct {
		steps map[string]float64
	}
	operations := map[string]*operation{}
	var acc *worktreeAccumulator
	var root string
	inWindow, started := false, false
	lastCause := ""
	var streak []time.Time
	streakCauses := map[string]int{}
	closeStreak := func() {
		if len(streak) >= failureStreakMinimum {
			cause := ""
			if top := sortedCounts(streakCauses, 1); len(top) == 1 {
				cause = top[0].Name
			}
			builds.Streaks = append(builds.Streaks, FailureStreak{AppRoot: root, Cause: cause, Count: len(streak),
				First: streak[0].UTC().Format(time.RFC3339), Last: streak[len(streak)-1].UTC().Format(time.RFC3339)})
		}
		if acc != nil && len(streak) > acc.longest {
			acc.longest = len(streak)
		}
		streak, streakCauses = nil, map[string]int{}
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		var event supervisorEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Data.Type == "" {
			continue
		}
		data := event.Data
		if acc == nil && data.App.Root != "" {
			root = data.App.Root
			acc = worktree(root, data.App.Name)
		}
		if acc == nil || !opts.inWindow(data.Time) {
			continue
		}
		inWindow = true
		if !started {
			started = true
			acc.sessions++
			builds.Sessions++
		}
		switch data.Type {
		case "build.error":
			var failure buildErrorData
			if json.Unmarshal(data.Data, &failure) == nil {
				lastCause = failureCause(failure.Error, failure.Diagnostic.Code)
				causes[lastCause]++
			}
		case "build.step":
			var step buildStepData
			if json.Unmarshal(data.Data, &step) != nil || step.OperationID == "" {
				continue
			}
			op := operations[step.OperationID]
			if op == nil {
				op = &operation{steps: map[string]float64{}}
				operations[step.OperationID] = op
			}
			if step.Name != "build.request" {
				op.steps[step.Name] += step.DurationMS
				continue
			}
			delete(operations, step.OperationID)
			duration := int64(step.DurationMS)
			switch step.Reason {
			case "initial_build":
				initial.add(duration, step.OK)
				acc.initial.add(duration, step.OK)
			default:
				rebuilds.add(duration, step.OK)
				acc.rebuilds.add(duration, step.OK)
				if step.OK {
					for name, ms := range op.steps {
						accumulate(steps, name, int64(ms), true)
					}
				}
			}
			if step.OK {
				closeStreak()
				continue
			}
			streak = append(streak, step.StartedAt)
			if lastCause != "" {
				streakCauses[lastCause]++
			}
		}
	}
	closeStreak()
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read supervisor log %s: %w", filepath.Base(path), err)
	}
	return inWindow, nil
}

var (
	causeDigest  = regexp.MustCompile(`sha256:[0-9a-f]{8,}`)
	causeHex     = regexp.MustCompile(`\b[0-9a-f]{12,}\b`)
	causeAddress = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	causePath    = regexp.MustCompile(`(?:^|[\s"'(=])(/[^\s"':)]+)`)
	causeNumber  = regexp.MustCompile(`\b\d{3,}\b`)
)

// failureCause groups a build failure by its diagnostic code and the stable
// part of its first line: digests, addresses, paths and long numbers vary
// between otherwise identical failures and are replaced. Credentials inside
// the message are redacted.
func failureCause(message, code string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(redact.String(message)), "\n")
	line = causeDigest.ReplaceAllString(line, "sha256:…")
	line = causeHex.ReplaceAllString(line, "…")
	line = causeAddress.ReplaceAllString(line, "<address>")
	line = causePath.ReplaceAllStringFunc(line, func(match string) string {
		prefix := ""
		if match != "" && match[0] != '/' {
			prefix = match[:1]
		}
		return prefix + "<path>"
	})
	line = causeNumber.ReplaceAllString(line, "N")
	if runes := []rune(line); len(runes) > 140 {
		line = string(runes[:140]) + "…"
	}
	if code != "" {
		return code + " " + line
	}
	if line == "" {
		return "unknown"
	}
	return line
}
