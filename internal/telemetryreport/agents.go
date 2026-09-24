package telemetryreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agents aggregates the sessions of coding agents that attempted Scenery at
// least once. Only command names, outcome classes and the inputs Scenery
// itself rejected are reported; no command text, output or file content is
// kept.
//
// A transcript shows the outcome of a shell command, not of the Scenery
// process inside it. Three things are therefore kept apart: an attempt, a
// Scenery command at command position of a shell command; the shell
// command's outcome, which is unknown when the transcript does not record
// it; and a Scenery command's own outcome, which the report takes only from
// a shell command that is one simple command running Scenery directly
// (attributable).
type Agents struct {
	ClaudeSessions int     `json:"claude_sessions"`
	CodexSessions  int     `json:"codex_sessions"`
	ToolCalls      int     `json:"tool_calls"`
	ToolErrors     int     `json:"tool_errors"`
	ToolErrorKinds []Count `json:"tool_error_kinds"`
	// SceneryInvocations counts attempted invocations, recognized or not.
	SceneryInvocations int `json:"scenery_invocations"`
	// SceneryCommands counts the shell commands that attempted Scenery;
	// SceneryOutcomeUnknown those whose outcome the transcript does not
	// record, and SceneryFailed those whose shell reported a failure, which
	// may be another command's: a pipe reports its last command's status.
	SceneryCommands       int `json:"scenery_commands"`
	SceneryOutcomeUnknown int `json:"scenery_outcome_unknown"`
	SceneryFailed         int `json:"scenery_failed"`
	// SceneryAttributable counts the shell commands whose recorded outcome
	// is Scenery's own, and SceneryAttributableFailed those that failed.
	SceneryAttributable       int            `json:"scenery_attributable"`
	SceneryAttributableFailed int            `json:"scenery_attributable_failed"`
	Commands                  []AgentCommand `json:"commands"`
	FailureClasses            []Count        `json:"failure_classes"`
	RejectedInputs            []Count        `json:"rejected_inputs"`
	sources                   TranscriptSources
}

// AgentCommand is one attempted Scenery command: a command `scenery help`
// advertises, "unknown <word>" for a word it does not, or a root flag such as
// "--help". Failures, waiting time and p50 cover only attributable shell
// commands: one simple command that ran Scenery directly, with a recorded
// outcome, whose exit status and duration are therefore the command's own.
type AgentCommand struct {
	Command      string `json:"command"`
	Count        int    `json:"count"`
	Attributable int    `json:"attributable"`
	FailureCount int    `json:"failure_count"`
	WallTimeMS   int64  `json:"wall_time_ms"`
	P50MS        *int64 `json:"p50_ms"`
}

// TranscriptSources tells how completely the transcripts were read, so that
// no failures found can be told apart from files that were not processed.
type TranscriptSources struct {
	// Read counts the files read to the end, Partial those whose reading
	// stopped at an error after some records, and Failed those that could
	// not be opened, including unreadable directories.
	Read    int `json:"read"`
	Partial int `json:"partial"`
	Failed  int `json:"failed"`
	// InvalidRecords counts tool records that did not decode, and
	// OversizedRecords lines longer than the reader keeps, which are skipped.
	InvalidRecords   int `json:"invalid_records"`
	OversizedRecords int `json:"oversized_records"`
	// UnmatchedResults counts tool results without their call, and
	// UnansweredCalls calls without their result, such as a call still
	// running when the transcript was read.
	UnmatchedResults int `json:"unmatched_results"`
	UnansweredCalls  int `json:"unanswered_calls"`
}

func (s *TranscriptSources) add(other TranscriptSources) {
	s.Read += other.Read
	s.Partial += other.Partial
	s.Failed += other.Failed
	s.InvalidRecords += other.InvalidRecords
	s.OversizedRecords += other.OversizedRecords
	s.UnmatchedResults += other.UnmatchedResults
	s.UnansweredCalls += other.UnansweredCalls
}

// transcriptLineLimit bounds the memory a transcript line may take; longer
// lines, such as embedded images, are skipped and counted.
const transcriptLineLimit = 8 << 20

