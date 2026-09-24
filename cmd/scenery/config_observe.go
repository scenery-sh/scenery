package main

import (
	"context"

	"scenery.sh/internal/appconfig"
)

// observeLocalConfigApplication reports what this worktree's runtime applied,
// from the pin its supervisor keeps on the environment history, and how many
// other runtimes of the application run the desired revision. A missing pin
// means no runtime of this worktree is running; it is never reported as
// applied.
func observeLocalConfigApplication(_ context.Context, store *appconfig.Store, root, environment, desired string) configApplied {
	records, err := store.PinRecords(environment)
	if err != nil {
		return configApplied{State: "unobserved", Problem: err.Error()}
	}
	observed := configApplied{State: "not_running"}
	holder := devConfigHolder(root)
	others := &configOtherRuntimes{}
	for name, pin := range records {
		if name == holder {
			observed.Revision, observed.Problem = pin.Revision, pin.Problem
			switch {
			case pin.State == "rejected" && pin.Desired == desired:
				observed.State = "rejected"
			case pin.Revision == desired:
				observed.State = "applied"
			default:
				observed.State = "pending"
			}
			continue
		}
		if len(name) > len("worktree-") && name[:len("worktree-")] == "worktree-" {
			others.Total++
			if pin.Revision == desired {
				others.Applied++
			}
		}
	}
	observed.Others = others
	return observed
}
