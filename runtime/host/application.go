package host

import (
	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativedurable"
	"scenery.sh/internal/runtimeapp"
)

func init() {
	nativecall.Bind(currentInternalBindings)
	runtimeapp.BindDBTrace(runtimeapp.DBTraceProvider{Start: TraceDBQueryStart, End: TraceDBQueryEnd})
	nativedurable.BindExecution(nativedurable.ExecutionProvider{
		Start: StartDurableTask, Wait: WaitDurableTask, Schedule: DurableSchedule,
		Dispatch: DispatchContractDurableExecutionWithOptions, DispatchAndWait: DispatchAndWaitContractDurableExecutionWithOptions,
	})
	runtimeapp.Bind(runtimeapp.Provider{
		Meta: Meta, CurrentRequest: CurrentRequest, StartSpan: StartSpan,
		CurrentAuth: CurrentAuth, WithAuthContext: WithAuthContext,
	})
}
