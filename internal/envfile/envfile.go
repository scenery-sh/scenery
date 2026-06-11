package envfile

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func ParseFile(path string) (map[string]string, error) {
	data := make(map[string]string)
	file, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for lineNo, raw := range strings.Split(string(file), "\n") {
		line := strings.TrimSpace(raw)
		if lineNo == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(after)
		}
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid .env line %d", lineNo+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid empty .env key on line %d", lineNo+1)
		}
		value, err := parseValue(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("parse .env line %d: %w", lineNo+1, err)
		}
		data[key] = value
	}
	return data, nil
}

func MergeFiles(root string, names ...string) (map[string]string, error) {
	merged := make(map[string]string)
	for _, name := range names {
		values, err := ParseFile(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		maps.Copy(merged, values)
	}
	return merged, nil
}

func AppendMissing(base []string, values map[string]string) []string {
	env := append([]string(nil), base...)
	existing := make(map[string]bool, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			existing[key] = true
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if existing[key] {
			continue
		}
		env = append(env, key+"="+values[key])
	}
	return env
}

func parseValue(value string) (string, error) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return strconv.Unquote(value)
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], nil
	}
	return value, nil
}
