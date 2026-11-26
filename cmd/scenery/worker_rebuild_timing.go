package main

import "time"

type workerRebuildTiming struct {
	Generation     uint64   `json:"generation"`
	Declarations   bool     `json:"declarations"`
	Go             bool     `json:"go"`
	GoLinks        []string `json:"go_links,omitempty"`
	JS             []string `json:"js,omitempty"`
	Cargo          []string `json:"cargo,omitempty"`
	Rebuilt        []string `json:"rebuilt"`
	RebuiltModules []string `json:"rebuilt_modules"`
	SkippedModules []string `json:"skipped_modules"`
	Skipped        int      `json:"skipped"`
	DetectMS       float64  `json:"detect_ms"`
	ContractMS     float64  `json:"contract_ms"`
	GoMS           float64  `json:"go_ms"`
	JSMS           float64  `json:"js_ms"`
	CargoMS        float64  `json:"cargo_ms"`
	LinkMS         float64  `json:"link_ms"`
	CompileMS      float64  `json:"compile_ms"`
	ReadyMS        float64  `json:"ready_ms"`
	TotalMS        float64  `json:"total_ms"`
	EditToReadyMS  float64  `json:"edit_to_ready_ms,omitempty"`
	TriggerKind    string   `json:"trigger_kind,omitempty"`
}

func workerMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}
