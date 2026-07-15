package edge

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// EscapePlistString escapes a value for embedding in launchd plist XML.
func EscapePlistString(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	).Replace(value)
}

// ParseHelperLaunchStatus parses `launchctl print` output into the top-level
// service state and pid.
func ParseHelperLaunchStatus(output string) (string, int, error) {
	var state string
	var pid int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if state == "" && strings.HasPrefix(line, "state = ") {
			state = strings.TrimSpace(strings.TrimPrefix(line, "state = "))
		}
		if pid == 0 && strings.HasPrefix(line, "pid = ") {
			parsed, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
			if err == nil {
				pid = parsed
			}
		}
	}
	return state, pid, nil
}

// ParseHelperPlistProgramArguments extracts the ProgramArguments string array
// from an edge helper launchd plist.
func ParseHelperPlistProgramArguments(data []byte) ([]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	expectProgramArguments := false
	inProgramArguments := false
	var args []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch item := token.(type) {
		case xml.StartElement:
			switch item.Name.Local {
			case "key":
				var key string
				if err := decoder.DecodeElement(&key, &item); err != nil {
					return nil, err
				}
				expectProgramArguments = key == "ProgramArguments"
			case "array":
				if expectProgramArguments {
					inProgramArguments = true
					expectProgramArguments = false
				}
			case "string":
				if inProgramArguments {
					var value string
					if err := decoder.DecodeElement(&value, &item); err != nil {
						return nil, err
					}
					args = append(args, value)
				}
			}
		case xml.EndElement:
			if item.Name.Local == "array" && inProgramArguments {
				return args, nil
			}
		}
	}
	return nil, fmt.Errorf("edge helper plist missing ProgramArguments")
}

// HelperRunArguments returns the arguments that follow the
// `system edge privileged-helper run` invocation inside plist
// ProgramArguments.
func HelperRunArguments(args []string) ([]string, error) {
	for i := 0; i+3 < len(args); i++ {
		if args[i] == "system" && args[i+1] == "edge" && args[i+2] == "privileged-helper" && args[i+3] == "run" {
			return args[i+4:], nil
		}
	}
	return nil, fmt.Errorf("edge helper plist does not run scenery system edge privileged-helper run")
}
