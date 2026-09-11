package store

import (
	"context"

	"scenery.sh/internal/nativedurable"
)

// StepContext attaches this job's native persistence owner without replacing
// any caller context values, cancellation or deadline.
func (s *Store) StepContext(ctx context.Context, jobID string) context.Context {
	return nativedurable.WithStepStore(ctx, jobID, nativeSteps{s})
}

type nativeSteps struct{ db *Store }

func (adapter nativeSteps) Load(ctx context.Context, jobID, key string) ([]byte, bool, error) {
	step, ok, err := adapter.db.GetStep(ctx, jobID, key)
	return step.ResultBlob, ok && step.State == "succeeded", err
}

func (adapter nativeSteps) Save(ctx context.Context, jobID, key, state string, result, failure []byte) error {
	return adapter.db.SaveStep(ctx, jobID, key, state, "json", result, failure)
}
