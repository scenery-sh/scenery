package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	obs "scenery.sh/internal/observability"
)

const (
	logsQueryLimitMax    = 2000
	metricsQueryLimitMax = 10000
)

type logsQueryOptions struct {
	AppRoot  string
	Session  string
	Query    string
	Since    time.Duration
	SinceRaw string
	Start    time.Time
	End      time.Time
	Limit    int
	Timeout  time.Duration
	Fields   []string
	JSONL    bool
	Warnings []string
}

type metricsQueryOptions struct {
	AppRoot  string
	Session  string
	PromQL   string
	Since    time.Duration
	SinceRaw string
	Start    time.Time
	End      time.Time
	Step     time.Duration
	Timeout  time.Duration
	Limit    int
	Instant  bool
	Warnings []string
}

func runLogsQueryCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseLogsQueryArgs(args)
	if err != nil {
		return err
	}
	scope, err := resolveQueryScope(ctx, opts.AppRoot, opts.Session)
	if err != nil {
		return err
	}
	stack := resolveLogsVictoriaStackFunc(ctx, scope.AppRoot)
	baseURL := ""
	if stack != nil {
		baseURL = stack.BaseURL("logs")
	}
	result, err := obs.QueryLogs(ctx, obs.LogsQuery{
		BaseURL:  baseURL,
		Scope:    scope,
		Query:    opts.Query,
		Bounds:   queryBounds(opts.Since, opts.SinceRaw, opts.Start, opts.End),
		Limit:    opts.Limit,
		Timeout:  opts.Timeout,
		Fields:   opts.Fields,
		Warnings: opts.Warnings,
	})
	if err != nil {
		return err
	}
	if opts.JSONL {
		events := newCLIEventWriter(stdout)
		for _, entry := range result.Logs {
			if err := events.event(entry); err != nil {
				return err
			}
		}
		return events.summary(len(result.Logs))
	}
	return writeInspectJSON(stdout, result)
}

func runMetricsQueryCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseMetricsQueryArgs(args)
	if err != nil {
		return err
	}
	scope, err := resolveQueryScope(ctx, opts.AppRoot, opts.Session)
	if err != nil {
		return err
	}
	stack := resolveLogsVictoriaStackFunc(ctx, scope.AppRoot)
	baseURL := ""
	if stack != nil {
		baseURL = stack.BaseURL("metrics")
	}
	result, err := obs.QueryMetrics(ctx, obs.MetricsQuery{
		BaseURL:  baseURL,
		Scope:    scope,
		PromQL:   opts.PromQL,
		Bounds:   queryBounds(opts.Since, opts.SinceRaw, opts.Start, opts.End),
		Step:     opts.Step,
		Instant:  opts.Instant,
		Timeout:  opts.Timeout,
		Limit:    opts.Limit,
		Warnings: opts.Warnings,
	})
	if err != nil {
		return err
	}
	return writeInspectJSON(stdout, result)
}

func parseLogsQueryArgs(args []string) (logsQueryOptions, error) {
	opts := logsQueryOptions{Session: "current", Since: 15 * time.Minute, SinceRaw: "15m", Limit: 200, Timeout: 3 * time.Second}
	since, start, end, timeout, fields := opts.SinceRaw, "", "", opts.Timeout.String(), ""
	flags := newCLIFlagSet("logs query")
	registerJSONOrLinesOutput(flags, &opts.JSONL)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Session, "session", opts.Session, "")
	flags.StringVar(&opts.Query, "query", "", "")
	flags.StringVar(&since, "since", since, "")
	flags.StringVar(&start, "start", "", "")
	flags.StringVar(&end, "end", "", "")
	flags.IntVar(&opts.Limit, "limit", opts.Limit, "")
	flags.StringVar(&timeout, "timeout", timeout, "")
	flags.StringVar(&fields, "fields", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return logsQueryOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return logsQueryOptions{}, err
	}
	opts.Session, opts.Query = strings.TrimSpace(opts.Session), strings.TrimSpace(opts.Query)
	if opts.Session == "" {
		return logsQueryOptions{}, usageErrorf("invalid session %q", opts.Session)
	}
	if opts.Since, err = parsePositiveDuration(since, "since"); err != nil {
		return logsQueryOptions{}, err
	}
	opts.SinceRaw = since
	if start != "" {
		if opts.Start, err = parseQueryTime(start); err != nil {
			return logsQueryOptions{}, err
		}
	}
	if end != "" {
		if opts.End, err = parseQueryTime(end); err != nil {
			return logsQueryOptions{}, err
		}
	}
	if opts.Limit <= 0 {
		return logsQueryOptions{}, usageErrorf("invalid limit %q", strconv.Itoa(opts.Limit))
	}
	if opts.Timeout, err = parsePositiveDuration(timeout, "timeout"); err != nil {
		return logsQueryOptions{}, err
	}
	if fields != "" {
		opts.Fields = splitCSV(fields)
	}
	if opts.Query == "" {
		return logsQueryOptions{}, usageErrorf("missing required --query")
	}
	opts.Limit, opts.Warnings = clampLimit(opts.Limit, logsQueryLimitMax, opts.Warnings)
	return opts, nil
}

