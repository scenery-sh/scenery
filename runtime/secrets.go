package runtime

import (
	"sync"

	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
)

var (
	dotEnvOnce sync.Once
	dotEnvData map[string]string
	dotEnvErr  error
)

func LoadDotEnvIntoEnv() error {
	dotEnvOnce.Do(func() { dotEnvData, dotEnvErr = envfile.ParseFile(".env") })
	if dotEnvErr != nil {
		return dotEnvErr
	}
	for key, value := range dotEnvData {
		if _, exists := envpolicy.Lookup(key); exists {
			continue
		}
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}
