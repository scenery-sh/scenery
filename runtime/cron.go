package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

const (
	sceneryCronExecutionHeader = "X-Scenery-Cron-Execution"
	maxCronScheduleHorizon     = 5 * 366 * 24 * time.Hour
)

type cronScheduler struct {
	cancel context.CancelFunc
	done   chan struct{}
	stop   func(context.Context) error
}

type cronPlan interface {
	Next(after time.Time) time.Time
}

type everyCronPlan struct {
	interval time.Duration
}

func (p everyCronPlan) Next(after time.Time) time.Time {
	after = after.UTC()
	unix := after.Unix()
	seconds := int64(p.interval / time.Second)
	nextUnix := ((unix / seconds) + 1) * seconds
	return time.Unix(nextUnix, 0).UTC()
}

type parsedCronPlan struct {
	minute cronField
	hour   cronField
	dom    cronField
	month  cronField
	dow    cronField
}

func (p parsedCronPlan) Next(after time.Time) time.Time {
	next := after.UTC().Truncate(time.Minute).Add(time.Minute)
	deadline := next.Add(maxCronScheduleHorizon)
	for !next.After(deadline) {
		if p.matches(next) {
			return next
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}
}

func (p parsedCronPlan) matches(t time.Time) bool {
	if !p.minute.Has(t.Minute()) || !p.hour.Has(t.Hour()) || !p.month.Has(int(t.Month())) {
		return false
	}

	domMatch := p.dom.Has(t.Day())
	dow := int(t.Weekday())
	dowMatch := p.dow.Has(dow) || (dow == 0 && p.dow.Has(7))

	switch {
	case p.dom.any && p.dow.any:
		return true
	case p.dom.any:
		return dowMatch
	case p.dow.any:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

type cronField struct {
	any    bool
	min    int
	max    int
	values []bool
}

func newCronField(min, max int) cronField {
	return cronField{
		min:    min,
		max:    max,
		values: make([]bool, max-min+1),
	}
}

func (f cronField) Has(value int) bool {
	if f.any {
		return true
	}
	if value < f.min || value > f.max {
		return false
	}
	return f.values[value-f.min]
}

func startCronScheduler(parent context.Context, cfg AppConfig) (*cronScheduler, error) {
	jobs := listCronJobs()
	if len(jobs) == 0 {
		done := make(chan struct{})
		close(done)
		return &cronScheduler{done: done}, nil
	}
	return startInProcessCronScheduler(parent, jobs), nil
}

func startInProcessCronScheduler(parent context.Context, jobs []*CronJob) *cronScheduler {
	ctx, cancel := context.WithCancel(parent)
	s := &cronScheduler{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(job *CronJob) {
			defer wg.Done()
			runCronJobLoop(ctx, job)
		}(job)
		slog.Info("scenery cron job scheduled", "id", job.ID, "title", job.Title, "schedule", cronScheduleSummary(job))
	}
	go func() {
		wg.Wait()
		close(s.done)
	}()
	return s
}

func (s *cronScheduler) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.stop != nil {
		return s.stop(ctx)
	}
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runCronJobLoop(ctx context.Context, job *CronJob) {
	for {
		next := job.plan.Next(time.Now().UTC())
		if next.IsZero() {
			slog.Error("scenery cron job disabled after failing to compute next execution", "id", job.ID)
			return
		}

		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}

		executionID, err := newCronExecutionID(job.ID, next)
		if err != nil {
			slog.Error("scenery cron job failed to allocate execution id", "id", job.ID, "err", err)
			continue
		}
		callCtx := withCronInvocation(ctx, job, next, executionID)
		if err := safeInvokeCronJob(callCtx, job); err != nil {
			slog.Error("scenery cron job failed", "id", job.ID, "err", err)
			continue
		}
	}
}

func safeInvokeCronJob(ctx context.Context, job *CronJob) (err error) {
	state := stateFromContext(ctx)
	if state != nil {
		if state.request.Service == "" {
			state.request.Service = "cron"
		}
		state.request.Endpoint = job.ID
		state.request.Path = "/cron/" + job.ID
		startRequestTrace(state)
		logRequestStart(state)
		defer func() {
			finishRequestTrace(state, errs.HTTPStatus(err), nil, err)
		}()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic executing cron job %s: %v", job.ID, recovered)
		}
	}()
	return job.Invoke(ctx)
}

func withCronInvocation(ctx context.Context, job *CronJob, scheduledAt time.Time, executionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	headers := make(http.Header)
	headers.Set(sceneryCronExecutionHeader, executionID)
	request := shared.Request{
		Type:               shared.APICall,
		Started:            scheduledAt,
		Method:             "CRON",
		Headers:            headers,
		CronIdempotencyKey: executionID,
	}
	return withState(ctx, &requestState{
		started:      scheduledAt,
		request:      request,
		logsEnabled:  true,
		traceEnabled: true,
	})
}

func newCronExecutionID(jobID string, scheduledAt time.Time) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", jobID, scheduledAt.UTC().Format("20060102T150405Z"), hex.EncodeToString(suffix[:])), nil
}

func cronScheduleSummary(job *CronJob) string {
	if job.Every > 0 {
		return "every " + job.Every.String()
	}
	return job.Schedule
}

func InvokeCronJob(ctx context.Context, job *CronJob, scheduledAt time.Time, executionID string) error {
	if job == nil {
		return fmt.Errorf("runtime: missing cron job declaration")
	}
	scheduledAt = scheduledAt.UTC()
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		var err error
		executionID, err = newCronExecutionID(job.ID, scheduledAt)
		if err != nil {
			return err
		}
	}
	return safeInvokeCronJob(withCronInvocation(ctx, job, scheduledAt, executionID), job)
}

