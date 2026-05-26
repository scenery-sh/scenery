package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.temporal.io/api/serviceerror"
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
