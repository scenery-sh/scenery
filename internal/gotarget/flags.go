package gotarget

import "strings"

// WithTrimpath places -trimpath ahead of the configured Go build flags unless
// they already decide it. Scenery-owned compilation and Go package analysis
// pass the result, so compiled packages never name the private workspace,
// owned module generation or framework snapshot directory: identical source
// shares Go build cache entries across app roots, worktrees and snapshots
// instead of filling the cache once per path, and analysis export data is the
// same cache entry the build links.
func WithTrimpath(flags []string) []string {
	for _, flag := range flags {
		if flag == "-trimpath" || strings.HasPrefix(flag, "-trimpath=") {
			return flags
		}
	}
	return append([]string{"-trimpath"}, flags...)
}
