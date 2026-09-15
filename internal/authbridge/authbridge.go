package authbridge

import (
	"fmt"
	"sync"
)

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

// DataCodec carries one supported authentication data type across a Scenery
// process boundary. Encode reports false for values of other types.
type DataCodec struct {
	Kind   string
	Encode func(any) ([]byte, bool, error)
	Decode func([]byte) (any, error)
}

var codecs struct {
	mu     sync.RWMutex
	byKind map[string]DataCodec
}

func RegisterDataCodec(codec DataCodec) {
	codecs.mu.Lock()
	defer codecs.mu.Unlock()
	if codecs.byKind == nil {
		codecs.byKind = map[string]DataCodec{}
	}
	codecs.byKind[codec.Kind] = codec
}

// EncodeData returns the registered kind and encoding of data.
func EncodeData(data any) (string, []byte, bool, error) {
	codecs.mu.RLock()
	defer codecs.mu.RUnlock()
	for kind, codec := range codecs.byKind {
		raw, ok, err := codec.Encode(data)
		if err != nil || ok {
			return kind, raw, ok, err
		}
	}
	return "", nil, false, nil
}

// DecodeData restores data encoded under a registered kind.
func DecodeData(kind string, raw []byte) (any, error) {
	codecs.mu.RLock()
	codec, ok := codecs.byKind[kind]
	codecs.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("authentication data kind %q is not available in this process", kind)
	}
	return codec.Decode(raw)
}
