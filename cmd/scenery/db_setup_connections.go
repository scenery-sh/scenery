package main

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
)

// dbSetupConnections owns pools only for one setup invocation. Exact DSNs
// retain their schema/authority distinction; no connection or success result
// survives the invocation, a runtime replacement, or a database reset.
type dbSetupConnections struct {
	open func(context.Context, string) (*sql.DB, error)
	// mu guards the fields below; migration status of several services is
	// read concurrently.
	mu        sync.Mutex
	databases map[string]*sql.DB
	retained  map[string]bool
	opened    int
	reuses    int
}

func newDBSetupConnections() *dbSetupConnections {
	return &dbSetupConnections{open: openPostgresDatabase, databases: map[string]*sql.DB{}, retained: map[string]bool{}}
}

func (c *dbSetupConnections) Open(ctx context.Context, dsn string) (*sql.DB, error) {
	c.mu.Lock()
	if database := c.databases[dsn]; database != nil {
		c.reuses++
		c.mu.Unlock()
		return database, nil
	}
	c.mu.Unlock()
	// The connection is opened without the lock: it is network work.
	database, err := c.open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.databases[dsn]; existing != nil {
		c.reuses++
		_ = database.Close()
		return existing, nil
	}
	c.databases[dsn] = database
	c.opened++
	return database, nil
}

// Only pools needed by later seed work survive a migration plan. A large
// schema graph must not hold one idle connection per migration service.
func (c *dbSetupConnections) ReleaseMigration(dsn string) error {
	c.mu.Lock()
	if c.retained[dsn] {
		c.mu.Unlock()
		return nil
	}
	database := c.databases[dsn]
	delete(c.databases, dsn)
	c.mu.Unlock()
	if database != nil {
		return database.Close()
	}
	return nil
}

func (c *dbSetupConnections) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.databases))
	for key := range c.databases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var failures []error
	for _, key := range keys {
		failures = append(failures, c.databases[key].Close())
		delete(c.databases, key)
	}
	return errors.Join(failures...)
}

func (c *dbSetupConnections) SeedStore(ctx context.Context, dsn string) (databaseSeedStore, error) {
	database, err := c.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return borrowedSetupSeedStore{&postgresDatabaseSeedStore{db: database}}, nil
}

// The setup owner closes this shared pool after both migration and seed work.
type borrowedSetupSeedStore struct{ *postgresDatabaseSeedStore }

func (borrowedSetupSeedStore) Close(context.Context) error { return nil }
