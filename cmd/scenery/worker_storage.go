package main

import (
	"context"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

func configureWorkerDevelopmentStorage(ctx context.Context, root string, cfg app.Config) error {
	environment, err := storageCapabilityEnv(ctx, root, cfg, nil, nil, "")
	if err != nil {
		return err
	}
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}
