package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

func newCLIFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

// parseCLIFlags keeps the CLI's existing interspersed-flag grammar while using
// the standard library for flag values, aliases, booleans, and --flag=value.
func parseCLIFlags(flags *flag.FlagSet, args []string) ([]string, error) {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
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
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
		flagArgs = append(flagArgs, arg)
		if hasValue || isBoolCLIFlag(registered) {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("missing value for %s", arg)
		}
		i++
		flagArgs = append(flagArgs, args[i])
	}
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positionals, nil
}

func parseLeadingCLIFlags(flags *flag.FlagSet, args []string) ([]string, error) {
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
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
		end++
		if !hasValue && !isBoolCLIFlag(registered) {
			if end >= len(args) {
				return nil, fmt.Errorf("missing value for %s", arg)
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

func rejectCLIFlag(flags *flag.FlagSet, name, message string) {
	flags.BoolFunc(name, "", func(string) error { return fmt.Errorf("%s", message) })
}

func rejectCLIPositionals(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("unknown argument %q", args[0])
}

func splitCLIPassthrough(args []string) (before, after []string, found bool) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}
