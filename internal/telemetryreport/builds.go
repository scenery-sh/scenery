package telemetryreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/devtelemetry"
	"scenery.sh/internal/redact"
	"scenery.sh/internal/rotatinglog"
)

// failureStreakMinimum is the number of consecutive failed builds of one
// session that a report calls a streak: an agent editing against a runtime that
// stopped accepting every change.
const failureStreakMinimum = 5

// Builds aggregates the build requests recorded by development supervisors.
// Only a supervisor started with --detach keeps its event stream in a log, so
// foreground sessions are absent.
type Builds struct {
	StartupPhases          []StepTiming `json:"startup_phases"`
	PhaseBoundary          string       `json:"phase_boundary"`
	ObservabilityStates    []Count      `json:"observability_states"`
	CacheWork              []CacheWork  `json:"cache_work"`
	phaseSamples           map[string]*timingAccumulator
	observabilityStates    map[string]int
	cacheWork              map[string]*CacheWork
	HeaderObservations     []FirstHeaderObservation `json:"first_header_observations"`
	HeaderObservationCount int                      `json:"first_header_observation_count"`
	TransactionWaits       int                      `json:"transaction_waits"`
	DeferredCandidates     int                      `json:"deferred_candidates"`
	InitialFailures        []Count                  `json:"initial_failure_causes"`
	RebuildFailures        []Count                  `json:"rebuild_failure_causes"`
	UnstartedErrors        []Count                  `json:"unstarted_error_causes"`
	initialCauses          map[string]int
	rebuildCauses          map[string]int
	unstartedCauses        map[string]int
	FirstResponse          Timing `json:"observed_change_to_first_response"`
	firstResponses         timingAccumulator
	Superseded             int              `json:"superseded"`
	Blocks                 []BuildBlock     `json:"blocks"`
	ActiveBlocks           []BuildBlock     `json:"active_blocks"`
	PreventedBuilds        int              `json:"prevented_builds"`
	Sessions               int              `json:"sessions"`
	Rebuilds               Timing           `json:"rebuilds"`
	Initial                Timing           `json:"initial_builds"`
	Worktrees              []WorktreeBuilds `json:"worktrees"`
	Steps                  []StepTiming     `json:"rebuild_steps"`
	// Failures has one cause for each failed initial build or rebuild; the
	// least frequent are summed as "other causes".
	Failures []Count `json:"failure_causes"`
	// UnmatchedErrors counts build errors that name an operation no build
	// request in the window has; they are charged to no build.
	UnmatchedErrors int             `json:"unmatched_errors"`
	Streaks         []FailureStreak `json:"failure_streaks"`
	rotatedLogs     int
	logs            int
	partialLogs     int
	failedLogs      int
	invalidRecords  int
	coverage        []SupervisorFileCoverage
}

type FirstHeaderObservation struct {
	AppRoot string `json:"app_root"`
	devtelemetry.FirstResponse
	DurationMS int64 `json:"duration_ms"`
}

type SupervisorFileCoverage struct {
	Path          string `json:"path"`
	Status        string `json:"status"`
	SnapshotBytes *int64 `json:"snapshot_bytes"`
	ReadBytes     int64  `json:"read_bytes"`
	Segments      int    `json:"segments"`
	Records       int    `json:"records"`
	InWindow      int    `json:"in_window"`
	Invalid       int    `json:"invalid"`
	First         string `json:"first,omitempty"`
	Last          string `json:"last,omitempty"`
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
	Cache                    string          `json:"cache"`
	EditClass                string          `json:"edit_class"`
	FilesHashed              int             `json:"files_hashed"`
	BytesHashed              int64           `json:"bytes_hashed"`
	FilesReused              int             `json:"files_reused"`
	BytesReused              int64           `json:"bytes_reused"`
	FilesWritten             int             `json:"files_written"`
	BytesWritten             int64           `json:"bytes_written"`
	CacheHits                int             `json:"cache_hits"`
	CacheMisses              int             `json:"cache_misses"`
	PackagesRebuilt          []string        `json:"packages_rebuilt"`
	PackagesRebuiltAvailable bool            `json:"packages_rebuilt_available"`
	ExecutableBytes          int64           `json:"executable_bytes"`
	Outcome                  string          `json:"outcome"`
	OperationID              string          `json:"operation_id"`
	Name                     string          `json:"name"`
	StartedAt                time.Time       `json:"started_at"`
	DurationMS               json.RawMessage `json:"duration_ms"`
	OK                       bool            `json:"ok"`
	Reason                   string          `json:"reason"`
}

