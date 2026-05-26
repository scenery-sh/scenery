package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	temporalclient "go.temporal.io/sdk/client"
)

func TestEveryCronPlanAlignsToUTCGrid(t *testing.T) {
	plan := everyCronPlan{interval: 6 * time.Hour}
	got := plan.Next(time.Date(2026, time.April, 14, 7, 10, 0, 0, time.UTC))
	want := time.Date(2026, time.April, 14, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next() = %s, want %s", got, want)
	}
}

func TestParseCronScheduleSupportsNamesAndSteps(t *testing.T) {
	plan, err := parseCronSchedule("*/15 9-17 * * MON-FRI")
	if err != nil {
		t.Fatalf("parseCronSchedule returned error: %v", err)
	}
	got := plan.Next(time.Date(2026, time.April, 13, 8, 59, 0, 0, time.UTC))
	want := time.Date(2026, time.April, 13, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next() = %s, want %s", got, want)
	}
}

func TestValidateCronJobRequiresExactlyOneScheduleMode(t *testing.T) {
	err := validateCronJob(&CronJob{
		ID:       "tick",
		Every:    time.Minute,
		Schedule: "* * * * *",
		Invoke:   func(context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("validateCronJob returned nil error")
	}
}

func TestTemporalCronScheduleSpecForEvery(t *testing.T) {
	job := &CronJob{
		ID:     "tick",
		Every:  5 * time.Minute,
		Invoke: func(context.Context) error { return nil },
	}
	if err := validateCronJob(job); err != nil {
		t.Fatalf("validateCronJob returned error: %v", err)
	}
	spec, err := temporalCronScheduleSpec(job)
	if err != nil {
		t.Fatalf("temporalCronScheduleSpec returned error: %v", err)
	}
	if len(spec.Intervals) != 1 || spec.Intervals[0].Every != 5*time.Minute {
		t.Fatalf("intervals = %#v", spec.Intervals)
	}
}

func TestTemporalCronScheduleOptionsApplyPolicy(t *testing.T) {
	job := &CronJob{
		ID:                   "tick",
		Every:                5 * time.Minute,
		OverlapPolicy:        "buffer_one",
		CatchupWindow:        10 * time.Minute,
		PauseOnFailure:       true,
		ActivityStartToClose: 2 * time.Minute,
		ActivityRetryPolicy: CronRetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
		Invoke: func(context.Context) error { return nil },
	}
	if err := validateCronJob(job); err != nil {
		t.Fatalf("validateCronJob returned error: %v", err)
	}
	options, err := temporalCronScheduleOptions(AppConfig{Name: "app"}, TemporalRuntimeInfo{TaskQueuePrefix: "app"}, "app.cron.go", job)
	if err != nil {
		t.Fatalf("temporalCronScheduleOptions returned error: %v", err)
	}
	if options.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE {
		t.Fatalf("Overlap = %v, want BUFFER_ONE", options.Overlap)
	}
	if options.CatchupWindow != 10*time.Minute {
		t.Fatalf("CatchupWindow = %s, want 10m", options.CatchupWindow)
	}
	if !options.PauseOnFailure {
		t.Fatal("PauseOnFailure = false, want true")
	}
	action, ok := options.Action.(*temporalclient.ScheduleWorkflowAction)
	if !ok {
		t.Fatalf("Action = %T, want *client.ScheduleWorkflowAction", options.Action)
	}
	if len(action.Args) != 1 {
		t.Fatalf("Action.Args length = %d, want 1", len(action.Args))
	}
	input, ok := action.Args[0].(temporalCronInput)
	if !ok {
		t.Fatalf("Action.Args[0] = %T, want temporalCronInput", action.Args[0])
	}
	if input.ActivityStartToClose != 2*time.Minute {
		t.Fatalf("ActivityStartToClose = %s, want 2m", input.ActivityStartToClose)
	}
	if input.ActivityRetryPolicy.MaximumAttempts != 3 {
		t.Fatalf("ActivityRetryPolicy.MaximumAttempts = %d, want 3", input.ActivityRetryPolicy.MaximumAttempts)
	}
}

func TestStableTemporalCronExecutionIDIsDeterministic(t *testing.T) {
	scheduledAt := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	got := stableTemporalCronExecutionID("orders-app", "nightly-sync", scheduledAt)
	if got != stableTemporalCronExecutionID("orders-app", "nightly-sync", scheduledAt) {
		t.Fatalf("stableTemporalCronExecutionID returned different values")
	}
	if got != "orders.app-nightly.sync-20260526T103000Z" {
		t.Fatalf("stableTemporalCronExecutionID = %q", got)
	}
}

func TestTemporalCronRetryPolicySkipsNonPositiveInitialInterval(t *testing.T) {
	if got := temporalCronRetryPolicy(CronRetryPolicy{MaximumAttempts: 3}); got != nil {
		t.Fatalf("temporalCronRetryPolicy = %#v, want nil", got)
	}
	if got := temporalCronRetryPolicy(CronRetryPolicy{InitialInterval: -time.Second, MaximumAttempts: 3}); got != nil {
		t.Fatalf("temporalCronRetryPolicy = %#v, want nil", got)
	}
	got := temporalCronRetryPolicy(CronRetryPolicy{InitialInterval: time.Second, MaximumAttempts: 3})
	if got == nil || got.InitialInterval != time.Second || got.MaximumAttempts != 3 {
		t.Fatalf("temporalCronRetryPolicy = %#v", got)
	}
}

func TestTemporalCronScheduleOptionsDefaultPolicy(t *testing.T) {
	job := &CronJob{
		ID:     "tick",
		Every:  5 * time.Minute,
		Invoke: func(context.Context) error { return nil },
	}
	if err := validateCronJob(job); err != nil {
		t.Fatalf("validateCronJob returned error: %v", err)
	}
	options, err := temporalCronScheduleOptions(AppConfig{Name: "app"}, TemporalRuntimeInfo{TaskQueuePrefix: "app"}, "app.cron.go", job)
	if err != nil {
		t.Fatalf("temporalCronScheduleOptions returned error: %v", err)
	}
	if options.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Fatalf("Overlap = %v, want SKIP", options.Overlap)
	}
	if options.CatchupWindow != time.Minute {
		t.Fatalf("CatchupWindow = %s, want 1m", options.CatchupWindow)
	}
	action := options.Action.(*temporalclient.ScheduleWorkflowAction)
	input := action.Args[0].(temporalCronInput)
	if input.ActivityStartToClose != time.Hour {
		t.Fatalf("ActivityStartToClose = %s, want 1h", input.ActivityStartToClose)
	}
}

func TestValidateCronJobRejectsInvalidTemporalPolicy(t *testing.T) {
	err := validateCronJob(&CronJob{
		ID:            "tick",
		Every:         time.Minute,
		OverlapPolicy: "sideways",
		Invoke:        func(context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("validateCronJob returned nil error for invalid overlap policy")
	}

	err = validateCronJob(&CronJob{
		ID:            "tick",
		Every:         time.Minute,
		CatchupWindow: -time.Second,
		Invoke:        func(context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("validateCronJob returned nil error for negative catchup window")
	}

	err = validateCronJob(&CronJob{
		ID:     "tick",
		Every:  time.Minute,
		Invoke: func(context.Context) error { return nil },
		ActivityRetryPolicy: CronRetryPolicy{
			MaximumAttempts: 3,
		},
	})
	if err == nil {
		t.Fatal("validateCronJob returned nil error for retry policy without initial interval")
	}
}

func TestTemporalCronCalendarPreservesDayOfMonthOrDayOfWeekSemantics(t *testing.T) {
	job := &CronJob{
		ID:       "monthly-or-monday",
		Schedule: "0 9 1 * MON",
		Invoke:   func(context.Context) error { return nil },
	}
	if err := validateCronJob(job); err != nil {
		t.Fatalf("validateCronJob returned error: %v", err)
	}
	spec, err := temporalCronScheduleSpec(job)
	if err != nil {
		t.Fatalf("temporalCronScheduleSpec returned error: %v", err)
	}
	if len(spec.Calendars) != 2 {
		t.Fatalf("calendar specs = %#v, want DOM and DOW union", spec.Calendars)
	}
	domSpec, dowSpec := spec.Calendars[0], spec.Calendars[1]
	if len(domSpec.DayOfMonth) != 1 || domSpec.DayOfMonth[0].Start != 1 {
		t.Fatalf("day-of-month spec = %#v", domSpec.DayOfMonth)
	}
	if len(domSpec.DayOfWeek) != 1 || domSpec.DayOfWeek[0].Start != 0 || domSpec.DayOfWeek[0].End != 6 {
		t.Fatalf("day-of-month spec day-of-week = %#v", domSpec.DayOfWeek)
	}
	if len(dowSpec.DayOfWeek) != 1 || dowSpec.DayOfWeek[0].Start != 1 {
		t.Fatalf("day-of-week spec = %#v", dowSpec.DayOfWeek)
	}
	if len(dowSpec.DayOfMonth) != 1 || dowSpec.DayOfMonth[0].Start != 1 || dowSpec.DayOfMonth[0].End != 31 {
		t.Fatalf("day-of-week spec day-of-month = %#v", dowSpec.DayOfMonth)
	}
}

func TestTemporalCronRoleSplit(t *testing.T) {
	if shouldReconcileTemporalCronSchedules("worker") {
		t.Fatal("worker role should not reconcile schedules")
	}
	if !shouldReconcileTemporalCronSchedules("api") || !shouldReconcileTemporalCronSchedules("all") {
		t.Fatal("api/all roles should reconcile schedules")
	}
	if shouldStartTemporalCronWorker("api") {
		t.Fatal("api role should not start cron worker")
	}
	if !shouldStartTemporalCronWorker("worker") || !shouldStartTemporalCronWorker("all") {
		t.Fatal("worker/all roles should start cron worker")
	}
}

func TestTemporalAlreadyExistsErrorDetection(t *testing.T) {
	for _, err := range []error{
		serviceerror.NewAlreadyExists("schedule already exists"),
		fmt.Errorf("schedule with this ID is already registered"),
	} {
		if !isTemporalAlreadyExistsError(err) {
			t.Fatalf("isTemporalAlreadyExistsError(%v) = false, want true", err)
		}
	}
	if isTemporalAlreadyExistsError(fmt.Errorf("permission denied")) {
		t.Fatal("isTemporalAlreadyExistsError returned true for unrelated error")
	}
}
