package main

import (
	"context"
)

// observeLocalConfigApplication reports the configuration revision this
// worktree's running runtime applied.
func observeLocalConfigApplication(ctx context.Context, root, environment string) configApplied {
	return configApplied{State: "unobserved"}
}