type buildErrorData struct {
	OperationID string          `json:"operation_id"`
	Error       json.RawMessage `json:"error"`
	Diagnostic  struct {
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
	builds := Builds{StartupPhases: []StepTiming{}, ObservabilityStates: []Count{}, CacheWork: []CacheWork{}, phaseSamples: map[string]*timingAccumulator{}, observabilityStates: map[string]int{}, cacheWork: map[string]*CacheWork{}, PhaseBoundary: "independent wall intervals; overlaps are not summed and dependency/parent edges are unavailable", Worktrees: []WorktreeBuilds{}, Steps: []StepTiming{}, Failures: []Count{}, Streaks: []FailureStreak{}, Blocks: []BuildBlock{}, ActiveBlocks: []BuildBlock{}, InitialFailures: []Count{}, RebuildFailures: []Count{}, UnstartedErrors: []Count{}, initialCauses: map[string]int{}, rebuildCauses: map[string]int{}, unstartedCauses: map[string]int{}}
	builds.HeaderObservations, builds.coverage = []FirstHeaderObservation{}, []SupervisorFileCoverage{}
	blocks := map[string]*BuildBlock{}
	if opts.AgentHome == "" {
		finalizeBlocks(&builds, blocks, opts)
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
		}, rebuilds, initial, steps, causes, &builds, blocks)
		if err != nil {
			// A log that cannot be opened, or stops reading part way, is
			// counted; what it held before the error still counts.
			if errors.Is(err, errSupervisorLogOpen) {
				builds.failedLogs++
				continue
			}
			builds.partialLogs++
		}
		if inWindow {
			builds.logs++
		}
	}
	finalizePhaseAndCacheWork(&builds)
	builds.Rebuilds, builds.Initial = rebuilds.timing(), initial.timing()
	for step, acc := range steps {
		builds.Steps = append(builds.Steps, StepTiming{Step: step, Timing: acc.timing()})
	}
	sort.Slice(builds.Steps, func(i, j int) bool {
		return ms(builds.Steps[i].P50MS) > ms(builds.Steps[j].P50MS) || ms(builds.Steps[i].P50MS) == ms(builds.Steps[j].P50MS) && builds.Steps[i].Step < builds.Steps[j].Step
	})
	builds.Failures = sortedCounts(causes, 0)
	builds.InitialFailures = sortedCounts(builds.initialCauses, 0)
	builds.RebuildFailures = sortedCounts(builds.rebuildCauses, 0)
	builds.UnstartedErrors = sortedCounts(builds.unstartedCauses, 0)
	// The fifteen most frequent causes are named; the rest are summed, so
	// the list still adds up to the failed builds.
	if len(builds.Failures) > 15 {
		rest := Count{Name: "other causes"}
		for _, cause := range builds.Failures[14:] {
			rest.Count += cause.Count
		}
		builds.Failures = append(builds.Failures[:14], rest)
	}
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
	finalizeBlocks(&builds, blocks, opts)
	builds.FirstResponse = builds.firstResponses.timing()
	return builds, nil
}

// supervisorLineLimit bounds one supervisor log line; longer lines are
// skipped and counted.
const supervisorLineLimit = 8 << 20

var errSupervisorLogOpen = errors.New("open supervisor log")

