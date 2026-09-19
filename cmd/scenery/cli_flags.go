package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// usageErrorf reports a request the caller wrote wrongly: an unknown command,
// flag or argument, a missing or malformed value, or flags that do not belong
// together. It exits 2 and is rendered as SCN8001 with its message, so the
// caller sees what to correct; an unclassified error is an internal failure
// whose message is withheld.
func usageErrorf(format string, args ...any) error {
	return &codedCLIError{err: fmt.Errorf(format, args...), code: 2}
}

// preconditionErrorf reports a correct request that the current state refuses,
// such as a runtime that must stop first. It exits 3.
func preconditionErrorf(format string, args ...any) error {
	return &codedCLIError{err: fmt.Errorf(format, args...), code: 3}
}

// unavailableErrorf reports a capability this host does not offer, such as a
// command of another platform or a missing tool. It exits 4.
func unavailableErrorf(format string, args ...any) error {
	return &codedCLIError{err: fmt.Errorf(format, args...), code: 4}
}

// flagBeforeWord reports a flag standing where a command expects a word such
// as its subcommand, which otherwise reads as an unknown subcommand named "-o".
func flagBeforeWord(args []string, word string) error {
	if len(args) > 0 && args[0] != "-" && strings.HasPrefix(args[0], "-") {
		return usageErrorf("expected %s before %q; flags follow it", word, args[0])
	}
	return nil
}

func newCLIFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

// parseCLIFlags keeps the CLI's existing interspersed-flag grammar while using
// the standard library for flag values, aliases, booleans, and --flag=value.
func parseCLIFlags(flags *flag.FlagSet, args []string) (positionals []string, err error) {
	defer func() {
		if err != nil {
			err = &codedCLIError{err: err, code: 2}
		}
	}()
	ensureCLIOutputFlag(flags)
	flagArgs := make([]string, 0, len(args))
	positionals = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}

		nameValue := strings.TrimLeft(arg, "-")
		name, _, hasValue := strings.Cut(nameValue, "=")
		registered := flags.Lookup(name)
		if registered == nil {
			return nil, usageErrorf("unknown flag %q", arg)
		}
		flagArgs = append(flagArgs, arg)
		if hasValue || isBoolCLIFlag(registered) {
			continue
		}
		if i+1 >= len(args) || isCLIFlagToken(flags, args[i+1]) {
			return nil, usageErrorf("missing value for %s", arg)
		}
		i++
		flagArgs = append(flagArgs, args[i])
	}
	if err := flags.Parse(flagArgs); err != nil {
		return nil, cliFlagValueError(err)
	}
	return positionals, nil
}

// isCLIFlagToken reports whether a token names a registered flag, which is
// never the value of the flag before it; such a value is written --flag=value.
func isCLIFlagToken(flags *flag.FlagSet, token string) bool {
	if token == "-" || token == "--" || !strings.HasPrefix(token, "-") {
		return false
	}
	name, _, _ := strings.Cut(strings.TrimLeft(token, "-"), "=")
	return flags.Lookup(name) != nil
}

// cliFlagValueError renders the flag package's value error in the CLI's own
// words: a flag's own explanation alone, or the rejected value and its flag.
func cliFlagValueError(err error) error {
	head, reason, ok := strings.Cut(err.Error(), ": ")
	value, name, found := strings.Cut(strings.TrimPrefix(head, "invalid value "), " for flag -")
	if !ok || !found || !strings.HasPrefix(head, "invalid value ") {
		return err
	}
	if reason != "parse error" && reason != "value out of range" {
		return errors.New(reason)
	}
	dashes := "--"
	if len(name) == 1 {
		dashes = "-"
	}
	return fmt.Errorf("invalid value %s for %s%s", value, dashes, name)
}

func ensureCLIOutputFlag(flags *flag.FlagSet) {
	if flags.Lookup("o") == nil {
		flags.String("o", "human", "")
	}
}

func registerJSONOutput(flags *flag.FlagSet, enabled *bool) {
	flags.Func("o", "", func(value string) error {
		if value != "human" && value != "json" {
			return usageErrorf("unsupported output %q; use human or json", value)
		}
		*enabled = value == "json"
		return nil
	})
}

func registerJSONLinesOutput(flags *flag.FlagSet, enabled *bool) {
	flags.Func("o", "", func(value string) error {
		if value != "human" && value != "jsonl" {
			return usageErrorf("unsupported output %q; use human or jsonl", value)
		}
		*enabled = value == "jsonl"
		return nil
	})
}

// registerJSONOrLinesOutput serves a command whose answer is one JSON document
// unless JSON Lines are requested.
func registerJSONOrLinesOutput(flags *flag.FlagSet, lines *bool) {
	flags.Func("o", "", func(value string) error {
		if value != "human" && value != "json" && value != "jsonl" {
			return usageErrorf("unsupported output %q; use json or jsonl", value)
		}
		*lines = value == "jsonl"
		return nil
	})
}

func parseLeadingCLIFlags(flags *flag.FlagSet, args []string) ([]string, error) {
	ensureCLIOutputFlag(flags)
	end := 0
	for end < len(args) {
		arg := args[end]
		if arg == "--" {
			end++
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		registered := flags.Lookup(name)
		if registered == nil {
			return nil, usageErrorf("unknown flag %q", arg)
		}
		end++
		if !hasValue && !isBoolCLIFlag(registered) {
			if end >= len(args) || isCLIFlagToken(flags, args[end]) {
				return nil, usageErrorf("missing value for %s", arg)
			}
			end++
		}
	}
	if _, err := parseCLIFlags(flags, args[:end]); err != nil {
		return nil, err
	}
	return args[end:], nil
}

func isBoolCLIFlag(value *flag.Flag) bool {
	boolFlag, ok := value.Value.(interface{ IsBoolFlag() bool })
	return ok && boolFlag.IsBoolFlag()
}

func cliFlagSet(flags *flag.FlagSet, names ...string) bool {
	set := false
	flags.Visit(func(found *flag.Flag) {
		for _, name := range names {
			if found.Name == name {
				set = true
			}
		}
	})
	return set
}

func rejectCLIPositionals(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return usageErrorf("unexpected argument %q", args[0])
}

func splitCLIPassthrough(args []string) (before, after []string, found bool) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}