// transcriptWorkers bounds the transcripts read at once.
const transcriptWorkers = 4

type outcome int

const (
	outcomeUnknown outcome = iota
	outcomeSucceeded
	outcomeFailed
)

// shellRun is one shell command an agent ran and what the transcript records
// of it.
type shellRun struct {
	command  string
	outcome  outcome
	exit     int // the reported exit status, -1 when none was reported
	duration time.Duration
	timed    bool
	output   string
}

// toolCall is one completed tool call of an agent session.
type toolCall struct {
	errored bool
	output  string
	at      time.Time
	shells  []shellRun
}

var (
	commandWord       = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	rejectedInput     = regexp.MustCompile(`unknown (command|flag|subcommand) "([^"]{1,40})"`)
	claudeExitCode    = regexp.MustCompile(`Exit code (\d+)`)
	claudeTimedOut    = regexp.MustCompile(`(?i)command timed out`)
	codexCommand      = regexp.MustCompile(`"?\bcmd"?\s*:\s*"((?:[^"\\]|\\.)*)"`)
	codexScriptError  = regexp.MustCompile(`^(Error|Script error|Script failed)|Uncaught|TypeError|SyntaxError|ReferenceError`)
	invalidInvocation = regexp.MustCompile(`SCN8001|invalid_request|unknown (command|flag|subcommand) "|flag provided but not defined`)
	internalFailure   = regexp.MustCompile(`SCN9\d{3}|internal tooling failure`)
	appDiagnostic     = regexp.MustCompile(`SCN[1-7]\d{3}`)
	notFound          = regexp.MustCompile(`(?i)command not found|no such file or directory`)
	timedOut          = regexp.MustCompile(`(?i)timed out|deadline exceeded`)
)

// sceneryFailureClass classifies a failed shell command by what Scenery or the
// shell reported.
func sceneryFailureClass(run shellRun) string {
	text := run.output
	switch {
	case invalidInvocation.MatchString(text):
		return "invalid invocation"
	case strings.Contains(text, "SCN8003") || strings.Contains(text, "failed_precondition"):
		return "failed precondition"
	case strings.Contains(text, "SCN8004") || strings.Contains(text, "capability_unavailable"):
		return "capability unavailable"
	case internalFailure.MatchString(text):
		return "internal failure"
	case appDiagnostic.MatchString(text):
		return "application diagnostic"
	case notFound.MatchString(text):
		return "executable not found"
	case timedOut.MatchString(text):
		return "timeout"
	}
	return "other failure"
}

var toolErrorKinds = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"agent script error", regexp.MustCompile(`Script error|Script failed|TypeError|SyntaxError|ReferenceError|Uncaught`)},
	{"shell command failed", regexp.MustCompile(`Exit code \d+`)},
	{"edit or write before reading", regexp.MustCompile(`has not been read yet|modified since read`)},
	{"edit target not found or not unique", regexp.MustCompile(`String to replace not found|Found \d+ matches`)},
	{"command timed out", regexp.MustCompile(`(?i)timed out`)},
	{"blocked foreground wait", regexp.MustCompile(`(?i)sleep`)},
	{"denied or not permitted", regexp.MustCompile(`(?i)doesn't want to proceed|rejected|permission|denied`)},
	{"tool input invalid", regexp.MustCompile(`InputValidationError|Invalid tool parameters`)},
	{"file not found", regexp.MustCompile(`(?i)does not exist|no such file`)},
	{"browser automation", regexp.MustCompile(`(?i)browser|screenshot|navigate|\btab\b`)},
}

func toolErrorKind(call toolCall) string {
	for _, kind := range toolErrorKinds {
		if kind.pattern.MatchString(call.output) {
			return kind.kind
		}
	}
	for _, run := range call.shells {
		if run.outcome == outcomeFailed {
			return "shell command failed"
		}
	}
	return "other"
}

type commandTally struct {
	count, attributable, failures int
	wall                          int64
	durations                     []int64
}

