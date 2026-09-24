package telemetryreport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
// The unit of evidence is one shell command an agent ran: a Claude Code Bash
// call, or one command of a Codex script whose per-command exit code is known.
// A shell command that invokes Scenery more than once has one outcome and one
// duration for all its invocations, so those invocations are counted but
// neither failures nor timings are attributed to them.
type Agents struct {
	ClaudeSessions int     `json:"claude_sessions"`
	CodexSessions  int     `json:"codex_sessions"`
	ToolCalls      int     `json:"tool_calls"`
	ToolErrors     int     `json:"tool_errors"`
	ToolErrorKinds []Count `json:"tool_error_kinds"`
	// SceneryInvocations counts attempted invocations, recognized or not.
	SceneryInvocations int `json:"scenery_invocations"`
	// SceneryCommands counts the shell commands that attempted Scenery, and
	// SceneryFailed those that failed.
	SceneryCommands int            `json:"scenery_commands"`
	SceneryFailed   int            `json:"scenery_failed"`
	Commands        []AgentCommand `json:"commands"`
	FailureClasses  []Count        `json:"failure_classes"`
	RejectedInputs  []Count        `json:"rejected_inputs"`
}

// AgentCommand is one attempted Scenery command: a command `scenery help`
// advertises, "unknown <word>" for a word it does not, or a root flag such as
// "--help". Failures, waiting time and p50 cover only the shell commands that
// invoked Scenery once (Attributable).
type AgentCommand struct {
	Command      string `json:"command"`
	Count        int    `json:"count"`
	Attributable int    `json:"attributable"`
	FailureCount int    `json:"failure_count"`
	WallTimeMS   int64  `json:"wall_time_ms"`
	P50MS        *int64 `json:"p50_ms"`
}

// toolCall is one completed tool call of an agent session.
type toolCall struct {
	commands []string // shell commands the call ran; empty for other tools
	exit     int      // -1 when unknown
	errored  bool
	output   string
	duration time.Duration
	at       time.Time
}

type agentSession struct {
	agent string
	calls []toolCall
}

var (
	// sceneryInvocation matches Scenery where a shell runs a command: at the
	// start of a line or after ;, &&, ||, |, ( or $(, optionally behind
	// variable assignments, sudo/env/time/exec/command, a path, or go run.
	// A mention inside an argument, such as echo "scenery up", is no command.
	sceneryInvocation = regexp.MustCompile(`(?m)(?:^|&&|\|\||[;|(` + "`" + `]|\$\()[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=\S*[ \t]+)*(?:(?:sudo|env|time|exec|command)[ \t]+)*(?:go[ \t]+run[ \t]+)?(?:[^\s;&|(` + "`" + `]*/)?scenery[ \t]+([^\s;&|)` + "`" + `]+)(?:[ \t]+([a-z][a-z-]*))?`)
	commandWord       = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	rejectedInput     = regexp.MustCompile(`unknown (command|flag|subcommand) "([^"]{1,40})"`)
	claudeExitCode    = regexp.MustCompile(`Exit code (\d+)`)
	codexChunk        = regexp.MustCompile(`"wall_time_seconds"\s*:\s*([0-9.eE+-]+)\s*,\s*"exit_code"\s*:\s*(-?\d+)`)
	codexCommand      = regexp.MustCompile(`cmd\s*:\s*"((?:[^"\\]|\\.)*)"`)
	codexScriptError  = regexp.MustCompile(`^(Error|Script error|Script failed)|Uncaught|TypeError|SyntaxError|ReferenceError`)
	invalidInvocation = regexp.MustCompile(`SCN8001|invalid_request|unknown (command|flag|subcommand) "|flag provided but not defined`)
	internalFailure   = regexp.MustCompile(`SCN9\d{3}|internal tooling failure`)
	appDiagnostic     = regexp.MustCompile(`SCN[1-7]\d{3}`)
	notFound          = regexp.MustCompile(`(?i)command not found|no such file or directory`)
	timedOut          = regexp.MustCompile(`(?i)timed out|deadline exceeded`)
)

// sceneryCommands returns the Scenery commands a shell command attempts: a
// known command, with its subcommand for a family that has one; a root flag
// such as "--help"; or "unknown <word>" for a word Scenery has no command for.
func sceneryCommands(shell string, families map[string][]string) []string {
	var result []string
	for _, match := range sceneryInvocation.FindAllStringSubmatch(shell, -1) {
		word := match[1]
		switch subcommands, known := families[word]; {
		case known:
			command := word
			for _, sub := range subcommands {
				if sub == match[2] {
					command += " " + sub
					break
				}
			}
			result = append(result, command)
		case strings.HasPrefix(word, "-") && len(word) <= 24:
			result = append(result, word)
		case commandWord.MatchString(word):
			result = append(result, "unknown "+word)
		}
	}
	return result
}

