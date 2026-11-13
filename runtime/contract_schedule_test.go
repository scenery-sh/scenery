package runtime

import (
	"context"
	"testing"
	"time"

	"scenery.sh/errs"
)

func TestContractScheduleRegistersTypedCronInvocation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	called := false
	if err := RegisterContractSchedule(ContractScheduleRegistration{
		Address: "house/schedule/nightly", Name: "nightly", TriggerKind: "every", TriggerValue: "24h",
		Overlap: "queue", CatchupMaximumAge: 10 * time.Minute, Identity: testWorkloadIdentity(),
		AuthorizationAddress: "std.authorization.scheduled", PipelineAddress: "std.pipeline.empty",
		Invoke: func(ctx context.Context) error {
			called = true
			if auth := CurrentAuth(); auth == nil || auth.UID != "workload:std.workload_identity.scheduler" {
				t.Fatalf("auth = %#v", auth)
			}
			if _, err := (sdkHost{}).ConnectionToken(ctx, "owner", "files", nil); errs.Code(err) != errs.ConnectionNotConfigured {
				t.Fatalf("admitted schedule owner selection: %v", err)
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	jobs := listCronJobs()
	if len(jobs) != 1 || jobs[0].Every != 24*time.Hour || jobs[0].OverlapPolicy != "buffer_all" || jobs[0].CatchupWindow != 10*time.Minute {
		t.Fatalf("jobs = %#v", jobs)
	}
	if err := safeInvokeCronJob(withCronInvocation(context.Background(), time.Now().UTC(), "schedule-test"), jobs[0]); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("schedule did not invoke operation")
	}
}

func testWorkloadIdentity() ContractWorkloadIdentity {
	return ContractWorkloadIdentity{
		Address: "std.workload_identity.scheduler", Issuer: "std.identity_issuer.runtime",
		PrincipalType: "std.type.workload_principal", ClaimsJSON: `{"workload":"scheduler"}`,
	}
}