// agentTally accumulates the tool calls of one transcript and, merged, of
// all of them. It holds counters and per-command durations only; no call is
// retained once it is counted.
type agentTally struct {
	claudeSessions, codexSessions  int
	toolCalls, toolErrors          int
	invocations, commands          int
	failed, unknown                int
	attributable, attributableFail int
	kinds, classes, rejected       map[string]int
	perCommand                     map[string]*commandTally
	attemptedScenery               bool
	sources                        TranscriptSources
}

func newAgentTally() *agentTally {
	return &agentTally{kinds: map[string]int{}, classes: map[string]int{}, rejected: map[string]int{}, perCommand: map[string]*commandTally{}}
}

// visit counts one tool call; calls outside the window only tell whether the
// session attempted Scenery.
func (t *agentTally) visit(opts Options, call toolCall) {
	attempts := make([][]string, len(call.shells))
	attributable := make([]bool, len(call.shells))
	for index, run := range call.shells {
		attempts[index], attributable[index] = sceneryAttempts(run.command, opts.CommandFamilies)
		if len(attempts[index]) > 0 {
			t.attemptedScenery = true
		}
	}
	if !opts.inWindow(call.at) {
		return
	}
	t.toolCalls++
	if call.errored {
		t.toolErrors++
		t.kinds[toolErrorKind(call)]++
	}
	for index, run := range call.shells {
		invoked := attempts[index]
		if len(invoked) == 0 {
			continue
		}
		t.commands++
		t.invocations += len(invoked)
		for _, match := range rejectedInput.FindAllStringSubmatch(run.output, -1) {
			t.rejected["unknown "+match[1]+" \""+match[2]+"\""]++
		}
		switch run.outcome {
		case outcomeUnknown:
			t.unknown++
		case outcomeFailed:
			t.failed++
			t.classes[sceneryFailureClass(run)]++
		}
		for _, command := range invoked {
			tally := t.perCommand[command]
			if tally == nil {
				tally = &commandTally{}
				t.perCommand[command] = tally
			}
			tally.count++
			if !attributable[index] || run.outcome == outcomeUnknown {
				continue
			}
			tally.attributable++
			t.attributable++
			if run.outcome == outcomeFailed {
				tally.failures++
				t.attributableFail++
			}
			if run.timed {
				tally.wall += run.duration.Milliseconds()
				tally.durations = append(tally.durations, run.duration.Milliseconds())
			}
		}
	}
}

// merge adds a transcript's tally; a session that never attempted Scenery
// adds only what its reading covered.
func (t *agentTally) merge(other *agentTally, agent string) {
	t.sources.add(other.sources)
	if !other.attemptedScenery {
		return
	}
	switch agent {
	case "claude":
		t.claudeSessions++
	case "codex":
		t.codexSessions++
	}
	t.toolCalls += other.toolCalls
	t.toolErrors += other.toolErrors
	t.invocations += other.invocations
	t.commands += other.commands
	t.failed += other.failed
	t.unknown += other.unknown
	t.attributable += other.attributable
	t.attributableFail += other.attributableFail
	for _, pair := range []struct{ into, from map[string]int }{{t.kinds, other.kinds}, {t.classes, other.classes}, {t.rejected, other.rejected}} {
		for name, count := range pair.from {
			pair.into[name] += count
		}
	}
	for command, from := range other.perCommand {
		into := t.perCommand[command]
		if into == nil {
			into = &commandTally{}
			t.perCommand[command] = into
		}
		into.count += from.count
		into.attributable += from.attributable
		into.failures += from.failures
		into.wall += from.wall
		into.durations = append(into.durations, from.durations...)
	}
}

