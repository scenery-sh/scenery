package generate

import (
	"fmt"
	"sort"
	"strings"
)

// Share exact repeated failure unions without widening any operation's names.
// Leading underscores keep private aliases separate from generated public names.
func renderTSFailureSets(resources, bindings []Resource) (string, map[string]string) {
	counts := map[string]int{}
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" {
			failures := renderTSFailureVariants(tsTransportOutcomeVariants(resource, bindings))
			if failures != "" {
				counts[failures]++
			}
		}
	}
	var sets []string
	for failures, count := range counts {
		if count > 1 {
			sets = append(sets, failures)
		}
	}
	sort.Strings(sets)
	aliases := make(map[string]string, len(sets))
	var source strings.Builder
	for i, failures := range sets {
		alias := fmt.Sprintf("_SceneryFailureSet%d", i)
		aliases[failures] = alias
		fmt.Fprintf(&source, "type %s =\n%s;\n\n", alias, failures)
	}
	return source.String(), aliases
}

func renderTSFailureVariants(variants []tsOutcomeVariant) string {
	var source strings.Builder
	for _, variant := range variants {
		if variant.Kind == "failure" {
			source.WriteString(renderTSOutcomeVariant(variant))
		}
	}
	return source.String()
}

func renderTSOutcomeVariant(variant tsOutcomeVariant) string {
	return fmt.Sprintf("  | { readonly kind: %q; readonly name: %q; readonly %s: %s }\n", variant.Kind, variant.Name, variant.Field, variant.Type)
}
