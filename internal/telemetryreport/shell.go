package telemetryreport

import (
	"path"
	"strings"
)

// shellScan is what a report may conclude about one shell command string
// without running it. It is deliberately not a shell interpreter: it finds
// the words at command position, skipping quoted text, comments and here-doc
// bodies, and it recognizes a single simple command. Anything else, such as
// &&, ||, pipes, background jobs, subshells, substitutions or compound
// commands, only makes the string compound: which of its commands ran, and
// whose exit status and time the shell reported, is then unknown.
type shellScan struct {
	commands [][]string
	compound bool
}

// shellInvocation is one Scenery command at command position: the words after
// the executable, and whether the executable ran directly, so that its exit
// status and duration are the command's own.
type shellInvocation struct {
	args   []string
	direct bool
}

// shellReservedWords begin or end a compound command; the word after them is
// again at command position.
var shellReservedWords = map[string]bool{
	"!": true, "{": true, "}": true, "if": true, "then": true, "else": true, "elif": true, "fi": true,
	"while": true, "until": true, "do": true, "done": true, "case": true, "esac": true, "for": true, "select": true,
}

// scanShell splits a shell command string into the words of its commands.
func scanShell(source string) shellScan {
	var scan shellScan
	var command []string
	var word strings.Builder
	inWord := false
	redirectTarget := false // the next word is a redirection target
	heredoc := false        // that target is a here-doc delimiter
	var heredocs []string   // delimiters whose bodies follow the next newline
	flushWord := func() {
		if !inWord {
			return
		}
		text := word.String()
		word.Reset()
		inWord = false
		if redirectTarget {
			if heredoc {
				heredocs = append(heredocs, text)
			}
			redirectTarget, heredoc = false, false
			return
		}
		command = append(command, text)
	}
	endCommand := func() {
		flushWord()
		if len(command) > 0 {
			scan.commands = append(scan.commands, command)
			if shellReservedWords[command[0]] {
				scan.compound = true
			}
		}
		command = nil
	}
	control := func() {
		endCommand()
		scan.compound = true
	}
	runes := []rune(source)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		next := rune(0)
		if index+1 < len(runes) {
			next = runes[index+1]
		}
		switch {
		case char == ' ' || char == '\t':
			flushWord()
		case char == '\n':
			endCommand()
			// Here-doc bodies are data, not commands.
			for _, delimiter := range heredocs {
				for index+1 < len(runes) {
					end := index + 1
					for end < len(runes) && runes[end] != '\n' {
						end++
					}
					line := strings.TrimLeft(string(runes[index+1:end]), "\t")
					index = end
					if line == delimiter {
						break
					}
				}
			}
			heredocs = nil
		case char == '#' && !inWord:
			for index+1 < len(runes) && runes[index+1] != '\n' {
				index++
			}
		case char == '\'':
			inWord = true
			for index++; index < len(runes) && runes[index] != '\''; index++ {
				word.WriteRune(runes[index])
			}
		case char == '"':
			inWord = true
			for index++; index < len(runes) && runes[index] != '"'; index++ {
				switch {
				case runes[index] == '\\' && index+1 < len(runes):
					index++
				case runes[index] == '`' || runes[index] == '$' && index+1 < len(runes) && runes[index+1] == '(':
					scan.compound = true
				}
				word.WriteRune(runes[index])
			}
		case char == '\\':
			if next == '\n' {
				index++
				continue
			}
			if next != 0 {
				inWord = true
				word.WriteRune(next)
				index++
			}
		case char == '$' && next == '(':
			// A command substitution runs its own commands.
			control()
			index++
		case char == '$' && next == '{':
			inWord = true
			for ; index < len(runes) && runes[index] != '}'; index++ {
				word.WriteRune(runes[index])
			}
			word.WriteRune('}')
		case char == '`', char == '(', char == ')', char == '|':
			control()
			if char == '|' && (next == '|' || next == '&') {
				index++
			}
		case char == ';':
			endCommand()
		case char == '&' && next == '>':
			flushWord()
			index++
			if index+1 < len(runes) && runes[index+1] == '>' {
				index++
			}
			redirectTarget = true
		case char == '&':
			// && and a background job both leave the exit status unknown.
			control()
			if next == '&' {
				index++
			}
		case char == '<' || char == '>':
			// A file descriptor number belongs to the redirection.
			if inWord && strings.Trim(word.String(), "0123456789") == "" {
				word.Reset()
				inWord = false
			} else {
				flushWord()
			}
			operator := string(char)
			for index+1 < len(runes) && strings.ContainsRune("<>&|-", runes[index+1]) && len(operator) < 3 {
				index++
				operator += string(runes[index])
			}
			switch {
			case strings.HasSuffix(operator, "&") || operator == ">&-" || operator == "<&-":
				// A descriptor duplication such as 2>&1 names no file.
				for index+1 < len(runes) && strings.ContainsRune("0123456789-", runes[index+1]) {
					index++
				}
			case operator == "<<" || operator == "<<-":
				redirectTarget, heredoc = true, true
			default:
				redirectTarget = true
			}
		default:
			inWord = true
			word.WriteRune(char)
		}
	}
	endCommand()
	return scan
}

