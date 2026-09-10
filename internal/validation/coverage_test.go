package validation

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"scenery.sh/internal/app"
)

func coveragePlanner() Planner {
	return Planner{Config: app.Config{Validation: app.ValidationConfig{
		Default: "quick",
		Profiles: map[string]app.ValidationProfileConfig{
			"quick":        {Steps: []string{"check"}},
			"go":           {Paths: []string{"service/**/*.go"}, Steps: []string{"test:go"}},
			"web":          {Paths: []string{"apps/web/**"}, Steps: []string{"check"}},
			"blog":         {Paths: []string{"apps/blog/**/*.astro"}, Steps: []string{"check"}},
			"ui":           {Paths: []string{"apps/ui/**"}, Steps: []string{"check", "profile:web"}},
			"dependencies": {Paths: []string{"./package.json", "./bun.lock"}, Steps: []string{"profile:web", "profile:blog", "profile:ui"}},
			"native":       {Description: "House native owner lane", Manual: true, Paths: []string{"house/native/**"}, Steps: []string{"test:go"}},
		},
		Exemptions: []app.ValidationExemptionConfig{{Paths: []string{"LICENSE"}, Reason: "License text has no executable behavior."}},
	}}}
}

func TestChangedCoverageAccountsForEveryPathAndConsumer(t *testing.T) {
	previous := CollectChangedFiles
	defer func() { CollectChangedFiles = previous }()
	cases := []struct {
		name, path, status string
		profiles, owners   []string
	}{
		{"root dependencies", "package.json", "planned", []string{"dependencies"}, []string{"web", "blog", "ui"}},
		{"lock", "bun.lock", "planned", []string{"dependencies"}, []string{"web", "blog", "ui"}},
		{"native", "house/native/roof.cpp", "unverified", []string{"native"}, nil},
		{"astro", "apps/blog/src/index.astro", "planned", []string{"blog"}, []string{"blog"}},
		{"shared UI", "apps/ui/Button.tsx", "planned", []string{"ui"}, []string{"ui", "web"}},
		{"new package", "newservice/handler.go", "unverified", nil, nil},
		{"new frontend", "apps/new/package.json", "unverified", nil, nil},
		{"exemption", "LICENSE", "exempt", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			CollectChangedFiles = func(context.Context, string, string) ([]string, error) { return []string{tc.path}, nil }
			plan, err := coveragePlanner().Plan(context.Background(), PlanRequest{Changed: true, Base: "main"})
			if err != nil || len(plan.Diagnostics) != 0 || len(plan.Selection.Coverage) != 1 {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			coverage := plan.Selection.Coverage[0]
			if coverage.Path != tc.path || coverage.Status != tc.status || !slices.Equal(coverage.Profiles, tc.profiles) {
				t.Fatalf("coverage=%+v", coverage)
			}
			for _, owner := range tc.owners {
				found := false
				for _, step := range plan.Steps {
					found = found || step.Profile == owner && slices.Contains(coverage.StepIDs, step.ID)
				}
				if !found {
					t.Fatalf("consumer %s absent from %+v", owner, coverage)
				}
			}
			if tc.name == "native" && (slices.Contains(plan.Profiles, "native") || !slices.Equal(coverage.ManualProfiles, []string{"native"})) {
				t.Fatal("manual native lane was executed or hidden")
			}
			result := ExecutePlan(context.Background(), plan, func(context.Context, PlanStep, io.Writer, io.Writer) error { return nil }, nil)
			wantOK := tc.status != "unverified"
			if result.OK != wantOK || result.Selection.CoverageComplete != wantOK {
				t.Fatalf("result=%+v", result)
			}
			if tc.status == "planned" && (result.Selection.Coverage[0].Status != "checked" || plan.Selection.Coverage[0].Status != "planned") {
				t.Fatal("executed evidence missing or mutated the dry-run plan")
			}
		})
	}
}

func TestChangedCoverageFailureAndMixedChangesCannotHideUnexecutedChecks(t *testing.T) {
	previous := CollectChangedFiles
	defer func() { CollectChangedFiles = previous }()
	CollectChangedFiles = func(context.Context, string, string) ([]string, error) {
		return []string{"service/handler.go", "apps/web/main.ts", "house/native/roof.cpp", "unknown/file.go"}, nil
	}
	plan, err := coveragePlanner().Plan(context.Background(), PlanRequest{Changed: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		result := ExecutePlan(context.Background(), plan, func(_ context.Context, step PlanStep, _, _ io.Writer) error {
			if fail && step.Profile == "go" {
				return errors.New("test failure")
			}
			return nil
		}, nil)
		if result.OK || result.Selection.CoverageComplete || len(result.Selection.Coverage) != 4 {
			t.Fatalf("result=%+v", result)
		}
		for _, item := range result.Selection.Coverage {
			if item.Status == "planned" || item.Status == "exempt" {
				t.Fatalf("missing final status: %+v", item)
			}
			if fail && item.Status == "checked" {
				t.Fatalf("fail-fast left behavioral check unexecuted: %+v", item)
			}
		}
	}
}

func TestManualValidationRequiresExplicitSelectionAndReasonedExemptions(t *testing.T) {
	p := coveragePlanner()
	plan, err := p.NamedPlan("native", Selection{Mode: "explicit"})
	if err != nil || len(plan.Diagnostics) != 0 || len(plan.Steps) != 1 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	result := ExecutePlan(context.Background(), plan, func(context.Context, PlanStep, io.Writer, io.Writer) error { return nil }, nil)
	if !result.OK {
		t.Fatalf("explicit owner lane failed: %+v", result)
	}
	quick := p.Config.Validation.Profiles["quick"]
	quick.Steps = append(quick.Steps, "profile:native")
	p.Config.Validation.Profiles["quick"] = quick
	p.Config.Validation.Exemptions[0].Reason = ""
	if len(p.ValidateConfig()) < 2 {
		t.Fatal("automatic manual-lane reference or empty exemption reason accepted")
	}
}
