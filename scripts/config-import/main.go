// Command config-import is the one-time, explicitly invoked conversion of an
// operator's former dotenv file into one environment's Scenery configuration.
// It is not part of the product or any runtime: it reads exactly one file the
// operator names, never evaluates shell code, previews only names and
// destinations, validates every value before writing any, and writes through
// `scenery config set --stdin`, so values never appear in arguments or output.
//
// Usage:
//
//	go run ./scripts/config-import --file <dotenv> --env <name> [--app-root <path>]
//	    [--map NAME=key]... [--scenery <binary>] [--apply] [--overwrite]
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/compiler"
)

// defaultMappings are the framework inputs every application shares.
var defaultMappings = map[string]string{
	"JWT_SECRET":                 "auth.jwt_secret",
	"GOOGLE_OAUTH_CLIENT_ID":     "auth.google_client_id",
	"GOOGLE_OAUTH_CLIENT_SECRET": "auth.google_client_secret",
	"AUTH_TOKEN_CIPHER_KEY":      "auth.token_cipher_key",
	"AUTH_COOKIE_DOMAIN":         "auth.cookie_domain",
	"AUTH_EMAIL_FROM":            "auth.email_from",
	"OPENAI_API_KEY":             "assistant.openai_api_key",
	"DATABASE_URL":               "sql.database_url",
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var expansionPattern = regexp.MustCompile(`\$(\{|[A-Za-z_])`)

type entry struct {
	name  string
	value string
	// problem explains why the value cannot be converted without guessing.
	problem string
}

// parseDotenv reads KEY=VALUE lines. It supports optional `export`, single
// quotes (literal) and double quotes (Go escapes). Unquoted or double-quoted
// values that use `$` expansion, and unquoted values with an inline `#`, are
// reported instead of guessed.
func parseDotenv(data []byte) ([]entry, error) {
	var entries []entry
	seen := map[string]int{}
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if index == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, rawValue, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !namePattern.MatchString(name) {
			return nil, fmt.Errorf("line %d is not NAME=VALUE", index+1)
		}
		current := entry{name: name}
		rawValue = strings.TrimSpace(rawValue)
		switch {
		case len(rawValue) >= 2 && rawValue[0] == '\'' && rawValue[len(rawValue)-1] == '\'':
			current.value = rawValue[1 : len(rawValue)-1]
		case len(rawValue) >= 2 && rawValue[0] == '"' && rawValue[len(rawValue)-1] == '"':
			value, err := unquoteDouble(rawValue)
			if err != nil {
				current.problem = "malformed double-quoted value"
			}
			current.value = value
			if expansionPattern.MatchString(rawValue) {
				current.problem = "uses $ expansion; set this key manually"
			}
		default:
			current.value = rawValue
			if expansionPattern.MatchString(rawValue) {
				current.problem = "uses $ expansion; set this key manually"
			}
			if strings.Contains(rawValue, " #") {
				current.problem = "has an ambiguous inline comment; quote the value or set this key manually"
			}
		}
		if previous, duplicate := seen[name]; duplicate {
			entries[previous].problem = "is defined more than once; choose one value and set this key manually"
			continue
		}
		seen[name] = len(entries)
		entries = append(entries, current)
	}
	return entries, nil
}

func unquoteDouble(value string) (string, error) {
	var out strings.Builder
	for index := 1; index < len(value)-1; index++ {
		character := value[index]
		if character != '\\' {
			out.WriteByte(character)
			continue
		}
		index++
		if index >= len(value)-1 {
			return "", errors.New("dangling escape")
		}
		switch value[index] {
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		case '"', '\\', '$':
			out.WriteByte(value[index])
		default:
			return "", fmt.Errorf("unsupported escape \\%c", value[index])
		}
	}
	return out.String(), nil
}

type catalogInput struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	Sensitive bool   `json:"sensitive"`
	State     string `json:"state"`
	Source    string `json:"source"`
}

type plannedWrite struct {
	name, key, typ, status string
	sensitive              bool
	value                  string
}

type mappingFlags map[string]string

