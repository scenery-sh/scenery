package feature

import (
	"fmt"
	"path/filepath"
	"scenery.sh/internal/envpolicy"
	"slices"
	"strings"
)

// ReadPolicy reads repository-authored argv; an absent policy never waives checks.
func ReadPolicy(root string) (Policy, error) {
	var policy Policy
	if err := readJSON(filepath.Join(root, PolicyFile), &policy); err != nil {
		return policy, fmt.Errorf("read required feature policy: %w", err)
	}
	if policy.Version != 1 || policy.ProbeLimit < 1 || policy.ProbeLimit > 16 || len(policy.Landing) == 0 {
		return policy, fmt.Errorf("feature policy requires version 1, probe_limit 1..16 and nonempty landing checks")
	}
	for _, checks := range [][]Check{policy.Development, policy.Landing} {
		seen := map[string]bool{}
		for _, check := range checks {
			if !namePattern.MatchString(check.ID) || strings.TrimSpace(check.Command) == "" || seen[check.ID] {
				return policy, fmt.Errorf("invalid or duplicate feature check %q", check.ID)
			}
			if check.Expensive && check.Reuse {
				return policy, fmt.Errorf("external/expensive check %q cannot reuse past execution", check.ID)
			}
			seen[check.ID] = true
		}
	}
	return policy, nil
}

func checkUnion(policies ...Policy) []Check {
	result := []Check{}
	seen := map[string]bool{}
	for _, policy := range policies {
		for _, check := range policy.Landing {
			key := digest(check)
			if !seen[key] {
				result = append(result, check)
				seen[key] = true
			}
		}
	}
	return result
}

func selectChecks(checks []Check, paths []string) []Check {
	result := []Check{}
	for _, check := range checks {
		selected := len(check.WhenPaths) == 0
		for _, prefix := range check.WhenPaths {
			for _, path := range paths {
				if strings.HasPrefix(path, prefix) {
					selected = true
				}
			}
		}
		if selected {
			result = append(result, check)
		}
	}
	return result
}

func checkArgs(check Check, base, revision string) []string {
	result := []string{check.Command}
	for _, arg := range check.Args {
		result = append(result, strings.NewReplacer("{base}", base, "{revision}", revision).Replace(arg))
	}
	return result
}

func commandEnvironment() string {
	values := envpolicy.Environ()
	slices.Sort(values)
	return digest(values)
}
