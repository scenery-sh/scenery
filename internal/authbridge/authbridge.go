package authbridge

import "sync"

type Provider struct {
	UserID      func() (string, bool)
	Data        func() any
	CurrentData func() (any, bool)
	TenantID    func(any) (string, bool)
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
