package main

import (
	"fmt"
	"strings"
)

func nativeBuildCohortSummary(samples []nativeBuildDriverSample) map[string]any {
	result := map[string]any{"sample_count": len(samples), "complete_pairs": len(samples) / 2}
	backends := []string{"stock"}
	for _, sample := range samples {
		if sample.Backend != "stock" {
			backends = append(backends, sample.Backend)
			break
		}
	}
	for _, backend := range backends {
		var capture, archive, support, artifact, finalization, compile, link, handling, scheduler, launch, activation, build, buildToResponse, response, accepted []float64
		for _, sample := range samples {
			if sample.Backend == backend && sample.OK {
				capture = append(capture, sample.CaptureMS)
				archive = append(archive, sample.ArchiveValidationMS)
				support = append(support, sample.SupportValidationMS)
				artifact = append(artifact, sample.ArtifactBuildMS)
				finalization = append(finalization, sample.BackendFinalizationMS)
				compile = append(compile, sample.CompileMS)
				link = append(link, sample.LinkMS)
				handling = append(handling, sample.ArtifactHandlingMS)
				scheduler = append(scheduler, sample.SchedulerDelayMS)
				launch = append(launch, sample.FirstLaunchMS)
				activation = append(activation, sample.ActivationMS)
				build = append(build, sample.AccountableBuildMS)
				buildToResponse = append(buildToResponse, sample.FirstVerifiedResponseMS-sample.AccountableBuildMS)
				response = append(response, sample.FirstVerifiedResponseMS)
				accepted = append(accepted, sample.AcceptedEditMS)
			}
		}
		result[backend] = map[string]any{"count": len(build), "capture": nativeReloadStats(capture), "archive_validation": nativeReloadStats(archive), "support_validation": nativeReloadStats(support), "artifact_build": nativeReloadStats(artifact), "backend_finalization": nativeReloadStats(nonZeroFloats(finalization)), "compile": nativeReloadStats(nonZeroFloats(compile)), "link": nativeReloadStats(nonZeroFloats(link)), "artifact_handling": nativeReloadStats(nonZeroFloats(handling)), "scheduler_delay": nativeReloadStats(nonZeroFloats(scheduler)), "first_launch_attestation": nativeReloadStats(nonZeroFloats(launch)), "runtime_activation": nativeReloadStats(nonZeroFloats(activation)), "accountable_build": nativeReloadStats(build), "build_to_first_verified_response": nativeReloadStats(buildToResponse), "first_verified_response": nativeReloadStats(response), "accepted_edit": nativeReloadStats(accepted)}
	}
	return result
}

func nativeBuildComparisonDeltas(summary map[string]any, candidate string) map[string]any {
	stock, stockOK := summary["stock"].(map[string]any)
	changed, changedOK := summary[candidate].(map[string]any)
	result := map[string]any{}
	if !stockOK || !changedOK {
		return result
	}
	for _, metric := range []string{"capture", "archive_validation", "support_validation", "artifact_build", "backend_finalization", "compile", "link", "artifact_handling", "scheduler_delay", "first_launch_attestation", "runtime_activation", "accountable_build", "build_to_first_verified_response", "first_verified_response", "accepted_edit"} {
		stockStats, stockExists := stock[metric].(map[string]any)
		changedStats, changedExists := changed[metric].(map[string]any)
		if !stockExists || !changedExists {
			continue
		}
		entry := map[string]any{}
		for _, quantile := range []string{"p50_ms", "p95_ms", "mean_ms"} {
			stockValue, stockValueOK := stockStats[quantile].(float64)
			changedValue, changedValueOK := changedStats[quantile].(float64)
			if !stockValueOK || !changedValueOK {
				continue
			}
			delta, relative := stockValue-changedValue, float64(0)
			if stockValue != 0 {
				relative = delta / stockValue
			}
			entry[quantile] = map[string]any{"stock_ms": stockValue, "candidate_ms": changedValue, "absolute_reduction_ms": delta, "relative_reduction": relative}
		}
		result[metric] = entry
	}
	return result
}

func nativeBuildCheckpoint(summary map[string]any, candidate string) map[string]any {
	changed, ok := summary[candidate].(map[string]any)
	if !ok {
		return map[string]any{"met": false, "reason": "candidate summary absent"}
	}
	value := func(metric string) float64 {
		stats, _ := changed[metric].(map[string]any)
		result, _ := stats["p50_ms"].(float64)
		return result
	}
	build, launch, response := value("accountable_build"), value("first_launch_attestation"), value("build_to_first_verified_response")
	return map[string]any{
		"build_p50_ms": build, "build_target_ms": 200.0, "build_met": build <= 200,
		"first_launch_attestation_phase_p50_ms": launch, "launch_ready_target_ms": 100.0, "launch_ready_result": "not_comparable",
		"derived_build_to_response_p50_ms": response, "build_to_response_target_ms": 250.0, "build_to_response_result": "not_comparable",
		"result": "not_comparable", "reason": "current runtime events expose aggregate candidate.preflight and runtime.activation phases, not separate process creation, executable loading, Go initialization, construction, attestation, and readiness timestamps",
	}
}