// readSupervisorLog folds one session log into the aggregates and reports
// whether the session had events in the window. A read error after some
// events is returned with what was folded before it.
//
// A supervisor writes a build's build.error after that build's build.request
// step. An error names its operation (operation_id) since the event carries
// it; an older log's error, which names none, belongs to the failed build it
// immediately follows. An error naming an operation that no build request in
// the window has is unmatched and charged to no build. Each failed build has
// exactly one cause, its first error, so the causes add up to the failed
// builds.
func readSupervisorLog(opts Options, path string, worktree func(root, name string) *worktreeAccumulator, rebuilds, initial *timingAccumulator, steps map[string]*timingAccumulator, causes map[string]int, builds *Builds, blocks map[string]*BuildBlock) (bool, error) {
	coverage := SupervisorFileCoverage{Path: path, Status: "complete"}
	var first, last time.Time
	defer func() { builds.coverage = append(builds.coverage, coverage) }()
	var readers []io.Reader
	var files []*os.File
	var snapshots []*snapshotReader
	var infos []os.FileInfo
	var snapshotBytes int64
	var coverageErr error
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for _, segment := range rotatinglog.Segments(path) {
		file, err := os.Open(segment)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			coverage.Status = "unreadable"
			coverage.SnapshotBytes = nil
			return false, fmt.Errorf("%w %s: %w", errSupervisorLogOpen, filepath.Base(segment), err)
		}
		snapshot, info, err := captureSnapshot(file)
		if err != nil {
			coverage.Status = "partial"
			coverage.SnapshotBytes = nil
			_ = file.Close()
			return false, err
		}
		duplicate := false
		for _, previous := range infos {
			if os.SameFile(previous, info) {
				duplicate = true
				break
			}
		}
		if duplicate {
			_ = file.Close()
			coverage.Status = "partial"
			coverageErr = errors.New("duplicate supervisor segment identity")
			continue
		}
		files = append(files, file)
		infos = append(infos, info)
		var valid bool
		snapshotBytes, valid = addReadBytes(snapshotBytes, snapshot.size)
		if !valid {
			coverage.Status, coverage.SnapshotBytes = "partial", nil
			return false, errors.New("supervisor captured byte total is unrepresentable")
		}
		coverage.SnapshotBytes = &snapshotBytes
		coverage.Segments++
		snapshots = append(snapshots, snapshot)
		readers = append(readers, snapshot)
	}
	if len(files) == 0 {
		coverage.Status = "missing"
		return false, nil
	}
	if len(files) > 1 {
		builds.rotatedLogs++
	}
	file := io.MultiReader(readers...)

	type outcome struct {
		at      time.Time
		ok      bool
		cause   string
		initial bool
	}
	var outcomes []outcome
	byOperation := map[string]int{}
	awaiting := -1 // the failed build whose error has not arrived yet
	operationSteps := map[string]map[string]*supervisorTiming{}
	invalidRecord := func() {
		builds.invalidRecords++
		coverage.Invalid++
	}
	var acc *worktreeAccumulator
	var root string
	inWindow := false
	oversized, readErr := readLines(file, supervisorLineLimit, func(line []byte) {
		// The log also holds the plain output of the processes it runs.
		if len(line) == 0 || line[0] != '{' {
			return
		}
		var event supervisorEvent
		if json.Unmarshal(line, &event) != nil {
			invalidRecord()
			return
		}
		if event.Data.Type == "" {
			return
		}
		data := event.Data
		coverage.Records++
		if !data.Time.IsZero() {
			if first.IsZero() || data.Time.Before(first) {
				first = data.Time
				coverage.First = data.Time.UTC().Format(time.RFC3339Nano)
			}
			if data.Time.After(last) {
				last = data.Time
				coverage.Last = data.Time.UTC().Format(time.RFC3339Nano)
			}
		}
		if acc == nil && data.App.Root != "" {
			root = data.App.Root
			acc = worktree(root, data.App.Name)
		}
		if acc == nil || !opts.inWindow(data.Time) {
			return
		}
		inWindow = true
		coverage.InWindow++
		switch data.Type {
		case "phase.finish":
			var phase struct {
				ID         string          `json:"phase_id"`
				Title      string          `json:"title"`
				DurationMS json.RawMessage `json:"duration_ms"`
				OK         bool            `json:"ok"`
			}
			if json.Unmarshal(data.Data, &phase) != nil {
				invalidRecord()
				return
			}
			id := phase.ID
			if id == "" {
				id = strings.Join(strings.Fields(strings.ToLower(phase.Title)), "-")
			}
			if id == "" {
				invalidRecord()
				return
			}
			if builds.phaseSamples[id] == nil {
				builds.phaseSamples[id] = &timingAccumulator{}
			}
			timing := decodeSupervisorTiming(phase.DurationMS)
			if timing.invalid {
				invalidRecord()
			}
			timing.record(builds.phaseSamples[id], phase.OK)
		case "observability.health":
			var health struct {
				State string `json:"state"`
			}
			if json.Unmarshal(data.Data, &health) == nil && health.State != "" {
				builds.observabilityStates[health.State]++
			}
		case "source.lost":
			builds.unstartedCauses["source lost; runtime stopped and data retained"]++
		case "build.deferred":
			builds.TransactionWaits++
		case "process.output":
			var output struct {
				Source string `json:"source"`
				Output string `json:"output"`
			}
			if json.Unmarshal(data.Data, &output) != nil || output.Source != "host" {
				return
			}
			for line := range strings.SplitSeq(output.Output, "\n") {
				raw, ok := strings.CutPrefix(line, devtelemetry.FirstResponsePrefix)
				if !ok {
					continue
				}
				var response devtelemetry.FirstResponse
				if json.Unmarshal([]byte(raw), &response) != nil || response.OperationID == "" || response.ObservedAt.IsZero() || response.At.Before(response.ObservedAt) || response.BuildInputDigest == "" || response.Status < 200 || response.Status >= 500 {
					continue
				}
				duration := response.At.Sub(response.ObservedAt).Milliseconds()
				builds.HeaderObservationCount++
				builds.HeaderObservations = append(builds.HeaderObservations, FirstHeaderObservation{AppRoot: root, FirstResponse: response, DurationMS: duration})
				sort.Slice(builds.HeaderObservations, func(i, j int) bool { return builds.HeaderObservations[i].At.Before(builds.HeaderObservations[j].At) })
				if len(builds.HeaderObservations) > 200 {
					builds.HeaderObservations = builds.HeaderObservations[1:]
				}
				if !response.Initial {
					builds.firstResponses.add(duration, true)
				}
			}

		case "build.blocked", "build.unblocked":
			recordBlockEvent(blocks, root, data.Type, data.Time, data.Data)
		case "build.error":
			var failure buildErrorData
			if json.Unmarshal(data.Data, &failure) != nil || (len(failure.Error) == 0 && failure.Diagnostic.Code == "") {
				invalidRecord()
				return
			}
			// A missing legacy message can retain a diagnostic code. Explicit
			// null or another JSON kind must not consume a pending error join.
			message := ""
			if len(failure.Error) != 0 {
				var decoded *string
				if json.Unmarshal(failure.Error, &decoded) != nil || decoded == nil {
					invalidRecord()
					return
				}
				message = *decoded
			}
			index := awaiting
			if failure.OperationID != "" {
				named, ok := byOperation[failure.OperationID]
				if !ok {
					builds.UnmatchedErrors++
					return
				}
				index = named
			}
			if index >= 0 && !outcomes[index].ok && outcomes[index].cause == "" {
				outcomes[index].cause = failureCause(message, failure.Diagnostic.Code)
			}
			if index < 0 && failure.OperationID == "" {
				builds.unstartedCauses[failureCause(message, failure.Diagnostic.Code)]++
			}
			if index == awaiting {
				awaiting = -1
			}
		case "build.step":
			var step buildStepData
			if json.Unmarshal(data.Data, &step) != nil || step.OperationID == "" {
				invalidRecord()
				return
			}
			timing := decodeSupervisorTiming(step.DurationMS)
			invalid := timing.invalid
			if !recordCacheWork(builds, step, timing) {
				invalid = true
			}
			if step.Name != "build.request" {
				if operationSteps[step.OperationID] == nil {
					operationSteps[step.OperationID] = map[string]*supervisorTiming{}
				}
				if operationSteps[step.OperationID][step.Name] == nil {
					operationSteps[step.OperationID][step.Name] = &supervisorTiming{}
				}
				if !operationSteps[step.OperationID][step.Name].add(timing) {
					invalid = true
				}
				if invalid {
					invalidRecord()
				}
				return
			}
			if invalid {
				invalidRecord()
			}
			stepDurations := operationSteps[step.OperationID]
			delete(operationSteps, step.OperationID)
			if step.Outcome == "superseded" {
				builds.Superseded++
				outcomes = append(outcomes, outcome{at: step.StartedAt, ok: true})
				awaiting = -1
				return
			}
			if step.Outcome == "deferred" {
				builds.DeferredCandidates++
				awaiting = -1
				return
			}
			switch step.Reason {
			case "initial_build":
				timing.record(initial, step.OK)
				timing.record(&acc.initial, step.OK)
			default:
				timing.record(rebuilds, step.OK)
				timing.record(&acc.rebuilds, step.OK)
				if step.OK {
					for name, childTiming := range stepDurations {
						if steps[name] == nil {
							steps[name] = &timingAccumulator{}
						}
						childTiming.record(steps[name], true)
					}
				}
			}
			outcomes = append(outcomes, outcome{at: step.StartedAt, ok: step.OK, initial: step.Reason == "initial_build"})
			byOperation[step.OperationID] = len(outcomes) - 1
			awaiting = -1
			if !step.OK {
				awaiting = len(outcomes) - 1
			}
		}
	})
	for _, snapshot := range snapshots {
		var valid bool
		coverage.ReadBytes, valid = addReadBytes(coverage.ReadBytes, snapshot.readBytes)
		if !valid {
			coverage.Status = "partial"
			return inWindow, errors.New("supervisor read byte total is unrepresentable")
		}
	}
	readErr = errors.Join(readErr, coverageErr)
	builds.invalidRecords += oversized
	coverage.Invalid += oversized
	if coverage.Invalid > 0 {
		coverage.Status = "partial"
	}
	if readErr != nil {
		coverage.Status = "partial"
		readErr = fmt.Errorf("read supervisor log %s: %w", filepath.Base(path), readErr)
	}
	if inWindow {
		acc.sessions++
		builds.Sessions++
	}
	var streak []outcome
	closeStreak := func() {
		if len(streak) >= failureStreakMinimum {
			streakCauses := map[string]int{}
			for _, failed := range streak {
				streakCauses[failed.cause]++
			}
			builds.Streaks = append(builds.Streaks, FailureStreak{AppRoot: root, Cause: sortedCounts(streakCauses, 1)[0].Name, Count: len(streak),
				First: streak[0].at.UTC().Format(time.RFC3339), Last: streak[len(streak)-1].at.UTC().Format(time.RFC3339)})
		}
		if acc != nil && len(streak) > acc.longest {
			acc.longest = len(streak)
		}
		streak = nil
	}
	for _, build := range outcomes {
		if build.ok {
			closeStreak()
			continue
		}
		if build.cause == "" {
			build.cause = "no error recorded"
		}
		causes[build.cause]++
		if build.initial {
			builds.initialCauses[build.cause]++
		} else {
			builds.rebuildCauses[build.cause]++
		}
		streak = append(streak, build)
	}
	closeStreak()
	return inWindow, readErr
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
