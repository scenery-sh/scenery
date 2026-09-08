package devprocess

import (
	"context"
	"os/exec"
	"time"
)

// MonitorStartup prioritizes the supervisor's startup result over an exit-only
// failure, draining the private result pipe before reporting an observed exit.
// The caller owns protocol decoding and the user-facing exit diagnostic.
func MonitorStartup(ctx context.Context, cancel context.CancelCauseFunc, startup <-chan error, exited <-chan error, exitFailure func(error) error) {
	for {
		select {
		case err := <-startup:
			startup = nil
			if err != nil {
				cancel(err)
				return
			}
		default:
		}
		select {
		case err := <-startup:
			startup = nil
			if err != nil {
				cancel(err)
				return
			}
		case waitErr := <-exited:
			if startup != nil {
				if err := <-startup; err != nil {
					cancel(err)
					return
				}
			}
			cancel(exitFailure(waitErr))
			return
		case <-ctx.Done():
			return
		}
	}
}

func StopDetached(cmd *exec.Cmd, exited <-chan struct{}) {
	select {
	case <-exited:
		return
	default:
	}
	_ = InterruptTree(cmd)
	select {
	case <-exited:
	case <-time.After(DefaultStopTimeout):
		_ = KillTree(cmd)
		select {
		case <-exited:
		case <-time.After(time.Second):
		}
	}
}