func readAgents(opts Options) (Agents, error) {
	total := newAgentTally()
	for _, source := range []struct {
		root  string
		agent string
		read  transcriptReader
	}{
		{opts.ClaudeProjectsDir, "claude", readClaudeTranscript},
		{opts.CodexSessionsDir, "codex", readCodexTranscript},
	} {
		if source.root == "" {
			continue
		}
		if err := readTranscripts(opts, source.root, source.agent, source.read, total); err != nil {
			return Agents{}, err
		}
	}
	agents := Agents{
		ClaudeSessions: total.claudeSessions, CodexSessions: total.codexSessions,
		ToolCalls: total.toolCalls, ToolErrors: total.toolErrors,
		SceneryInvocations: total.invocations, SceneryCommands: total.commands,
		SceneryOutcomeUnknown: total.unknown, SceneryFailed: total.failed,
		SceneryAttributable: total.attributable, SceneryAttributableFailed: total.attributableFail,
		Commands: []AgentCommand{}, sources: total.sources,
	}
	for command, tally := range total.perCommand {
		agents.Commands = append(agents.Commands, AgentCommand{Command: command, Count: tally.count, Attributable: tally.attributable, FailureCount: tally.failures, WallTimeMS: tally.wall, P50MS: percentiles(tally.durations, 50)[0]})
	}
	sort.Slice(agents.Commands, func(i, j int) bool {
		return agents.Commands[i].Count > agents.Commands[j].Count || agents.Commands[i].Count == agents.Commands[j].Count && agents.Commands[i].Command < agents.Commands[j].Command
	})
	agents.ToolErrorKinds = sortedCounts(total.kinds, 0)
	agents.FailureClasses = sortedCounts(total.classes, 0)
	agents.RejectedInputs = sortedCounts(total.rejected, 20)
	return agents, nil
}

// transcriptReader reads one transcript, passing each completed tool call to
// visit as soon as it is paired and recording what it could not read.
type transcriptReader func(file io.Reader, visit func(toolCall), sources *TranscriptSources) error

// readTranscripts streams every transcript under root modified inside the
// window through a few workers, each folding one file into a compact tally
// that is merged as it completes.
func readTranscripts(opts Options, root, agent string, read transcriptReader, total *agentTally) error {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if errors.Is(err, os.ErrPermission) {
				total.sources.Failed++
				return nil
			}
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info, err := entry.Info(); err != nil || (!opts.Since.IsZero() && info.ModTime().Before(opts.Since)) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	work := make(chan string)
	tallies := make(chan *agentTally, transcriptWorkers)
	var wg sync.WaitGroup
	for range min(transcriptWorkers, max(len(paths), 1)) {
		wg.Go(func() {
			for path := range work {
				tallies <- readTranscript(opts, path, read)
			}
		})
	}
	go func() {
		for _, path := range paths {
			work <- path
		}
		close(work)
		wg.Wait()
		close(tallies)
	}()
	for tally := range tallies {
		total.merge(tally, agent)
	}
	return nil
}

func readTranscript(opts Options, path string, read transcriptReader) *agentTally {
	tally := newAgentTally()
	file, err := os.Open(path)
	if err != nil {
		tally.sources.Failed++
		return tally
	}
	defer func() { _ = file.Close() }()
	if err := read(file, func(call toolCall) { tally.visit(opts, call) }, &tally.sources); err != nil {
		// What was read before the error still counts.
		tally.sources.Partial++
		return tally
	}
	tally.sources.Read++
	return tally
}

func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			texts = append(texts, part.Text)
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func limitText(text string) string {
	if len(text) > 4096 {
		return text[:4096]
	}
	return text
}

