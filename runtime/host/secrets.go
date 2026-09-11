package host

import "scenery.sh/internal/runtimeapp"

func LoadDotEnvIntoEnv() error {
	return runtimeapp.LoadDotEnvIntoEnv()
}
