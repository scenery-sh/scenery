package main

import (
	"reflect"
	"regexp"
	"testing"
)

func TestBuildGoTestArgsDefaultsPackageParallelism(t *testing.T) {
	got := buildGoTestArgs(nil)
	want := []string{"test", "-json", "-p=4", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildGoTestArgs(nil) = %#v, want %#v", got, want)
	}
}

func TestUseDefaultShardsOnlyForDefaultInvocation(t *testing.T) {
	if !useDefaultShards(nil) {
		t.Fatal("default invocation should use sharded package timing")
	}
	if !useDefaultShards([]string{"--"}) {
		t.Fatal("separator-only invocation should use sharded package timing")
	}
	for _, args := range [][]string{
		{"./..."},
		{"-count=1"},
		{"-run", "TestBuildGoTestArgs"},
	} {
		if useDefaultShards(args) {
			t.Fatalf("useDefaultShards(%#v) = true, want false", args)
		}
	}
}

func TestShardedCmdSceneryRegexesCoverNameBucketsOnce(t *testing.T) {
	samples := []string{
		"TestAgentRouterTLSFlags",
		"TestEnsureTypeScriptWorkerDependenciesRunsBunInstallAndWritesMarker",
		"TestFindSceneryRepoRoot",
		"TestManagedElectricBackendsAndEnv",
		"TestSceneryTestRunsGoTestInGeneratedWorkspace",
		"TestPrepareDevAgentSessionDefaultsToUnixBackend",
		"TestRunSceneryCheckJSONSuccess",
		"TestStatusAndDownCommandsUseAgent",
		"TestTypeScriptWorkerEnvUsesTemporalAndSessionOverrides",
		"TestVictoriaEnabledDefaultsToTrue",
		"TestWriteDetachedDevResultJSON",
		"Test1SyntheticFutureName",
	}
	for _, sample := range samples {
		matches := 0
		for _, pattern := range shardedCmdSceneryDefaultRegexes {
			ok, err := regexp.MatchString(pattern, sample)
			if err != nil {
				t.Fatalf("invalid regex %q: %v", pattern, err)
			}
			if ok {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("%s matched %d shard regexes, want exactly 1", sample, matches)
		}
	}
}

func TestBuildGoTestArgsPreservesExplicitPackageParallelism(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "separate value",
			args: []string{"-p", "8", "./..."},
			want: []string{"test", "-json", "-p", "8", "./..."},
		},
		{
			name: "equals value",
			args: []string{"-p=8", "./..."},
			want: []string{"test", "-json", "-p=8", "./..."},
		},
		{
			name: "leading separator",
			args: []string{"--", "-run", "^$", "./..."},
			want: []string{"test", "-json", "-p=4", "-run", "^$", "./..."},
		},
		{
			name: "flags only default packages",
			args: []string{"-p=8"},
			want: []string{"test", "-json", "-p=8", "./..."},
		},
		{
			name: "fresh timing is explicit",
			args: []string{"-count=1", "-run", "^TestBuildGoTestArgs", "./scripts/testtimes"},
			want: []string{"test", "-json", "-p=4", "-count=1", "-run", "^TestBuildGoTestArgs", "./scripts/testtimes"},
		},
		{
			name: "run flag without package defaults packages",
			args: []string{"-run", "^TestBuildGoTestArgs"},
			want: []string{"test", "-json", "-p=4", "-run", "^TestBuildGoTestArgs", "./..."},
		},
		{
			name: "bool flag before package",
			args: []string{"-failfast", "./scripts/testtimes"},
			want: []string{"test", "-json", "-p=4", "-failfast", "./scripts/testtimes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildGoTestArgs(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildGoTestArgs(%#v) = %#v, want %#v", tt.args, got, tt.want)
			}
		})
	}
}
