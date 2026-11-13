package main

import (
	"context"

	"scenery.sh/auth"
	"scenery.sh/errs"
	"scenery.sh/runtime"
)

type connectionWorkloads struct {
	owners    chan string
	schedules chan connectionAdmissionResult
}

func registerConnectionWorkloads() *connectionWorkloads {
	w := &connectionWorkloads{owners: make(chan string, 1), schedules: make(chan connectionAdmissionResult, 1)}
	token := func(ctx context.Context, owner string) connectionAdmissionResult {
		value, err := auth.ConnectionTokenForUser(ctx, owner, "independent", "files.read")
		return connectionAdmissionResult{Code: errs.Code(err), Issued: value == "independent-access"}
	}
	must(runtime.RegisterContractSchedule(runtime.ContractScheduleRegistration{
		Address: "probe/schedule/connections", Name: "connections", TriggerKind: "every", TriggerValue: "1s",
		Identity:             runtime.ContractWorkloadIdentity{Address: "std.workload_identity.connection_schedule", Issuer: "std.identity_issuer.runtime", PrincipalType: "std.type.workload_principal"},
		AuthorizationAddress: "std.authorization.scheduled", PipelineAddress: "std.pipeline.empty",
		Invoke: func(ctx context.Context) error {
			select {
			case owner := <-w.owners:
				w.schedules <- token(ctx, owner)
			default:
			}
			return nil
		},
	}))
	return w
}