// sceneryFailureClass classifies a failed shell command by what Scenery or the
// shell reported; a command that succeeded has no class.
func sceneryFailureClass(call toolCall) string {
	if !call.errored && call.exit <= 0 {
		return ""
	}
	text := call.output
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
	if call.exit > 0 {
		return "shell command failed"
	}
	return "other"
}

func readAgents(opts Options) (Agents, error) {
	agents := Agents{ToolErrorKinds: []Count{}, Commands: []AgentCommand{}, FailureClasses: []Count{}, RejectedInputs: []Count{}}
	var sessions []agentSession
	if opts.ClaudeProjectsDir != "" {
		found, err := readTranscripts(opts, opts.ClaudeProjectsDir, readClaudeSession)
		if err != nil {
			return Agents{}, err
		}
		sessions = append(sessions, found...)
	}
	if opts.CodexSessionsDir != "" {
		found, err := readTranscripts(opts, opts.CodexSessionsDir, readCodexSession)
		if err != nil {
			return Agents{}, err
		}
		sessions = append(sessions, found...)
	}
	kinds, classes, rejected := map[string]int{}, map[string]int{}, map[string]int{}
	type commandAcc struct {
		count, attributable, failures int
		wall                          int64
		durations                     []int64
	}
	commands := map[string]*commandAcc{}
	for _, session := range sessions {
		ran := false
		for _, call := range session.calls {
			for _, shell := range call.commands {
				if len(sceneryCommands(shell, opts.CommandFamilies)) > 0 {
					ran = true
				}
			}
		}
		if !ran {
			continue
		}
		switch session.agent {
		case "claude":
			agents.ClaudeSessions++
		case "codex":
			agents.CodexSessions++
		}
		for _, call := range session.calls {
			if !opts.inWindow(call.at) {
				continue
			}
			agents.ToolCalls++
			if call.errored {
				agents.ToolErrors++
				kinds[toolErrorKind(call)]++
			}
			var invoked []string
			for _, shell := range call.commands {
				invoked = append(invoked, sceneryCommands(shell, opts.CommandFamilies)...)
			}
			if len(invoked) == 0 {
				continue
			}
			agents.SceneryCommands++
			agents.SceneryInvocations += len(invoked)
			class := sceneryFailureClass(call)
			if class != "" {
				agents.SceneryFailed++
				classes[class]++
			}
			for _, match := range rejectedInput.FindAllStringSubmatch(call.output, -1) {
				rejected["unknown "+match[1]+" \""+match[2]+"\""]++
			}
			for _, command := range invoked {
				acc := commands[command]
				if acc == nil {
					acc = &commandAcc{}
					commands[command] = acc
				}
				acc.count++
				if len(invoked) != 1 {
					continue
				}
				acc.attributable++
				acc.wall += call.duration.Milliseconds()
				acc.durations = append(acc.durations, call.duration.Milliseconds())
				if class != "" {
					acc.failures++
				}
			}
		}
	}
	for command, acc := range commands {
		agents.Commands = append(agents.Commands, AgentCommand{Command: command, Count: acc.count, Attributable: acc.attributable, FailureCount: acc.failures, WallTimeMS: acc.wall, P50MS: percentile(acc.durations, 50)})
	}
	sort.Slice(agents.Commands, func(i, j int) bool {
		return agents.Commands[i].Count > agents.Commands[j].Count || agents.Commands[i].Count == agents.Commands[j].Count && agents.Commands[i].Command < agents.Commands[j].Command
	})
	agents.ToolErrorKinds = sortedCounts(kinds, 0)
	agents.FailureClasses = sortedCounts(classes, 0)
	agents.RejectedInputs = sortedCounts(rejected, 20)
	return agents, nil
}

// readTranscripts reads, in parallel, every transcript under root modified
// inside the window and returns the sessions in path order.
func readTranscripts(opts Options, root string, read func(string) (agentSession, error)) ([]agentSession, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
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
		return nil, err
	}
	sessions := make([]agentSession, len(paths))
	work := make(chan int)
	var wg sync.WaitGroup
	for range min(max(runtime.GOMAXPROCS(0), 1), 8) {
		wg.Go(func() {
			for index := range work {
				if session, err := read(paths[index]); err == nil {
					sessions[index] = session
				}
			}
		})
	}
	for index := range paths {
		work <- index
	}
	close(work)
	wg.Wait()
	found := sessions[:0]
	for _, session := range sessions {
		if len(session.calls) > 0 {
			found = append(found, session)
		}
	}
	return found, nil
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

