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

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/envpolicy"
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
	Backend  string
}

const (
	logsBackendAuto     = "auto"
	logsBackendVictoria = "victoria"
)

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
	return runOnlavaLogsFunc(context.Background(), os.Stdout, args)
}

var runOnlavaLogsFunc = runOnlavaLogs

func attachCommand(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	if opts.TUI {
		return runOnlavaConsoleOrFallback(context.Background(), os.Stdin, os.Stdout, opts)
	}
	logArgs, err := attachLogArgs(args)
	if err != nil {
		return err
	}
	return runOnlavaLogsFunc(context.Background(), os.Stdout, logArgs)
}

func consoleCommand(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	opts.TUI = true
	return runOnlavaConsoleOrFallback(context.Background(), os.Stdin, os.Stdout, opts)
}

func attachLogArgs(args []string) ([]string, error) {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return nil, err
	}
	return logArgsFromOptions(opts, true), nil
}

func logArgsFromOptions(opts logsOptions, follow bool) []string {
	if opts.Session == "" {
		opts.Session = "current"
	}
	out := []string{"--session", opts.Session, "--limit", strconv.Itoa(opts.Limit), "--stream", opts.Stream}
	if follow {
		out = append([]string{"--follow"}, out...)
	}
	if opts.AppRoot != "" {
		out = append(out, "--app-root", opts.AppRoot)
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
	if opts.Backend != "" && opts.Backend != logsBackendAuto {
		out = append(out, "--backend", opts.Backend)
	}
	return out
}

func runOnlavaLogs(ctx context.Context, stdout io.Writer, args []string) error {
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

	record, sessionRecord, err := devdashAppRecordForSession(ctx, store, appID, sessionID)
	if err != nil {
		return fmt.Errorf("no local logs found for %q; run `onlava dev` or `onlava serve` first", appID)
	}
	if !sessionRecord && sessionID == "" && record.Root != "" && record.Root != appRoot {
		return fmt.Errorf("local logs for %q belong to %s, not %s", appID, record.Root, appRoot)
	}

	devQuery := logsDevEventQuery(opts, appID, sessionID)
	eventBackend, err := selectDevEventBackend(ctx, store, opts)
	if err != nil {
		return err
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
		Limit:   200,
		Stream:  "all",
		Backend: logsBackendAuto,
	}
	if backend := strings.TrimSpace(envpolicy.Get("ONLAVA_LOGS_BACKEND")); backend != "" {
		normalized := normalizeLogsBackend(backend)
		if normalized == "" {
			return logsOptions{}, fmt.Errorf("invalid logs backend %q", backend)
		}
		opts.Backend = normalized
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
		case "--tui":
			opts.TUI = true
		case "--backend":
			i++
			if i >= len(args) {
				return logsOptions{}, fmt.Errorf("missing value for --backend")
			}
			opts.Backend = normalizeLogsBackend(args[i])
			if opts.Backend == "" {
				return logsOptions{}, fmt.Errorf("invalid backend %q", args[i])
			}
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

func normalizeLogsBackend(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return logsBackendAuto
	case "victoria", "victorialogs", "vl":
		return logsBackendVictoria
	default:
		return ""
	}
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
	if value == "" {
		return "", nil
	}
	if value != "current" {
		return value, nil
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return "", err
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no onlava agent session found for %s", appRoot)
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
