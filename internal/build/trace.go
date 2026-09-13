package build

import (
	"context"
	"sync"
	"time"
)

// Step describes one measured interval. Callers project it into their existing
// event stream; this package neither stores traces nor adds a second logger.
type Step struct {
	OperationID               string
	Name                      string
	StartedAt                 time.Time
	Duration                  time.Duration
	QueueDuration             time.Duration
	Cache                     string
	Reason                    string
	OK                        bool
	Actions                   int
	CacheHits                 int
	CacheMisses               int
	FilesWritten              int
	FilesRemoved              int
	BytesWritten              int64
	WrittenPaths              []string
	RemovedPaths              []string
	PackagesRebuilt           []string
	PackagesRebuiltAvailable  bool
	ExecutableBytes           int64
	SnapshotDigest            string
	ContractRevision          string
	ImplementationRevision    string
	BuildInputDigest          string
	FrameworkSourceDigest     string
	FrameworkExecutableDigest string
	GoTarget                  string
}

type traceEmitter struct {
	operationID string
	emit        func(Step)
	mu          sync.Mutex
}

type traceKey struct{}

func WithTrace(ctx context.Context, emit func(Step)) context.Context {
	return WithTraceOperation(ctx, "", emit)
}

// WithTraceOperation correlates overlapping intervals from one build request.
// The operation ID is diagnostic only and never participates in build or
// ownership identity.
func WithTraceOperation(ctx context.Context, operationID string, emit func(Step)) context.Context {
	return context.WithValue(ctx, traceKey{}, &traceEmitter{operationID: operationID, emit: emit})
}

// RecordStep projects one already measured interval into the active build
// trace. It is safe for parallel producers and does not retain trace history.
func RecordStep(ctx context.Context, step Step) {
	trace, ok := ctx.Value(traceKey{}).(*traceEmitter)
	if !ok || trace == nil || trace.emit == nil {
		return
	}
	if step.OperationID == "" {
		step.OperationID = trace.operationID
	}
	step.WrittenPaths = append([]string(nil), step.WrittenPaths...)
	step.RemovedPaths = append([]string(nil), step.RemovedPaths...)
	step.PackagesRebuilt = append([]string(nil), step.PackagesRebuilt...)
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.emit(step)
}

func finishStep(ctx context.Context, name string, started time.Time, cache, reason string, err error) {
	RecordStep(ctx, Step{Name: name, StartedAt: started, Duration: time.Since(started), Cache: cache, Reason: reason, OK: err == nil})
}

func recordWorkspaceMaterialization(ctx context.Context, started time.Time, mutation workspaceMutation, err error) {
	cache, reason := "hit", "workspace_bytes_unchanged"
	if mutation.cacheMisses > 0 {
		cache, reason = "partial", "workspace_bytes_changed"
	}
	RecordStep(ctx, Step{
		Name: "workspace.materialize", StartedAt: started, Duration: time.Since(started), Cache: cache, Reason: reason, OK: err == nil,
		Actions: mutation.filesWritten + mutation.filesRemoved, CacheHits: mutation.cacheHits, CacheMisses: mutation.cacheMisses,
		FilesWritten: mutation.filesWritten, FilesRemoved: mutation.filesRemoved, BytesWritten: mutation.bytesWritten,
		WrittenPaths: mutation.writtenPaths, RemovedPaths: mutation.removedPaths,
	})
}

func observeBuild[T any](ctx context.Context, name string, fn func() (T, error)) (T, error) {
	started := time.Now()
	value, err := fn()
	finishStep(ctx, name, started, "not_applicable", "executed", err)
	return value, err
}

func observeBuildAction(ctx context.Context, name string, fn func() error) error {
	_, err := observeBuild(ctx, name, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}