func (m mappingFlags) String() string { return "" }
func (m mappingFlags) Set(text string) error {
	name, key, ok := strings.Cut(text, "=")
	if !ok || !namePattern.MatchString(name) || key == "" {
		return fmt.Errorf("--map takes NAME=key")
	}
	m[name] = key
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "config-import:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("config-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "the one dotenv file to convert")
	environment := flags.String("env", "", "the Scenery environment to configure")
	appRoot := flags.String("app-root", "", "application root")
	scenery := flags.String("scenery", "scenery", "Scenery CLI to write through")
	apply := flags.Bool("apply", false, "write the converted values")
	overwrite := flags.Bool("overwrite", false, "replace keys the environment already configures")
	mappings := mappingFlags{}
	flags.Var(mappings, "map", "NAME=key mapping (repeatable); framework names map by default")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *environment == "" || flags.NArg() != 0 {
		return errors.New("usage: config-import --file <dotenv> --env <name> [--map NAME=key]... [--apply]")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	entries, err := parseDotenv(data)
	if err != nil {
		return fmt.Errorf("%s: %w", *file, err)
	}
	command := func(stdin io.Reader, arguments ...string) ([]byte, error) {
		if *appRoot != "" {
			arguments = append(arguments, "--app-root", *appRoot)
		}
		var output bytes.Buffer
		cmd := exec.Command(*scenery, arguments...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &output, stderr
		err := cmd.Run()
		return output.Bytes(), err
	}
	shown, err := command(nil, "config", "show", "--env", *environment, "-o", "json")
	if err != nil {
		return fmt.Errorf("read the %s catalog: %w", *environment, err)
	}
	var envelope struct {
		Data struct {
			Inputs []catalogInput `json:"inputs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(shown, &envelope); err != nil {
		return fmt.Errorf("decode the %s catalog: %w", *environment, err)
	}
	catalog := map[string]catalogInput{}
	for _, input := range envelope.Data.Inputs {
		catalog[input.Key] = input
	}
	typed, err := compiledCatalog(*appRoot)
	if err != nil {
		return err
	}
	plan, blocked := planWrites(entries, catalog, mappings, *overwrite)
	for index := range plan {
		if plan[index].status != "ready" || plan[index].sensitive {
			continue
		}
		input, _ := typed.Lookup(plan[index].key)
		if _, err := appconfig.ParseText(input, plan[index].value); err != nil {
			plan[index].status, blocked = "invalid for its type: "+strings.ReplaceAll(err.Error(), plan[index].value, "<value>"), true
		}
	}
	renderPlan(stdout, plan)
	if !*apply {
		_, _ = fmt.Fprintln(stdout, "preview only; rerun with --apply to write the ready keys")
		return nil
	}
	if blocked {
		return errors.New("resolve every conflict before --apply; nothing was written")
	}
	for _, write := range plan {
		if write.status != "ready" {
			continue
		}
		if _, err := command(strings.NewReader(write.value), "config", "set", write.key, "--env", *environment, "--stdin", "-o", "json"); err != nil {
			return fmt.Errorf("set %s: %w; earlier keys were written, rerun to continue", write.key, err)
		}
		_, _ = fmt.Fprintf(stdout, "set %s\n", write.key)
	}
	return nil
}

// planWrites maps names to keys. It is blocked when any mapped name cannot
// be converted without guessing or collides with another.
func planWrites(entries []entry, catalog map[string]catalogInput, mappings map[string]string, overwrite bool) ([]plannedWrite, bool) {
	blocked := false
	targets := map[string]string{}
	var plan []plannedWrite
	for _, current := range entries {
		key, mapped := mappings[current.name]
		if !mapped {
			key, mapped = defaultMappings[current.name]
		}
		write := plannedWrite{name: current.name, key: key, value: current.value}
		input, declared := catalog[key]
		write.typ, write.sensitive = input.Type, input.Sensitive
		switch {
		case !mapped:
			write.status, write.key = "unmapped (ignored)", "-"
		case !declared:
			write.status = "not a configuration input of this application"
			blocked = true
		case current.problem != "":
			write.status = current.problem
			blocked = true
		case targets[key] != "":
			write.status = "maps to the same key as " + targets[key]
			blocked = true
		case input.Source == "environment" && !overwrite:
			write.status = "already configured (skipped; --overwrite replaces it)"
		default:
			write.status = "ready"
		}
		if mapped {
			targets[key] = current.name
		}
		plan = append(plan, write)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].name < plan[j].name })
	return plan, blocked
}

func renderPlan(w io.Writer, plan []plannedWrite) {
	for _, write := range plan {
		kind := write.typ
		if write.sensitive {
			kind += " (secret)"
		}
		_, _ = fmt.Fprintf(w, "%-32s -> %-36s %-28s %s\n", write.name, write.key, kind, write.status)
	}
}

// compiledCatalog compiles the application to validate values with their
// declared types and constraints before anything is written.
func compiledCatalog(appRoot string) (appconfig.Catalog, error) {
	start := appRoot
	if start == "" {
		var err error
		if start, err = os.Getwd(); err != nil {
			return appconfig.Catalog{}, err
		}
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return appconfig.Catalog{}, err
	}
	result, err := compiler.Compile(root)
	if err != nil {
		return appconfig.Catalog{}, err
	}
	if !result.Valid() {
		return appconfig.Catalog{}, errors.New("the application does not compile; run scenery check")
	}
	return appconfig.BuildCatalog(result.Manifest, appconfig.FrameworkOptions{StandardAuth: cfg.Auth.Enabled, GoogleOAuth: cfg.Auth.Enabled && cfg.Auth.GoogleOAuth.Enabled, SQL: len(result.SQLRequirements) > 0})
}