// readClaudeSession pairs each tool use of a Claude Code transcript with its
// result.
func readClaudeSession(path string) (agentSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return agentSession{}, err
	}
	defer func() { _ = file.Close() }()
	session := agentSession{agent: "claude"}
	type use struct {
		command string
		at      time.Time
	}
	uses := map[string]use{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	for scanner.Scan() {
		// Only tool uses and their results are decoded.
		if raw := scanner.Bytes(); !bytes.Contains(raw, []byte(`"tool_use"`)) && !bytes.Contains(raw, []byte(`"tool_result"`)) {
			continue
		}
		var line struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil || (line.Type != "assistant" && line.Type != "user") {
			continue
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
			continue
		}
		for _, item := range items {
			switch item.Type {
			case "tool_use":
				var input struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(item.Input, &input)
				command := ""
				if item.Name == "Bash" {
					command = input.Command
				}
				uses[item.ID] = use{command: command, at: line.Timestamp}
			case "tool_result":
				started, ok := uses[item.ToolUseID]
				if !ok {
					continue
				}
				delete(uses, item.ToolUseID)
				output := limitText(contentText(item.Content))
				call := toolCall{errored: item.IsError, output: output, at: started.at, duration: line.Timestamp.Sub(started.at), exit: -1}
				if started.command != "" {
					call.commands = []string{started.command}
					call.exit = 0
					if match := claudeExitCode.FindStringSubmatch(output); item.IsError && match != nil {
						call.exit, _ = strconv.Atoi(match[1])
					}
				}
				session.calls = append(session.calls, call)
			}
		}
	}
	return session, scanner.Err()
}

// readCodexSession pairs each tool call of a Codex rollout with its output. A
// script that runs several shell commands reports one exit code per command
// in order; they are attributed only when the counts agree.
func readCodexSession(path string) (agentSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return agentSession{}, err
	}
	defer func() { _ = file.Close() }()
	session := agentSession{agent: "codex"}
	type pending struct {
		commands []string
		at       time.Time
	}
	calls := map[string]pending{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	for scanner.Scan() {
		// Only tool calls and their outputs are decoded.
		if raw := scanner.Bytes(); !bytes.Contains(raw, []byte(`call`)) {
			continue
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
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		payload := line.Payload
		switch payload.Type {
		case "custom_tool_call", "function_call":
			source := payload.Input
			if payload.Type == "function_call" {
				source = payload.Arguments
			}
			calls[payload.CallID] = pending{commands: codexCommands(source), at: line.Timestamp}
		case "custom_tool_call_output", "function_call_output":
			started, ok := calls[payload.CallID]
			if !ok {
				continue
			}
			delete(calls, payload.CallID)
			output := contentText(payload.Output)
			// Each command a script ran reports its own chunk: wall time, then
			// exit code.
			var exits []int
			var walls []time.Duration
			var outputs []string
			chunks := codexChunk.FindAllStringSubmatchIndex(output, -1)
			for index, match := range chunks {
				seconds, _ := strconv.ParseFloat(output[match[2]:match[3]], 64)
				code, _ := strconv.Atoi(output[match[4]:match[5]])
				walls = append(walls, time.Duration(seconds*float64(time.Second)))
				exits = append(exits, code)
				end := len(output)
				if index+1 < len(chunks) {
					end = chunks[index+1][0]
				}
				outputs = append(outputs, output[match[0]:end])
			}
			head := output
			if len(head) > 300 {
				head = head[:300]
			}
			errored := codexScriptError.MatchString(head)
			duration := line.Timestamp.Sub(started.at)
			if len(started.commands) == 0 {
				session.calls = append(session.calls, toolCall{errored: errored, output: limitText(output), at: started.at, duration: duration, exit: -1})
				continue
			}
			// One exit code per command makes each command its own outcome;
			// otherwise the script is one call whose outcome covers them all.
			// Commands of one script share its duration, which is not divided.
			if len(exits) == len(started.commands) {
				for index, command := range started.commands {
					session.calls = append(session.calls, toolCall{commands: []string{command}, exit: exits[index], errored: errored || exits[index] > 0, output: limitText(outputs[index]), at: started.at, duration: walls[index]})
				}
				continue
			}
			exit := -1
			session.calls = append(session.calls, toolCall{commands: started.commands, exit: exit, errored: errored || exit > 0, output: limitText(output), at: started.at, duration: duration})
		}
	}
	return session, scanner.Err()
}

func codexCommands(source string) []string {
	var commands []string
	for _, match := range codexCommand.FindAllStringSubmatch(source, -1) {
		if unquoted, err := strconv.Unquote(`"` + match[1] + `"`); err == nil {
			commands = append(commands, unquoted)
		} else {
			commands = append(commands, match[1])
		}
	}
	if len(commands) > 0 || source == "" {
		return commands
	}
	var arguments struct {
		Cmd     json.RawMessage `json:"cmd"`
		Command json.RawMessage `json:"command"`
	}
	if json.Unmarshal([]byte(source), &arguments) != nil {
		return nil
	}
	for _, raw := range []json.RawMessage{arguments.Cmd, arguments.Command} {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			return []string{text}
		}
		var words []string
		if json.Unmarshal(raw, &words) == nil && len(words) > 0 {
			return []string{strings.Join(words, " ")}
		}
	}
	return nil
}