func validateCronOverlapPolicy(policy string) error {
	switch strings.TrimSpace(strings.ToLower(policy)) {
	case "", "skip", "buffer_one", "buffer_all", "cancel_other", "terminate_other", "allow_all":
		return nil
	default:
		return fmt.Errorf("runtime: cron overlap policy %q is invalid", policy)
	}
}

func validateCronJob(job *CronJob) error {
	if job == nil {
		return fmt.Errorf("runtime: cron job cannot be nil")
	}
	if !isValidCronJobID(job.ID) {
		return fmt.Errorf("runtime: invalid cron job id %q", job.ID)
	}
	if job.Invoke == nil {
		return fmt.Errorf("runtime: cron job %s is missing an endpoint", job.ID)
	}
	if job.Title == "" {
		job.Title = job.ID
	}

	hasEvery := job.Every > 0
	hasSchedule := strings.TrimSpace(job.Schedule) != ""
	if hasEvery == hasSchedule {
		return fmt.Errorf("runtime: cron job %s must define exactly one of Every or Schedule", job.ID)
	}
	if err := validateCronOverlapPolicy(job.OverlapPolicy); err != nil {
		return err
	}
	if job.CatchupWindow < 0 {
		return fmt.Errorf("runtime: cron job %s CatchupWindow cannot be negative", job.ID)
	}
	if hasEvery {
		if job.Every%time.Second != 0 {
			return fmt.Errorf("runtime: cron job %s Every must be a whole number of seconds", job.ID)
		}
		if (24 * time.Hour % job.Every) != 0 {
			return fmt.Errorf("runtime: cron job %s Every must divide 24 hours evenly", job.ID)
		}
		job.plan = everyCronPlan{interval: job.Every}
		return nil
	}

	plan, err := parseCronSchedule(job.Schedule)
	if err != nil {
		return fmt.Errorf("runtime: cron job %s: %w", job.ID, err)
	}
	job.plan = plan
	return nil
}

func isValidCronJobID(id string) bool {
	if len(id) == 0 || len(id) > 63 {
		return false
	}
	for i, r := range id {
		switch {
		case i == 0:
			if r < 'a' || r > 'z' {
				return false
			}
		case i == len(id)-1:
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		default:
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func parseCronSchedule(expr string) (cronPlan, error) {
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid cron schedule %q: expected 5 fields", expr)
	}

	minute, err := parseCronField(parts[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %w", err)
	}
	hour, err := parseCronField(parts[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %w", err)
	}
	dom, err := parseCronField(parts[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-month field: %w", err)
	}
	month, err := parseCronField(parts[3], 1, 12, cronMonthNames)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %w", err)
	}
	dow, err := parseCronField(parts[4], 0, 7, cronDayNames)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-week field: %w", err)
	}
	return parsedCronPlan{
		minute: minute,
		hour:   hour,
		dom:    dom,
		month:  month,
		dow:    dow,
	}, nil
}

var cronMonthNames = map[string]int{
	"JAN": 1,
	"FEB": 2,
	"MAR": 3,
	"APR": 4,
	"MAY": 5,
	"JUN": 6,
	"JUL": 7,
	"AUG": 8,
	"SEP": 9,
	"OCT": 10,
	"NOV": 11,
	"DEC": 12,
}

var cronDayNames = map[string]int{
	"SUN": 0,
	"MON": 1,
	"TUE": 2,
	"WED": 3,
	"THU": 4,
	"FRI": 5,
	"SAT": 6,
}

func parseCronField(expr string, min, max int, names map[string]int) (cronField, error) {
	field := newCronField(min, max)
	if expr == "*" {
		field.any = true
		return field, nil
	}

	for part := range strings.SplitSeq(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return field, fmt.Errorf("empty field segment")
		}

		base, step, err := splitCronStep(part)
		if err != nil {
			return field, err
		}

		var start, end int
		switch {
		case base == "*":
			start, end = min, max
		case strings.Contains(base, "-"):
			left, right, _ := strings.Cut(base, "-")
			start, err = parseCronValue(left, names)
			if err != nil {
				return field, err
			}
			end, err = parseCronValue(right, names)
			if err != nil {
				return field, err
			}
		default:
			start, err = parseCronValue(base, names)
			if err != nil {
				return field, err
			}
			end = start
		}

		if start < min || end > max || start > end {
			return field, fmt.Errorf("value %q out of range [%d,%d]", base, min, max)
		}
		for value := start; value <= end; value += step {
			field.values[value-min] = true
		}
	}

	for _, set := range field.values {
		if set {
			return field, nil
		}
	}
	return field, fmt.Errorf("field matches no values")
}

func splitCronStep(part string) (base string, step int, err error) {
	base = part
	step = 1
	if !strings.Contains(part, "/") {
		return base, step, nil
	}
	var stepExpr string
	base, stepExpr, _ = strings.Cut(part, "/")
	if base == "" || stepExpr == "" {
		return "", 0, fmt.Errorf("invalid step %q", part)
	}
	step, err = strconv.Atoi(stepExpr)
	if err != nil || step <= 0 {
		return "", 0, fmt.Errorf("invalid step %q", part)
	}
	return base, step, nil
}

func parseCronValue(expr string, names map[string]int) (int, error) {
	expr = strings.TrimSpace(strings.ToUpper(expr))
	if expr == "" {
		return 0, fmt.Errorf("empty value")
	}
	if names != nil {
		if value, ok := names[expr]; ok {
			return value, nil
		}
	}
	value, err := strconv.Atoi(expr)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", expr)
	}
	return value, nil
}
