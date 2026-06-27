package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

type logsOptions struct {
	AppRoot  string
	Limit    int
	Follow   bool
	Stream   string
	Session  string
	JSONL    bool
	Source   string
	Kind     string
	Level    string
	Grep     string
	Since    time.Duration
	SinceRaw string
}

type logsEvent struct {
	SchemaVersion string `json:"schema_version"`
	App           struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"app"`
	ID        int64                 `json:"id"`
	Time      string                `json:"time"`
	SessionID string                `json:"session_id,omitempty"`
	Source    devdash.DevSource     `json:"source"`
	Level     string                `json:"level"`
	Message   string                `json:"message"`
	Fields    json.RawMessage       `json:"fields,omitempty"`
	Raw       string                `json:"raw,omitempty"`
	Parse     devdash.DevEventParse `json:"parse"`
}

func logsCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "query":
			return runLogsQueryCommand(context.Background(), os.Stdout, args[1:])
		case "tail":
			return runLogsTailCommand(context.Background(), os.Stdout, args[1:])
		}
	}
	return runSceneryLogsFunc(context.Background(), os.Stdout, args)
}

var runSceneryLogsFunc = runSceneryLogs

func consoleCommand(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	return runSceneryConsoleOrFallback(context.Background(), os.Stdin, os.Stdout, opts)
}

func logArgsFromOptions(opts logsOptions, follow bool) []string {
	out := []string{"--limit", strconv.Itoa(opts.Limit), "--stream", opts.Stream}
	if follow {
		out = append([]string{"--follow"}, out...)
	}
	if opts.AppRoot != "" {
		out = append(out, "--app-root", opts.AppRoot)
	}
	if opts.Session != "" {
		out = append(out, "--session", opts.Session)
	}
	if opts.JSONL {
		out = append(out, "--jsonl")
	}
	if opts.Source != "" {
		out = append(out, "--source", opts.Source)
	}
	if opts.Kind != "" {
		out = append(out, "--kind", opts.Kind)
	}
	if opts.Level != "" {
		out = append(out, "--level", opts.Level)
	}
	if opts.Grep != "" {
		out = append(out, "--grep", opts.Grep)
	}
	if opts.SinceRaw != "" {
		out = append(out, "--since", opts.SinceRaw)
	}
	return out
}

func runSceneryLogs(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}

	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	appID := cfg.AppID()
	sessionID, err := resolveLogsSessionID(ctx, opts.Session, appRoot)
	if err != nil {
		return err
	}

	store, err := openDevdashStore()
	if err != nil {
		return err
	}
	defer store.Close()

	record, sessionRecord, err := devdashAppRecordForRuntime(ctx, store, appID, sessionID, appRoot)
	if err != nil {
		return fmt.Errorf("no local logs found for %q; run `scenery up` first", appID)
	}
	if sessionID == "" && sessionRecord {
		sessionID = strings.TrimSpace(record.SessionID)
	}
	if !sessionRecord && sessionID == "" && record.Root != "" && record.Root != appRoot {
		return fmt.Errorf("local logs for %q belong to %s, not %s", appID, record.Root, appRoot)
	}

	devQuery := logsDevEventQuery(opts, appID, sessionID)
	eventBackend := resolveLogsVictoriaStackFunc(ctx, true)
	if eventBackend == nil {
		return errors.New("VictoriaLogs is unavailable")
	}
	devItems, err := eventBackend.ListDevEvents(ctx, devQuery)
	if err != nil {
		return err
	}
	return followDevEventBackend(ctx, stdout, eventBackend, appID, appRoot, sessionID, opts, devItems)
}

func logsDevEventQuery(opts logsOptions, appID, sessionID string) devdash.DevEventQuery {
	query := devdash.DevEventQuery{
		AppID:     appID,
		SessionID: sessionID,
		SourceID:  opts.Source,
		Kind:      opts.Kind,
		Level:     opts.Level,
		Stream:    opts.Stream,
		Grep:      opts.Grep,
		Limit:     opts.Limit,
	}
	if opts.Since > 0 {
		query.Since = time.Now().Add(-opts.Since)
	}
	return query
}

func parseLogsArgs(args []string) (logsOptions, error) {
	opts := logsOptions{
		Limit:  200,
		Stream: "all",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--limit", "-n":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for %s", args[i-1])
			}
			value, err := strconv.Atoi(args[i])
			if err != nil || value <= 0 {
				return logsOptions{}, fmt.Errorf("invalid limit %q", args[i])
			}
			opts.Limit = value
		case "--follow", "-f":
			opts.Follow = true
		case "--jsonl", "--json":
			opts.JSONL = true
		case "--stream":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --stream")
			}
			opts.Stream = normalizeLogStream(args[i])
			if opts.Stream == "" {
				return logsOptions{}, fmt.Errorf("invalid stream %q", args[i])
			}
		case "--session":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.Session = strings.TrimSpace(args[i])
			if opts.Session == "" {
				return logsOptions{}, fmt.Errorf("invalid session %q", args[i])
			}
		case "--source":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --source")
			}
			opts.Source = strings.TrimSpace(args[i])
			if opts.Source == "" {
				return logsOptions{}, fmt.Errorf("invalid source %q", args[i])
			}
		case "--kind":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --kind")
			}
			opts.Kind = strings.ToLower(strings.TrimSpace(args[i]))
			if opts.Kind == "" {
				return logsOptions{}, fmt.Errorf("invalid kind %q", args[i])
			}
		case "--level":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --level")
			}
			opts.Level = normalizeLogLevel(args[i])
			if opts.Level == "" {
				return logsOptions{}, fmt.Errorf("invalid level %q", args[i])
			}
		case "--grep":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --grep")
			}
			opts.Grep = strings.TrimSpace(args[i])
			if opts.Grep == "" {
				return logsOptions{}, fmt.Errorf("invalid grep %q", args[i])
			}
		case "--since":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --since")
			}
			duration, err := time.ParseDuration(args[i])
			if err != nil || duration <= 0 {
				return logsOptions{}, fmt.Errorf("invalid since duration %q", args[i])
			}
			opts.Since = duration
			opts.SinceRaw = args[i]
		default:
			return logsOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func resolveLogsVictoriaStack(ctx context.Context, allowDefault bool) *victoriaStack {
	agentCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if client, err := localagent.DefaultClient(); err == nil {
		if substrate, err := client.GetSubstrate(agentCtx, localagent.SubstrateVictoria); err == nil {
			if stack := victoriaStackFromSubstrate(substrate); stack != nil {
				return stack
			}
		}
	}
	if allowDefault {
		return defaultVictoriaQueryStack()
	}
	return nil
}

func resolveLogsSessionID(ctx context.Context, value, appRoot string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" && value != "current" {
		return value, nil
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		if value == "current" {
			return "", err
		}
		return "", nil
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		if value == "current" {
			return "", err
		}
		return "", nil
	}
	if len(sessions) == 0 {
		if value == "current" {
			return "", fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
		}
		return "", nil
	}
	return sessions[0].SessionID, nil
}

func normalizeLogStream(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all", "":
		return "all"
	case "stdout":
		return "stdout"
	case "stderr":
		return "stderr"
	default:
		return ""
	}
}

func normalizeLogLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug", "trace":
		return "debug"
	case "info", "information", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err":
		return "error"
	case "fatal", "panic":
		return "fatal"
	default:
		return ""
	}
}

func writeDevEventOutput(w io.Writer, appName, appRoot string, item devdash.DevEvent, jsonl bool) error {
	if jsonl {
		return writeDevEventJSONL(w, appName, appRoot, item)
	}
	text := item.Raw
	if text == "" {
		text = item.Message
	}
	if text == "" {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, err := io.WriteString(w, text)
	return err
}

func writeDevEventJSONL(w io.Writer, appName, appRoot string, item devdash.DevEvent) error {
	event := logsEvent{
		SchemaVersion: devdash.DevEventSchemaVersion,
		ID:            item.ID,
		Time:          item.CreatedAt.UTC().Format(time.RFC3339Nano),
		SessionID:     item.SessionID,
		Source:        item.Source,
		Level:         item.Level,
		Message:       item.Message,
		Raw:           item.Raw,
		Parse:         item.Parse,
	}
	if len(item.Fields) > 0 && string(item.Fields) != "{}" {
		event.Fields = item.Fields
	}
	event.App.ID = appName
	event.App.Name = appName
	event.App.Root = appRoot
	enc := json.NewEncoder(w)
	return enc.Encode(event)
}
