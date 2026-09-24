package main

import (
	"fmt"
	"io"
	"strings"
)

func renderConfigShow(w io.Writer, result configShowResult) {
	authority := "this machine"
	if result.Authority == "target" {
		authority = "target " + result.Target
	}
	_, _ = fmt.Fprintf(w, "%s / %s (authority: %s)\n", result.AppID, result.Environment, authority)
	_, _ = fmt.Fprintf(w, "desired revision %s", result.DesiredRevision)
	switch result.Applied.State {
	case "applied", "active":
		_, _ = fmt.Fprintf(w, ", applied %s", result.Applied.Revision)
	case "":
	default:
		_, _ = fmt.Fprintf(w, ", runtime %s", strings.ReplaceAll(result.Applied.State, "_", " "))
		if result.Applied.Revision != "" {
			_, _ = fmt.Fprintf(w, " (%s)", result.Applied.Revision)
		}
	}
	_, _ = fmt.Fprintln(w)
	if result.Applied.Problem != "" {
		_, _ = fmt.Fprintf(w, "runtime: %s\n", result.Applied.Problem)
	}
	for _, input := range result.Inputs {
		value := "-"
		switch {
		case input.Sensitive:
			value = "<secret " + strings.ReplaceAll(input.SecretNote, "_", " ") + ">"
		case len(input.Value) > 0:
			value = string(input.Value)
		}
		_, _ = fmt.Fprintf(w, "  %-40s %-10s %-10s %s", input.Key, input.State, input.Type, value)
		if input.Problem != "" {
			_, _ = fmt.Fprintf(w, "  (%s)", input.Problem)
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(w, "missing required: %s\n", strings.Join(result.Missing, ", "))
	}
	if len(result.Unused) > 0 {
		_, _ = fmt.Fprintf(w, "unused stored keys (declared by another revision): %s\n", strings.Join(result.Unused, ", "))
	}
}

func renderConfigChange(w io.Writer, result configChangeResult) {
	if !result.Changed {
		_, _ = fmt.Fprintf(w, "%s unchanged in %s (revision %s)\n", result.Key, result.Environment, result.Revision)
		return
	}
	verb := "set"
	if result.Operation == "unset" {
		verb = "unset"
	}
	_, _ = fmt.Fprintf(w, "%s %s in %s: revision %s -> %s\n", verb, result.Key, result.Environment, result.PreviousRevision, result.Revision)
	_, _ = fmt.Fprintf(w, "%s\n", result.Application)
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(w, "still missing: %s\n", strings.Join(result.Missing, ", "))
	}
}