func parseMetricsQueryArgs(args []string) (metricsQueryOptions, error) {
	opts := metricsQueryOptions{Session: "current", Since: 15 * time.Minute, SinceRaw: "15m", Step: 5 * time.Second, Timeout: 3 * time.Second, Limit: 100}
	since, start, end, step, timeout := opts.SinceRaw, "", "", opts.Step.String(), opts.Timeout.String()
	flags := newCLIFlagSet("metrics query")
	flags.BoolVar(&opts.Instant, "instant", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Session, "session", opts.Session, "")
	flags.StringVar(&opts.PromQL, "promql", "", "")
	flags.StringVar(&since, "since", since, "")
	flags.StringVar(&start, "start", "", "")
	flags.StringVar(&end, "end", "", "")
	flags.StringVar(&step, "step", step, "")
	flags.StringVar(&timeout, "timeout", timeout, "")
	flags.IntVar(&opts.Limit, "limit", opts.Limit, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return metricsQueryOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return metricsQueryOptions{}, err
	}
	opts.Session, opts.PromQL = strings.TrimSpace(opts.Session), strings.TrimSpace(opts.PromQL)
	if opts.Session == "" {
		return metricsQueryOptions{}, usageErrorf("invalid session %q", opts.Session)
	}
	if opts.Since, err = parsePositiveDuration(since, "since"); err != nil {
		return metricsQueryOptions{}, err
	}
	opts.SinceRaw = since
	if start != "" {
		if opts.Start, err = parseQueryTime(start); err != nil {
			return metricsQueryOptions{}, err
		}
	}
	if end != "" {
		if opts.End, err = parseQueryTime(end); err != nil {
			return metricsQueryOptions{}, err
		}
	}
	if opts.Step, err = parsePositiveDuration(step, "step"); err != nil {
		return metricsQueryOptions{}, err
	}
	if opts.Timeout, err = parsePositiveDuration(timeout, "timeout"); err != nil {
		return metricsQueryOptions{}, err
	}
	if opts.Limit <= 0 {
		return metricsQueryOptions{}, usageErrorf("invalid limit %q", strconv.Itoa(opts.Limit))
	}
	if opts.PromQL == "" {
		return metricsQueryOptions{}, usageErrorf("missing required --promql")
	}
	opts.Limit, opts.Warnings = clampLimit(opts.Limit, metricsQueryLimitMax, opts.Warnings)
	return opts, nil
}

func resolveQueryScope(ctx context.Context, appRootFlag, sessionFlag string) (obs.QueryScope, error) {
	start, err := resolveAppRoot(appRootFlag)
	if err != nil {
		return obs.QueryScope{}, err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return obs.QueryScope{}, err
	}
	return resolveQueryScopeForApp(ctx, appRoot, cfg, sessionFlag)
}

func resolveQueryScopeForApp(ctx context.Context, appRoot string, cfg appcfg.Config, sessionFlag string) (obs.QueryScope, error) {
	client, err := commandWorktreeClient(ctx, appRoot)
	if err != nil {
		return obs.QueryScope{}, err
	}
	return resolveQueryScopeForAppWithClient(ctx, client, appRoot, cfg, sessionFlag)
}

func resolveQueryScopeForAppWithClient(ctx context.Context, client *localagent.Client, appRoot string, cfg appcfg.Config, sessionFlag string) (obs.QueryScope, error) {
	sessionFlag = strings.TrimSpace(sessionFlag)
	if sessionFlag == "" {
		sessionFlag = "current"
	}
	scope := obs.QueryScope{
		AppID:       cfg.AppID(),
		AppRoot:     appRoot,
		AppRootHash: appRootHash(appRoot),
		Worktree:    appWorktreeName(appRoot),
		Enforced:    true,
	}
	if sessionFlag == "current" {
		session, err := currentAgentSessionForAppRootWithClient(ctx, client, appRoot)
		if err != nil {
			return obs.QueryScope{}, err
		}
		scope.SessionID = session.SessionID
		scope.Branch = session.Branch
		if strings.TrimSpace(session.AppRoot) != "" {
			scope.AppRoot = session.AppRoot
			scope.AppRootHash = appRootHash(session.AppRoot)
			scope.Worktree = appWorktreeName(session.AppRoot)
		}
		return scope, nil
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return obs.QueryScope{}, err
	}
	for _, session := range sessions {
		if session.SessionID == sessionFlag {
			scope.SessionID = session.SessionID
			scope.Branch = session.Branch
			if strings.TrimSpace(session.AppRoot) != "" {
				scope.AppRoot = session.AppRoot
				scope.AppRootHash = appRootHash(session.AppRoot)
				scope.Worktree = appWorktreeName(session.AppRoot)
			}
			return scope, nil
		}
	}
	return obs.QueryScope{}, fmt.Errorf("scenery agent session %q was not found for app root %s", sessionFlag, appRoot)
}

func queryBounds(since time.Duration, sinceRaw string, start, end time.Time) obs.TimeBounds {
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if start.IsZero() && since > 0 {
		start = end.Add(-since)
	}
	return obs.TimeBounds{Since: sinceRaw, Start: start.UTC(), End: end.UTC()}
}

func parsePositiveDuration(value, name string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, usageErrorf("invalid %s duration %q", name, value)
	}
	return duration, nil
}

func parseQueryTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, usageErrorf("invalid time %q", value)
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, usageErrorf("invalid time %q; use RFC3339", value)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func clampLimit(limit, max int, warnings []string) (int, []string) {
	if limit > max {
		warnings = append(warnings, fmt.Sprintf("limit clamped from %d to %d", limit, max))
		return max, warnings
	}
	return limit, warnings
}
