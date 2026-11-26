package auth

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"sync"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

//go:embed db/gen/schema.sql
var standardAuthSchema embed.FS

// StandardSchemaSQL returns the standard-auth schema for deployment hosts.
func StandardSchemaSQL() (string, error) {
	data, err := standardAuthSchema.ReadFile("db/gen/schema.sql")
	return string(data), err
}

const standardAuthInitializationTimeout = 30 * time.Second

var standardAuthState struct {
	mu      sync.Mutex
	enabled bool
	svc     *Service
	once    sync.Once
	err     error
}

// UseStandard lets a worker read the records of the host's standard
// authentication: CurrentUser, CurrentMembership and MembershipOf. The host
// owns the authentication endpoints, sessions and schema.
func UseStandard() {
	standardAuthState.mu.Lock()
	standardAuthState.enabled = true
	standardAuthState.mu.Unlock()
}

func standardAuthEnabled() bool {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	return standardAuthState.enabled
}

func standardAuthService(ctx context.Context) (*Service, error) {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	standardAuthState.once.Do(func() {
		// Standard auth is process state. A canceled first request must not poison
		// its one-time database initialization for the lifetime of the process.
		ctx, cancel := standardAuthInitializationContext(ctx)
		defer cancel()

		databaseURL := strings.TrimSpace(envpolicy.Get("DATABASE_URL"))
		if strings.TrimSpace(databaseURL) == "" {
			standardAuthState.err = fmt.Errorf("standard auth database URL is not configured (DATABASE_URL)")
			return
		}
		authURL, err := postgresdb.ServiceURL(databaseURL, "scenery")
		if err != nil {
			standardAuthState.err = fmt.Errorf("standard auth database URL must be postgres:// or postgresql://: %w", err)
			return
		}
		pool, err := postgresdb.Open(ctx, authURL)
		if err != nil {
			standardAuthState.err = fmt.Errorf("connect standard auth database: %w", err)
			return
		}
		standardAuthState.svc = &Service{db: pool, query: authdb.New(pool), now: time.Now}
		if host := appsdk.CurrentHost(); host != nil {
			host.MarkServiceInitialized("auth", func(context.Context) {
				_ = pool.Close()
			})
		}
	})
	return standardAuthState.svc, standardAuthState.err
}

func standardAuthInitializationContext(requestCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(requestCtx), standardAuthInitializationTimeout)
}
