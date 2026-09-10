package build

import (
	"reflect"

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

type verifiedGoAnalysis struct {
	target gotarget.Context
	app    *model.App
}

// Analyses are invocation-local: they come from the complete implementation
// check immediately preceding preparation, never from a retained cache. Compare
// every target field rather than maintaining a weaker parallel cache key.
func matchingGoAnalysis(analyses []verifiedGoAnalysis, root, name string, target gotarget.Context) *model.App {
	for _, analysis := range analyses {
		if analysis.app != nil && analysis.app.Root == root && reflect.DeepEqual(analysis.target, target) {
			if analysis.app.Name == name {
				return analysis.app
			}
			// parse uses Name only as result metadata, not as a loader input.
			// Runtime generation uses config naming, which can differ from the
			// declaration name used by ABI checking. Keep that adaptation local.
			app := *analysis.app
			app.Name = name
			return &app
		}
	}
	return nil
}
