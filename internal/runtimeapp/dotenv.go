package runtimeapp

import (
	"sync"

	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
)

type dotenv struct {
	once sync.Once
	data map[string]string
	err  error
}

var processDotEnv dotenv

// LoadDotEnvIntoEnv shares one process-owned parse across SQL and auth consumers.
// It still checks current environment presence on every call before filling gaps.
func LoadDotEnvIntoEnv() error {
	return processDotEnv.load()
}

func (file *dotenv) load() error {
	file.once.Do(func() { file.data, file.err = envfile.ParseFile(".env") })
	if file.err != nil {
		return file.err
	}
	for key, value := range file.data {
		if _, exists := envpolicy.Lookup(key); exists {
			continue
		}
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}
