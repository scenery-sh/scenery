package main

import (
	"context"
	"encoding/json"
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
	TUI      bool
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

func attachCommand(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	if opts.TUI {
		return runSceneryConsoleOrFallback(context.Background(), os.Stdin, os.Stdout, opts)
	}
	logArgs, err := attachLogArgs(args)
	if err != nil {
		return err
	}
	return runSceneryLogsFunc(context.Background(), os.Stdout, logArgs)
}

func consoleCommand(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	opts.TUI = true
	return runSceneryConsoleOrFallback(context.Background(), os.Stdin, os.Stdout, opts)
}

func attachLogArgs(args []string) ([]string, error) {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return nil, err
	}
	return logArgsFromOptions(opts, true), nil
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
		out = append(out, "-o", "jsonl")
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

	victoria, err := logsVictoriaStack(ctx)
	if err != nil {
		return err
	}
	devQuery := logsDevEventQuery(opts, appID, sessionID)
	devItems, err := victoria.ListDevEvents(ctx, devQuery)
	if err != nil {
		return err
	}
	return followVictoriaDevEvents(ctx, stdout, victoria, appID, appRoot, sessionID, opts, devItems)
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
	stream, level, since := opts.Stream, "", ""
	flags := newCLIFlagSet("logs")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.IntVar(&opts.Limit, "limit", opts.Limit, "")
	flags.IntVar(&opts.Limit, "n", opts.Limit, "")
	flags.BoolVar(&opts.Follow, "follow", false, "")
	flags.BoolVar(&opts.Follow, "f", false, "")
	registerJSONLinesOutput(flags, &opts.JSONL)
	flags.BoolVar(&opts.TUI, "tui", false, "")
	flags.StringVar(&stream, "stream", stream, "")
	flags.StringVar(&opts.Session, "session", "", "")
	flags.StringVar(&opts.Source, "source", "", "")
	flags.StringVar(&opts.Kind, "kind", "", "")
	flags.StringVar(&level, "level", "", "")
	flags.StringVar(&opts.Grep, "grep", "", "")
	flags.StringVar(&since, "since", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return logsOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return logsOptions{}, err
	}
	if opts.Limit <= 0 {
		return logsOptions{}, fmt.Errorf("invalid limit %d", opts.Limit)
	}
	opts.Stream = normalizeLogStream(stream)
	if opts.Stream == "" {
		return logsOptions{}, fmt.Errorf("invalid stream %q", stream)
	}
	opts.Session = strings.TrimSpace(opts.Session)
	if cliFlagSet(flags, "session") && opts.Session == "" {
		return logsOptions{}, fmt.Errorf("invalid session %q", opts.Session)
	}
	opts.Source = strings.TrimSpace(opts.Source)
	if cliFlagSet(flags, "source") && opts.Source == "" {
		return logsOptions{}, fmt.Errorf("invalid source %q", opts.Source)
	}
	opts.Kind = strings.ToLower(strings.TrimSpace(opts.Kind))
	if cliFlagSet(flags, "kind") && opts.Kind == "" {
		return logsOptions{}, fmt.Errorf("invalid kind %q", opts.Kind)
	}
	if cliFlagSet(flags, "level") {
		opts.Level = normalizeLogLevel(level)
		if opts.Level == "" {
			return logsOptions{}, fmt.Errorf("invalid level %q", level)
		}
	}
	opts.Grep = strings.TrimSpace(opts.Grep)
	if cliFlagSet(flags, "grep") && opts.Grep == "" {
		return logsOptions{}, fmt.Errorf("invalid grep %q", opts.Grep)
	}
	if since != "" {
		opts.Since, err = time.ParseDuration(since)
		if err != nil || opts.Since <= 0 {
			return logsOptions{}, fmt.Errorf("invalid since duration %q", since)
		}
		opts.SinceRaw = since
	}
	return opts, nil
}

var resolveLogsVictoriaStackFunc = resolveLogsVictoriaStack

func logsVictoriaStack(ctx context.Context) (*victoriaStack, error) {
	victoria := resolveLogsVictoriaStackFunc(ctx, true)
	if victoria == nil {
		return nil, fmt.Errorf("VictoriaLogs is unavailable")
	}
	return victoria, nil
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

func followVictoriaDevEvents(ctx context.Context, stdout io.Writer, victoria *victoriaStack, appID, appRoot, sessionID string, opts logsOptions, items []devdash.DevEvent) error {
	lastID := int64(0)
	eventCount := 0
	var events *cliEventWriter
	if opts.JSONL {
		events = newCLIEventWriter(stdout)
	}
	for _, item := range items {
		if item.ID > lastID {
			lastID = item.ID
		}
		if err := writeDevEventOutput(stdout, events, appID, appRoot, item); err != nil {
			return err
		}
		eventCount++
	}
	if !opts.Follow {
		if events != nil {
			return events.summary(eventCount)
		}
		return nil
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if events != nil {
				return events.summary(eventCount)
			}
			return nil
		case <-ticker.C:
			query := logsDevEventQuery(opts, appID, sessionID)
			query.AfterID = lastID
			items, err := victoria.ListDevEvents(ctx, query)
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ID > lastID {
					lastID = item.ID
				}
				if err := writeDevEventOutput(stdout, events, appID, appRoot, item); err != nil {
					return err
				}
				eventCount++
			}
		}
	}
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

func writeDevEventOutput(w io.Writer, events *cliEventWriter, appName, appRoot string, item devdash.DevEvent) error {
	if events != nil {
		return writeDevEventJSONL(events, appName, appRoot, item)
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

func writeDevEventJSONL(events *cliEventWriter, appName, appRoot string, item devdash.DevEvent) error {
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
	return events.event(event)
}
