package appsdk

import (
	"strings"
	"sync"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime/shared"
)

var metadata = struct {
	sync.RWMutex
	value shared.AppMetadata
}{value: shared.AppMetadata{Environment: defaultEnvironment()}}

func SetMetadata(value shared.AppMetadata) {
	metadata.Lock()
	metadata.value = value
	metadata.Unlock()
}

func SetPublicBaseURL(value string) {
	metadata.Lock()
	metadata.value.APIBaseURL = value
	metadata.Unlock()
}

func Metadata() *shared.AppMetadata {
	metadata.RLock()
	value := metadata.value
	metadata.RUnlock()
	return &value
}

func DefaultEnvironment() shared.Environment {
	return defaultEnvironment()
}

func defaultEnvironment() shared.Environment {
	if strings.EqualFold(strings.TrimSpace(envpolicy.Get("SCENERY_RUNTIME_ENV")), "test") {
		return shared.Environment{Name: "test", Type: shared.EnvTest, Cloud: shared.CloudLocal}
	}
	return shared.Environment{Name: "local", Type: shared.EnvDevelopment, Cloud: shared.CloudLocal}
}
