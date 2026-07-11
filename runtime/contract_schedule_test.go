package runtime

import (
	"context"
	"testing"
	"time"
)

func TestContractScheduleRegistersTypedCronInvocation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	called := false
	if err := RegisterContractSchedule(ContractScheduleRegistration{
		Address: "house/schedule/nightly", Name: "nightly", TriggerKind: "cron", TriggerValue: "0 2 * * *", Timezone: "Europe/Prague",
		Overlap: "queue", CatchupMaximumAge: 10 * time.Minute, Identity: testWorkloadIdentity(),
		AuthorizationAddress: "std.authorization.scheduled", PipelineAddress: "std.pipeline.empty",
		Invoke: func(context.Context) error {
			called = true
			if auth := CurrentAuth(); auth == nil || auth.UID != "workload:std.workload_identity.scheduler" {
				t.Fatalf("auth = %#v", auth)
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	jobs := listCronJobs()
	if len(jobs) != 1 || jobs[0].Timezone != "Europe/Prague" || jobs[0].OverlapPolicy != "buffer_all" || jobs[0].CatchupWindow != 10*time.Minute {
		t.Fatalf("jobs = %#v", jobs)
	}
	if err := InvokeCronJob(context.Background(), jobs[0], time.Now(), "schedule-test"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("schedule did not invoke operation")
	}
}

func TestContractScheduleSupportsOneShotAndCalendar(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	base := ContractScheduleRegistration{
		Address: "house/schedule/once", Name: "once", TriggerKind: "at", TriggerValue: "2030-01-02T03:04:05Z",
		Overlap: "skip", Identity: testWorkloadIdentity(), AuthorizationAddress: "std.authorization.scheduled", PipelineAddress: "std.pipeline.empty", Invoke: func(context.Context) error { return nil },
	}
	if err := RegisterContractSchedule(base); err != nil {
		t.Fatal(err)
	}
	if len(listCronJobs()) != 1 || listCronJobs()[0].At.IsZero() {
		t.Fatalf("jobs = %#v", listCronJobs())
	}
	base.Address, base.Name, base.TriggerKind, base.TriggerValue = "house/schedule/calendar", "calendar", "calendar", "FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=2"
	if err := RegisterContractSchedule(base); err != nil {
		t.Fatal(err)
	}
	jobs := listCronJobs()
	foundCalendar := false
	for _, job := range jobs {
		foundCalendar = foundCalendar || job.Calendar != "" && job.plan != nil
	}
	if len(jobs) != 2 || !foundCalendar {
		t.Fatalf("jobs = %#v", jobs)
	}
}

func testWorkloadIdentity() ContractWorkloadIdentity {
	return ContractWorkloadIdentity{
		Address: "std.workload_identity.scheduler", Issuer: "std.identity_issuer.runtime",
		PrincipalType: "std.type.workload_principal", ClaimsJSON: `{"workload":"scheduler"}`,
	}
}