// readClaudeTranscript pairs each tool use of a Claude Code transcript with its
// result. A Bash result records the shell's exit status only as an error's
// "Exit code N"; a command sent to the background, an interrupted one, or an
// error without an exit status, such as a denied command, has no known
// outcome.
func readClaudeTranscript(file io.Reader, visit func(toolCall), sources *TranscriptSources) error {
	type use struct {
		command    string
		background bool
		at         time.Time
	}
	uses := map[string]use{}
	oversized, err := readLines(file, transcriptLineLimit, func(raw []byte) {
		// Only tool uses and their results are decoded.
		if !bytes.Contains(raw, []byte(`"tool_use"`)) && !bytes.Contains(raw, []byte(`"tool_result"`)) {
			return
		}
		var line struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
		}
		if json.Unmarshal(raw, &line) != nil {
			sources.InvalidRecords++
			return
		}
		if line.Type != "assistant" && line.Type != "user" {
			return
		}
		var items []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(line.Message.Content, &items) != nil {
			// A string message content carries no tool records.
			return
		}
		var result struct {
			Interrupted bool `json:"interrupted"`
		}
		_ = json.Unmarshal(line.ToolUseResult, &result)
		for _, item := range items {
			switch item.Type {
			case "tool_use":
				var input struct {
					Command         string `json:"command"`
					RunInBackground bool   `json:"run_in_background"`
				}
				_ = json.Unmarshal(item.Input, &input)
				started := use{at: line.Timestamp}
				if item.Name == "Bash" {
					started.command, started.background = input.Command, input.RunInBackground
				}
				uses[item.ID] = started
			case "tool_result":
				started, ok := uses[item.ToolUseID]
				if !ok {
					sources.UnmatchedResults++
					continue
				}
				delete(uses, item.ToolUseID)
				output := limitText(contentText(item.Content))
				call := toolCall{errored: item.IsError, output: output, at: started.at}
				if started.command != "" {
					run := shellRun{command: started.command, exit: -1, output: output, duration: line.Timestamp.Sub(started.at), timed: !started.at.IsZero() && !line.Timestamp.IsZero()}
					switch match := claudeExitCode.FindStringSubmatch(output); {
					case started.background || result.Interrupted:
						run.timed = false
					case !item.IsError:
						run.outcome, run.exit = outcomeSucceeded, 0
					case match != nil:
						run.outcome = outcomeFailed
						run.exit, _ = strconv.Atoi(match[1])
					case claudeTimedOut.MatchString(output):
						run.outcome = outcomeFailed
					default:
						run.timed = false
					}
					call.shells = []shellRun{run}
				}
				visit(call)
			}
		}
	})
	sources.OversizedRecords += oversized
	sources.UnansweredCalls += len(uses)
	return err
}

// codexRun is what a Codex tool output records of one shell command: its exit
// status, when known, and its output. Codex records no command duration: the
// wall times it reports time the tool's wait for an output chunk, which reads
// 0.0000 seconds for a command that had already finished.
type codexRun struct {
	exit  int
	known bool
	text  string
}