// sceneryInvocations returns the Scenery commands at command position.
func (scan shellScan) sceneryInvocations() []shellInvocation {
	var invocations []shellInvocation
	for _, words := range scan.commands {
		direct := true
		for len(words) > 0 && shellReservedWords[words[0]] {
			words = words[1:]
		}
		// Variable assignments and commands that run the next word.
		for len(words) > 0 {
			word := words[0]
			if name, _, ok := strings.Cut(word, "="); ok && name != "" && !strings.ContainsAny(name, "/-.") {
				words = words[1:]
				continue
			}
			switch word {
			case "env", "command", "exec", "time":
				words = words[1:]
				for len(words) > 0 && strings.HasPrefix(words[0], "-") {
					words = words[1:]
				}
				continue
			case "sudo", "nohup":
				// sudo may refuse to run the command at all.
				direct = false
				words = words[1:]
				for len(words) > 0 && strings.HasPrefix(words[0], "-") {
					words = words[1:]
				}
				continue
			}
			if word == "go" && len(words) > 2 && words[1] == "run" {
				// go run compiles first and reports its own exit status.
				direct = false
				words = words[2:]
				continue
			}
			break
		}
		if len(words) > 0 && path.Base(words[0]) == "scenery" {
			invocations = append(invocations, shellInvocation{args: words[1:], direct: direct})
		}
	}
	return invocations
}

// sceneryAttempts returns the Scenery commands a shell command attempts, each
// a known command with its subcommand for a family that has one, a root flag
// such as "--help", or "unknown <word>" for a word Scenery has no command
// for, and whether the shell command's outcome is the one Scenery command's
// own: it is one simple command that runs Scenery directly.
func sceneryAttempts(shell string, families map[string][]string) ([]string, bool) {
	scan := scanShell(shell)
	invocations := scan.sceneryInvocations()
	var result []string
	for _, invocation := range invocations {
		if len(invocation.args) == 0 {
			continue
		}
		word := invocation.args[0]
		switch subcommands, known := families[word]; {
		case known:
			command := word
			if len(invocation.args) > 1 {
				for _, sub := range subcommands {
					if sub == invocation.args[1] {
						command += " " + sub
						break
					}
				}
			}
			result = append(result, command)
		case strings.HasPrefix(word, "-") && len(word) <= 24:
			result = append(result, word)
		case commandWord.MatchString(word):
			result = append(result, "unknown "+word)
		}
	}
	attributable := !scan.compound && len(scan.commands) == 1 && len(invocations) == 1 && invocations[0].direct && len(result) == 1
	return result, attributable
}
