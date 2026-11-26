package workerabi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
)

type capabilityScope struct {
	context context.Context
	service string
	token   string
}

// Scopes binds Host capabilities to the worker and context admitted by the
// Host. The worker cannot supply a principal, tenant, or generation.
type Scopes struct {
	mu     sync.RWMutex
	active map[string]capabilityScope
}

func (s *Scopes) Open(ctx context.Context, service, workerToken string) (id string, release func()) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.active == nil {
		s.active = make(map[string]capabilityScope)
	}
	for {
		id = rand.Text()
		if _, exists := s.active[id]; !exists {
			break
		}
	}
	s.active[id] = capabilityScope{context: ctx, service: service, token: workerToken}
	s.mu.Unlock()
	return id, func() {
		cancel()
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
	}
}

// Handler authenticates a capability request before dispatch to the supplied
// service-scoped router. Its context retains Host values and is canceled by
// either side closing the request or the Host revoking the invocation.
func (s *Scopes) Handler(next func(service string, w http.ResponseWriter, request *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, CapabilityPrefix) {
			http.NotFound(w, request)
			return
		}
		id := request.Header.Values(ScopeHeader)
		auth := request.Header.Values("Authorization")
		if len(id) != 1 || len(auth) != 1 {
			http.Error(w, "invocation scope rejected", http.StatusUnauthorized)
			return
		}
		s.mu.RLock()
		scope, exists := s.active[id[0]]
		s.mu.RUnlock()
		token, bearer := strings.CutPrefix(auth[0], "Bearer ")
		if !exists || !bearer || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(scope.token)) != 1 || scope.context.Err() != nil {
			http.Error(w, "invocation scope rejected", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithCancel(scope.context)
		defer cancel()
		stop := context.AfterFunc(request.Context(), cancel)
		defer stop()
		next(scope.service, w, request.WithContext(ctx))
	})
}
