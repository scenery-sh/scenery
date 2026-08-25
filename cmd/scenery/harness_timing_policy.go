package main

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	harnessTestClassFast                = "fast"
	harnessTestClassIntegration         = "integration"
	harnessFastTestTargetSeconds        = 0.060
	harnessFastTestBudgetSeconds        = 0.100
	harnessIntegrationTestTargetSeconds = 0.100
	harnessIntegrationTestBudgetSeconds = 3.000
	harnessTimingConfirmationPercentile = 95
)

// harnessTestTimingException preserves the published timing-report shape.
// The policy must remain empty: external-boundary proof belongs in the release
// harness, never in a top-level Go test root.
type harnessTestTimingException struct {
	Package        string  `json:"package"`
	Name           string  `json:"name"`
	Class          string  `json:"class"`
	TargetSeconds  float64 `json:"target_seconds"`
	BudgetSeconds  float64 `json:"budget_seconds"`
	BoundaryReason string  `json:"classification_reason"`
}

// harnessTimingIntegrationExceptionPolicy remains serialized in timing reports
// for schema stability. Any entry is a contract violation.
var harnessTimingIntegrationExceptionPolicy = []harnessTestTimingException{}

func harnessTimingIntegrationExceptions() []harnessTestTimingException {
	exceptions := append([]harnessTestTimingException{}, harnessTimingIntegrationExceptionPolicy...)
	for i := range exceptions {
		exceptions[i].Class = harnessTestClassIntegration
		exceptions[i].TargetSeconds = harnessIntegrationTestTargetSeconds
		exceptions[i].BudgetSeconds = harnessIntegrationTestBudgetSeconds
	}
	if err := validateHarnessTimingIntegrationExceptions(exceptions); err != nil {
		panic(err)
	}
	return exceptions
}

func harnessTimingIntegrationException(packageName, testName string, exceptions []harnessTestTimingException) (harnessTestTimingException, bool) {
	for _, exception := range exceptions {
		if exception.Package == packageName && exception.Name == testName {
			return exception, true
		}
	}
	return harnessTestTimingException{}, false
}

func validateHarnessTimingIntegrationExceptions(exceptions []harnessTestTimingException) error {
	if len(exceptions) != 0 {
		return fmt.Errorf("integration timing exceptions are forbidden; move external-boundary proof to the release harness")
	}
	return nil
}

func isExactTopLevelGoTestRoot(name string) bool {
	if !strings.HasPrefix(name, "Test") || len(name) == len("Test") || strings.Contains(name, "/") {
		return false
	}
	for i, r := range name[len("Test"):] {
		if i == 0 && unicode.IsLower(r) {
			return false
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
