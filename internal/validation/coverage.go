package validation

import (
	"slices"
	"strings"
)

// PathCoverage distinguishes planned checks from executed proof. Manual owner
// lanes and unmatched paths remain unverified even when every selected step passes.
type PathCoverage struct {
	Path           string   `json:"path"`
	Status         string   `json:"status"`
	Profiles       []string `json:"profiles,omitempty"`
	StepIDs        []string `json:"step_ids,omitempty"`
	ManualProfiles []string `json:"manual_profiles,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}

func (p Planner) changedCoverage(files []string, plan ResolvedPlan) []PathCoverage {
	coverage := make([]PathCoverage, 0, len(files))
	for _, file := range files {
		item := PathCoverage{Path: file, Status: "unverified", Reason: "No configured profile covers this changed path; select its owner lane or declare a reasoned exemption."}
		for _, match := range plan.Selection.MatchedProfiles {
			if !slices.Contains(match.MatchedFiles, file) {
				continue
			}
			item.Profiles = append(item.Profiles, match.Profile)
			if p.Config.Validation.Profiles[match.Profile].Manual {
				item.ManualProfiles = append(item.ManualProfiles, match.Profile)
				continue
			}
			owners := p.profileClosure(match.Profile, map[string]bool{})
			for _, step := range plan.Steps {
				if owners[step.Profile] && !slices.Contains(item.StepIDs, step.ID) {
					item.StepIDs = append(item.StepIDs, step.ID)
				}
			}
		}
		switch {
		case len(item.ManualProfiles) > 0:
			item.Reason = "Owner-selected lanes were not executed: " + strings.Join(item.ManualProfiles, ", ")
		case len(item.StepIDs) > 0:
			item.Status, item.Reason = "planned", "Selected checks have not executed."
		case len(item.Profiles) == 0:
			for _, exemption := range p.Config.Validation.Exemptions {
				for _, pattern := range exemption.Paths {
					if globMatches(pattern, file) && strings.TrimSpace(exemption.Reason) != "" {
						item.Status, item.Reason = "exempt", exemption.Reason
						break
					}
				}
				if item.Status == "exempt" {
					break
				}
			}
		}
		coverage = append(coverage, item)
	}
	return coverage
}

func (p Planner) profileClosure(name string, seen map[string]bool) map[string]bool {
	if seen[name] {
		return seen
	}
	seen[name] = true
	for _, step := range p.Config.Validation.Profiles[name].Steps {
		if ref := ParseStepRef(step); ref.Kind == "profile" {
			p.profileClosure(ref.Name, seen)
		}
	}
	return seen
}

func coverageComplete(coverage []PathCoverage) bool {
	for _, item := range coverage {
		if item.Status != "checked" && item.Status != "exempt" {
			return false
		}
	}
	return true
}

func finishCoverage(result *Result) {
	result.Selection.Coverage = slices.Clone(result.Selection.Coverage)
	passed := map[string]bool{}
	for _, step := range result.Steps {
		passed[step.ID] = step.OK
	}
	for i := range result.Selection.Coverage {
		item := &result.Selection.Coverage[i]
		if item.Status == "planned" {
			item.Status, item.Reason = "checked", ""
			for _, id := range item.StepIDs {
				if !passed[id] {
					item.Status, item.Reason = "unverified", "A required check failed or did not execute."
					break
				}
			}
		}
		if item.Status == "unverified" {
			action := item.Path + ": " + item.Reason
			if !slices.Contains(result.NextActions, action) {
				result.NextActions = append(result.NextActions, action)
			}
		}
	}
	result.Selection.CoverageComplete = coverageComplete(result.Selection.Coverage)
	result.OK = result.OK && result.Selection.CoverageComplete
}