// readCodexTranscript pairs each tool call of a Codex rollout with its output.
// exec_command and shell_command outputs begin with a header naming the
// command's exit status. An exec script reports each exec_command it printed
// as a JSON object; its commands are attributed their results only when the
// script names as many commands as it printed results. Codex commands add
// outcomes, never timings.
func readCodexTranscript(file io.Reader, visit func(toolCall), sources *TranscriptSources) error {
	type pending struct {
		commands []string
		at       time.Time
	}
	calls := map[string]pending{}
	oversized, err := readLines(file, transcriptLineLimit, func(raw []byte) {
		// Only tool calls and their outputs are decoded.
		if !bytes.Contains(raw, []byte(`call`)) {
			return
		}
		var line struct {
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type      string          `json:"type"`
				Name      string          `json:"name"`
				CallID    string          `json:"call_id"`
				Input     string          `json:"input"`
				Arguments string          `json:"arguments"`
				Output    json.RawMessage `json:"output"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &line) != nil {
			sources.InvalidRecords++
			return
		}
		payload := line.Payload
		switch payload.Type {
		case "custom_tool_call":
			calls[payload.CallID] = pending{commands: codexScriptCommands(payload.Input), at: line.Timestamp}
		case "function_call":
			calls[payload.CallID] = pending{commands: codexFunctionCommands(payload.Name, payload.Arguments), at: line.Timestamp}
		case "custom_tool_call_output", "function_call_output":
			started, ok := calls[payload.CallID]
			if !ok {
				sources.UnmatchedResults++
				return
			}
			delete(calls, payload.CallID)
			output := contentText(payload.Output)
			head := output
			if len(head) > 300 {
				head = head[:300]
			}
			call := toolCall{errored: codexScriptError.MatchString(head), output: limitText(output), at: started.at}
			var runs []codexRun
			if payload.Type == "function_call_output" {
				runs = []codexRun{codexHeaderRun(output)}
			} else {
				runs = codexScriptRuns(output)
			}
			// Without one result per command, no command has a known
			// outcome.
			if len(runs) != len(started.commands) {
				runs = make([]codexRun, len(started.commands))
				for index := range runs {
					runs[index].text = output
				}
			}
			for index, command := range started.commands {
				result := runs[index]
				run := shellRun{command: command, exit: -1, output: limitText(result.text)}
				if result.known {
					run.exit = result.exit
					run.outcome = outcomeSucceeded
					if result.exit != 0 {
						run.outcome = outcomeFailed
						call.errored = true
					}
				}
				call.shells = append(call.shells, run)
			}
			visit(call)
		}
	})
	sources.OversizedRecords += oversized
	sources.UnansweredCalls += len(calls)
	return err
}

// codexScriptCommands returns the literal commands an exec script passes to
// exec_command.
func codexScriptCommands(source string) []string {
	var commands []string
	for _, match := range codexCommand.FindAllStringSubmatch(source, -1) {
		if unquoted, err := strconv.Unquote(`"` + match[1] + `"`); err == nil {
			commands = append(commands, unquoted)
		} else {
			commands = append(commands, match[1])
		}
	}
	return commands
}

// codexFunctionCommands returns the shell command of an exec_command,
// shell_command or shell function call.
func codexFunctionCommands(name, arguments string) []string {
	var args struct {
		Cmd     json.RawMessage `json:"cmd"`
		Command json.RawMessage `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}
	var raw json.RawMessage
	switch name {
	case "exec_command":
		raw = args.Cmd
	case "shell_command", "shell":
		raw = args.Command
	default:
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return []string{text}
	}
	var words []string
	if json.Unmarshal(raw, &words) != nil || len(words) == 0 {
		return nil
	}
	// A shell started with -c runs its script argument.
	if len(words) == 3 && strings.HasPrefix(words[1], "-") && strings.HasSuffix(words[1], "c") {
		switch filepath.Base(words[0]) {
		case "sh", "bash", "zsh":
			return []string{words[2]}
		}
	}
	return []string{strings.Join(words, " ")}
}

var codexHeaderExit = regexp.MustCompile(`^(?:Process exited with code|Exit code:) (-?\d+)$`)

// codexHeaderRun reads the header Codex writes before a command's output, up
// to its "Output:" line; a command still running reports no exit status. An
// older shell call's output is a JSON object whose metadata carries it.
func codexHeaderRun(output string) codexRun {
	run := codexRun{text: output}
	var legacy struct {
		Output   string `json:"output"`
		Metadata *struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if strings.HasPrefix(output, "{") && json.Unmarshal([]byte(output), &legacy) == nil && legacy.Metadata != nil {
		run.text = legacy.Output
		if legacy.Metadata.ExitCode != nil {
			run.exit, run.known = *legacy.Metadata.ExitCode, true
		}
		return run
	}
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "Output:" {
			break
		}
		if match := codexHeaderExit.FindStringSubmatch(line); match != nil {
			run.exit, _ = strconv.Atoi(match[1])
			run.known = true
		}
	}
	return run
}

// codexScriptRuns reads the exec_command results an exec script printed: JSON
// objects, one per line, that carry a chunk_id, directly or as the value of a
// settled promise. Fields are read by name, in any order; a result without an
// exit_code, such as a command still running, has no known outcome.
func codexScriptRuns(output string) []codexRun {
	var runs []codexRun
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"chunk_id"`) {
			continue
		}
		type result struct {
			ChunkID  *string `json:"chunk_id"`
			ExitCode *int    `json:"exit_code"`
			Output   string  `json:"output"`
		}
		var object struct {
			result
			Value *result `json:"value"`
		}
		if json.Unmarshal([]byte(line), &object) != nil {
			continue
		}
		found := object.result
		if object.Value != nil && object.Value.ChunkID != nil {
			found = *object.Value
		}
		if found.ChunkID == nil {
			continue
		}
		run := codexRun{text: found.Output}
		if found.ExitCode != nil {
			run.exit, run.known = *found.ExitCode, true
		}
		runs = append(runs, run)
	}
	return runs
}
