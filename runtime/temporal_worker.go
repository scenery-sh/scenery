package runtime

import "context"

type TemporalWorkerStarter func(context.Context, AppConfig) (func(context.Context) error, error)

var temporalWorkerStarter TemporalWorkerStarter

func RegisterTemporalWorkerStarter(starter TemporalWorkerStarter) {
	temporalWorkerStarter = starter
}

func startTemporalWorkerRuntime(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
	if temporalWorkerStarter == nil {
		return func(context.Context) error { return nil }, nil
	}
	return temporalWorkerStarter(ctx, cfg)
}
