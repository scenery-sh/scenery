package build

import (
	"strings"
)

var runtimeLinkerMetadataKeys = [...]string{
	"scenery.sh/runtime.linkedContractRevision",
	"scenery.sh/runtime.linkedImplementationRevision",
	"scenery.sh/runtime.linkedBuildInputDigest",
	"scenery.sh/runtime.linkedGoTarget",
}

// developmentLinkerFlags omit DWARF from ordinary development executables:
// it costs about 30 percent of a full-application link, while stack traces,
// panics and profiles read pclntab. Debuggers need DWARF, so configured linker
// flags follow this default and -ldflags=-w=false restores it.
const developmentLinkerFlags = "-w"

// developmentLinkerDefaults applies to ordinary development builds.
// Deployable artifacts keep the stock linker defaults.
func developmentLinkerDefaults(result *Result) string {
	if result != nil && result.Target != nil && result.Target.Role == "development" {
		return developmentLinkerFlags
	}
	return ""
}

func withRuntimeLinkerMetadata(flags []string, defaults string, values map[string]string) []string {
	result := make([]string, 0, len(flags)+1)
	ldflags := defaults
	for index := 0; index < len(flags); index++ {
		flag := flags[index]
		switch {
		case strings.HasPrefix(flag, "-ldflags="):
			ldflags += " " + strings.TrimPrefix(flag, "-ldflags=")
		case flag == "-ldflags" && index+1 < len(flags):
			index++
			ldflags += " " + flags[index]
		default:
			result = append(result, flag)
		}
	}
	if len(values) != 0 {
		for _, key := range runtimeLinkerMetadataKeys {
			ldflags += " -X=" + key + "=" + values[key]
		}
	}
	return append(result, "-ldflags="+strings.TrimSpace(ldflags))
}
