package runtime

import "scenery.sh/internal/appsdk"

// LoadDotEnvIntoEnv sets each variable of the working directory's .env file
// that the process environment does not already define.
func LoadDotEnvIntoEnv() error {
	return appsdk.LoadDotEnv()
}
