package testsuite

import (
	"encoding/json"
	"os"
	"sort"
)

var bootstrapTiming = map[string]float64{
	"scenery.sh/cmd/scenery":        100,
	"scenery.sh/internal/testsuite": 95,
	"scenery.sh/internal/edge":      90,
	"scenery.sh/internal/build":     80,
	"scenery.sh/internal/devdash":   70,
	"scenery.sh":                    60,
	"scenery.sh/storage":            50,
	"scenery.sh/runtime":            30,
}

func loadTimingEstimates(path string) map[string]float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var estimates map[string]float64
	if json.Unmarshal(data, &estimates) != nil {
		return nil
	}
	return estimates
}

func writeTimingEstimates(path string, estimates map[string]float64) error {
	data, err := json.Marshal(estimates)
	if err != nil {
		return err
	}
	return writeAtomic(path, data, 0o644)
}

func sortTestPackages(packages []testPackage, estimates map[string]float64) {
	priority := func(pkg string) float64 {
		if seconds := estimates[pkg]; seconds > 0 {
			return seconds
		}
		return bootstrapTiming[pkg]
	}
	sort.SliceStable(packages, func(i, j int) bool {
		left, right := priority(packages[i].ImportPath), priority(packages[j].ImportPath)
		if left == right {
			return packages[i].ImportPath < packages[j].ImportPath
		}
		return left > right
	})
}