func nativeBuildExecutionMatrix(aggregate map[string]any, spec nativeBuildExperimentSpec) []map[string]any {
	rows := []map[string]any{
		{"capture": "full", "execution": "stock", "status": "unperformed"},
		{"capture": "full", "execution": "retained_driver", "status": "unperformed"},
		{"capture": "retained", "execution": "stock", "status": "unperformed"},
		{"capture": "retained", "execution": "retained_driver", "status": "unperformed"},
	}
	if stock, ok := aggregate["stock"].(map[string]any); ok {
		rows[0]["status"], rows[0]["measurements"] = "performed", stock
	}
	if candidate, ok := aggregate[spec.candidate].(map[string]any); ok {
		index := 1
		if spec.candidateMode == "compiler" {
			index = 3
		}
		rows[index]["status"], rows[index]["measurements"] = "performed", candidate
	}
	return rows
}

func nativeBuildEconomics(summary, aggregate map[string]any, candidate string) map[string]any {
	var stockPrepare, candidatePrepare, retainedBytes []float64
	if bootstraps, ok := summary["bootstraps"].(map[string]any); ok {
		for name, raw := range bootstraps {
			value, _ := raw.(map[string]any)
			prepare, _ := value["lane_prepare_ms"].(float64)
			if strings.HasSuffix(name, "-stock") {
				stockPrepare = append(stockPrepare, prepare)
			} else if strings.HasSuffix(name, "-"+candidate) {
				candidatePrepare = append(candidatePrepare, prepare)
				if bytes, ok := value["retained_bytes"].(float64); ok {
					retainedBytes = append(retainedBytes, bytes)
				}
			}
		}
	}
	mean := func(values []float64) float64 {
		var sum float64
		for _, value := range values {
			sum += value
		}
		if len(values) == 0 {
			return 0
		}
		return sum / float64(len(values))
	}
	stock, _ := aggregate["stock"].(map[string]any)
	changed, _ := aggregate[candidate].(map[string]any)
	stockBuild, _ := stock["accountable_build"].(map[string]any)
	changedBuild, _ := changed["accountable_build"].(map[string]any)
	stockMean, _ := stockBuild["mean_ms"].(float64)
	changedMean, _ := changedBuild["mean_ms"].(float64)
	extraBootstrap, savings := mean(candidatePrepare)-mean(stockPrepare), stockMean-changedMean
	breakEven := float64(0)
	if extraBootstrap > 0 && savings > 0 {
		breakEven = extraBootstrap / savings
	}
	return map[string]any{
		"stock_prepare_mean_ms": mean(stockPrepare), "candidate_prepare_mean_ms": mean(candidatePrepare), "candidate_extra_prepare_ms": extraBootstrap,
		"steady_state_accountable_build_savings_mean_ms": savings, "break_even_edits": breakEven,
		"candidate_retained_bytes_mean": mean(retainedBytes), "interpretation": "break-even uses measured lane preparation delta divided by measured per-edit accountable build savings; zero means no positive amortizable cost or savings",
	}
}

func nonZeroFloats(values []float64) []float64 {
	result := values[:0]
	for _, value := range values {
		if value > 0 {
			result = append(result, value)
		}
	}
	return result
}

func nativeBuildCandidateDecision(summary map[string]any, candidate string) string {
	for cohort := 1; cohort <= 2; cohort++ {
		value, ok := summary[fmt.Sprintf("cohort_%d", cohort)].(map[string]any)
		if !ok || value["complete_pairs"] != 30 {
			return "incomplete"
		}
		stock := value["stock"].(map[string]any)
		driver := value[candidate].(map[string]any)
		s50 := stock["accountable_build"].(map[string]any)["p50_ms"].(float64)
		d50 := driver["accountable_build"].(map[string]any)["p50_ms"].(float64)
		s95 := stock["accountable_build"].(map[string]any)["p95_ms"].(float64)
		d95 := driver["accountable_build"].(map[string]any)["p95_ms"].(float64)
		sa95 := stock["accepted_edit"].(map[string]any)["p95_ms"].(float64)
		da95 := driver["accepted_edit"].(map[string]any)["p95_ms"].(float64)
		sa50 := stock["accepted_edit"].(map[string]any)["p50_ms"].(float64)
		da50 := driver["accepted_edit"].(map[string]any)["p50_ms"].(float64)
		if s50-d50 < 100 || (s50-d50)/s50 < .25 || d95 > s95*1.05 || da50 >= sa50 || da95 > sa95*1.05 {
			return "no_go_current_candidate"
		}
	}
	return "go_for_next_experiment"
}
