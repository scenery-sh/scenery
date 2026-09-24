package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// isHelpFlag reports whether argument asks for help.
func isHelpFlag(argument string) bool {
	return argument == "-h" || argument == "--help"
}

// helpRequestTopics reports whether args ask for help on a built-in command:
// `-h` or `--help` anywhere before a `--` terminator. The topics name the
// longest command path the help catalog knows, followed by `-o json` when JSON
// output was requested. A help request never runs the command.
func helpRequestTopics(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	requested := false
	for _, argument := range args {
		if argument == "--" {
			break
		}
		if isHelpFlag(argument) {
			requested = true
			break
		}
	}
	if !requested {
		return nil, false
	}
	if isHelpFlag(args[0]) {
		return withoutHelpFlags(args[1:]), true
	}
	if !slices.Contains(builtinCommandNames(), args[0]) {
		return nil, false
	}
	var words []string
	for _, argument := range args {
		if argument == "--" || strings.HasPrefix(argument, "-") {
			break
		}
		words = append(words, argument)
	}
	// Operands such as a key or an SSH target are not part of the path: the
	// longest prefix that names a known command is.
	for len(words) > 1 {
		if _, ok := findHelpCommand(words); ok {
			break
		}
		words = words[:len(words)-1]
	}
	if requestedCLIOutput(args) == "json" {
		words = append(words, "-o", "json")
	}
	return words, true
}

func withoutHelpFlags(args []string) []string {
	result := make([]string, 0, len(args))
	for _, argument := range args {
		if !isHelpFlag(argument) {
			result = append(result, argument)
		}
	}
	return result
}

// builtinCommandNames lists the top-level commands the help catalog
// advertises.
func builtinCommandNames() []string {
	var names []string
	for _, entry := range helpCommands {
		name, _, _ := strings.Cut(entry.Command, " ")
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// unknownSubcommandError refuses a subcommand that a command's help entry does
// not list, when every usage line of that command starts with one of its
// listed subcommands, so the list is known to be complete. It returns nil for
// any other request, which the command parses itself.
func unknownSubcommandError(args []string) error {
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return nil
	}
	for _, entry := range helpCommands {
		if entry.Command != args[0] || !helpSubcommandsComplete(entry) || slices.Contains(entry.Subcommands, args[1]) {
			continue
		}
		return usageErrorf("unknown %s command %q; %s", entry.Command, args[1], unknownWordHint(args[1], entry.Subcommands, "scenery help "+entry.Command))
	}
	return nil
}

func helpSubcommandsComplete(entry helpCommandEntry) bool {
	if len(entry.Subcommands) == 0 {
		return false
	}
	for _, usage := range entry.Usage {
		rest := strings.Fields(strings.TrimPrefix(usage, "scenery "+entry.Command))
		if len(rest) == 0 {
			return false
		}
		for _, word := range strings.Split(rest[0], "|") {
			if !slices.Contains(entry.Subcommands, word) {
				return false
			}
		}
	}
	return true
}

// unknownWordHint tells what to do about an unknown word: the closest
// candidates when some are close, and the help command that lists them all.
func unknownWordHint(word string, candidates []string, help string) string {
	if suggestion := didYouMean(word, candidates); suggestion != "" {
		return suggestion + " Run `" + help + "` for all of them."
	}
	return "run `" + help + "`"
}

// didYouMean suggests the candidates closest to an unknown word as a
// question, or returns "" when none is close. A suggestion is only printed,
// never run.
func didYouMean(word string, candidates []string) string {
	type match struct {
		name     string
		distance int
	}
	var matches []match
	limit := 2
	if len(word) <= 3 {
		limit = 1
	}
	for _, candidate := range candidates {
		distance := editDistance(word, candidate)
		if distance <= limit || (len(word) >= 3 && strings.HasPrefix(candidate, word)) {
			matches = append(matches, match{candidate, distance})
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].distance < matches[j].distance })
	if len(matches) > 3 {
		matches = matches[:3]
	}
	quoted := make([]string, len(matches))
	for index, candidate := range matches {
		quoted[index] = fmt.Sprintf("%q", candidate.name)
	}
	return "did you mean " + strings.Join(quoted, " or ") + "?"
}

// editDistance is the optimal string alignment distance: insertions,
// deletions, substitutions and transpositions of adjacent characters.
func editDistance(a, b string) int {
	previous2 := make([]int, len(b)+1)
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				current[j] = min(current[j], previous2[j-2]+1)
			}
		}
		previous2, previous, current = previous, current, previous2
	}
	return previous[len(b)]
}
