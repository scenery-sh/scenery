//go:build scenery_benchmark_stock

package build

// benchmarkStockGoBuild exists only in a separately tagged verifier binary.
// Released and ordinary source-built Scenery binaries never enable it.
const benchmarkStockGoBuild = true

func benchmarkFrameworkBuildFlags() []string { return []string{"-tags=scenery_benchmark_stock"} }
