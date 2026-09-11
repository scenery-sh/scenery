package main

// initialWatchScan belongs to one lifetime-lock acquisition. The owner joins
// it on every exit; no source observer or validation result survives startup.
type initialWatchScan struct {
	done     chan struct{}
	snapshot fileSnapshot
	err      error
}

func beginInitialWatchScan(scan func() (fileSnapshot, error)) *initialWatchScan {
	attempt := &initialWatchScan{done: make(chan struct{})}
	go func() {
		defer close(attempt.done)
		attempt.snapshot, attempt.err = scan()
	}()
	return attempt
}

func (s *initialWatchScan) wait() (fileSnapshot, error) {
	<-s.done
	return s.snapshot, s.err
}
