package authbridge

import "sync"

type Provider struct {
	StandardIdentity func(any) (*StandardIdentity, bool)
	UserID           func() (string, bool)
	Data             func() any
	CurrentData      func() (any, bool)
	TenantID         func(any) (string, bool)
}

var providers struct {
	mu       sync.RWMutex
	provider Provider
}

func Register(provider Provider) {
	providers.mu.Lock()
	providers.provider = provider
	providers.mu.Unlock()
}

func CurrentData() (any, bool) {
	provider := current()
	if provider.CurrentData == nil {
		return nil, false
	}
	return provider.CurrentData()
}

func TenantID(data any) (string, bool) {
	provider := current()
	if provider.TenantID == nil {
		return "", false
	}
	return provider.TenantID(data)
}

func current() Provider {
	providers.mu.RLock()
	defer providers.mu.RUnlock()
	return providers.provider
}

// StandardAuth is the supported principal shape in the first experiment.
// Custom native auth data is rejected, not coerced into generic JSON.
type StandardIdentity struct {
	UserID          string `json:"user_id"`
	TenantID        string `json:"tenant_id"`
	SessionID       string `json:"session_id"`
	ActorUserID     string `json:"actor_user_id"`
	ImpersonationID string `json:"impersonation_id"`
}

func ExportStandardIdentity(data any) (*StandardIdentity, bool) {
	provider := current()
	if provider.StandardIdentity == nil {
		return nil, false
	}
	return provider.StandardIdentity(data)
}
