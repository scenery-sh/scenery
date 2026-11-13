package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

const sceneryCronExecutionHeader = "X-Scenery-Cron-Execution"

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

func startCronScheduler(parent context.Context) (*cronScheduler, error) {
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
	type completion struct{ id string }
	done := make(chan completion, 64)
	running := map[string]context.CancelFunc{}
	queued := []time.Time{}
	var workers sync.WaitGroup
	start := func(scheduledAt time.Time) {
		executionID, err := newCronExecutionID(job.ID, scheduledAt)
		if err != nil {
			slog.Error("scenery cron job failed to allocate execution id", "id", job.ID, "err", err)
			return
		}
		callCtx, cancel := context.WithCancel(withCronInvocation(ctx, scheduledAt, executionID))
		running[executionID] = cancel
		workers.Go(func() {
			if err := invokeAdmittedCronJob(callCtx, job); err != nil && callCtx.Err() == nil {
				slog.Error("scenery cron job failed", "id", job.ID, "err", err)
			}
			select {
			case done <- completion{id: executionID}:
			case <-ctx.Done():
			}
		})
	}
	stopRunning := func() {
		for _, cancel := range running {
			cancel()
		}
	}
	defer func() {
		stopRunning()
		workers.Wait()
	}()

	next := job.plan.Next(time.Now().UTC())
	for {
		var timer *time.Timer
		var timerChannel <-chan time.Time
		if !next.IsZero() {
			timer = time.NewTimer(max(time.Until(next), 0))
			timerChannel = timer.C
		} else if len(running) == 0 && len(queued) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case finished := <-done:
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if cancel := running[finished.id]; cancel != nil {
				cancel()
				delete(running, finished.id)
			}
			if len(running) == 0 && len(queued) > 0 {
				for len(queued) > 0 {
					scheduledAt := queued[0]
					queued = queued[1:]
					if job.CatchupWindow > 0 && time.Since(scheduledAt) > job.CatchupWindow {
						continue
					}
					start(scheduledAt)
					break
				}
			}
		case <-timerChannel:
			scheduledAt := next
			next = job.plan.Next(scheduledAt)
			switch job.OverlapPolicy {
			case "allow_all":
				start(scheduledAt)
			case "cancel_other":
				stopRunning()
				queued = queued[:0]
				start(scheduledAt)
			case "buffer_all":
				if len(running) == 0 {
					start(scheduledAt)
				} else {
					queued = append(queued, scheduledAt)
				}
			default:
				if len(running) == 0 {
					start(scheduledAt)
				}
			}
		}
	}
}

// invokeAdmittedCronJob runs one scheduled run admitted to an application
// generation; a run that is not admitted does not start.
func invokeAdmittedCronJob(ctx context.Context, job *CronJob) error {
	admitted, generation, release, err := admitProcessGeneration(ctx)
	if err != nil {
		return err
	}
	defer release()
	if state := stateFromContext(admitted); state != nil {
		state.processGeneration = generation
	}
	return processAdmissionOutcome(admitted, safeInvokeCronJob(admitted, job))
}

func safeInvokeCronJob(ctx context.Context, job *CronJob) (err error) {
	state := stateFromContext(ctx)
	if state != nil {
		restore := enterState(state)
		defer restore()
		if state.request.Service == "" {
			state.request.Service = "cron"
		}
		state.request.Endpoint = job.ID
		state.request.Path = "/cron/" + job.ID
		startRequestTrace(state)
		logRequestStart(state)
		defer func() {
			finishRequestTrace(state, errs.HTTPStatus(err), err)
		}()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic executing cron job %s: %v", job.ID, recovered)
		}
	}()
	return job.Invoke(ctx)
}

func withCronInvocation(ctx context.Context, scheduledAt time.Time, executionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	headers := make(http.Header)
	headers.Set(sceneryCronExecutionHeader, executionID)
	request := shared.Request{
		Type:               shared.APICall,
		Started:            scheduledAt,
		InvocationID:       executionID,
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
	return "every " + job.Every.String()
}

func validateCronOverlapPolicy(policy string) error {
	switch strings.TrimSpace(strings.ToLower(policy)) {
	case "", "skip", "buffer_all", "cancel_other", "allow_all":
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

	if job.Every <= 0 {
		return fmt.Errorf("runtime: cron job %s must define Every", job.ID)
	}
	if err := validateCronOverlapPolicy(job.OverlapPolicy); err != nil {
		return err
	}
	if job.CatchupWindow < 0 {
		return fmt.Errorf("runtime: cron job %s CatchupWindow cannot be negative", job.ID)
	}
	if job.Every%time.Second != 0 {
		return fmt.Errorf("runtime: cron job %s Every must be a whole number of seconds", job.ID)
	}
	if (24 * time.Hour % job.Every) != 0 {
		return fmt.Errorf("runtime: cron job %s Every must divide 24 hours evenly", job.ID)
	}
	job.plan = everyCronPlan{interval: job.Every}
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
