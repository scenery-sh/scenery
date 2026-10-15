package connectionruntime

import (
	"context"
	"database/sql"
	"sync"
)

type schemaCheck struct {
	done chan struct{}
	err  error
}

// Successful validation belongs to the service/pool lifetime. Cutover requires
// stopped writers; failed or canceled checks remain retryable on a later call.
type schemaChecks struct {
	mu    sync.Mutex
	pools map[*sql.DB]*schemaCheck
}

func (s *schemaChecks) ensure(ctx context.Context, pool *sql.DB, check func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if existing := s.pools[pool]; existing != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-existing.done:
			return existing.err
		}
	}
	if s.pools == nil {
		s.pools = make(map[*sql.DB]*schemaCheck)
	}
	current := &schemaCheck{done: make(chan struct{})}
	s.pools[pool] = current
	s.mu.Unlock()
	err := check()
	if err == nil {
		err = ctx.Err()
	}
	s.mu.Lock()
	current.err = err
	if err != nil {
		delete(s.pools, pool)
	}
	close(current.done)
	s.mu.Unlock()
	return err
}
