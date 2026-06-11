package runtime

import (
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"

	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
)

var (
	secretsEnvOnce      sync.Once
	secretsEnvData      map[string]string
	secretsEnvErr       error
	secretsWarnedMu     sync.Mutex
	secretsWarnedFields map[string]bool
	secretsPendingKeys  map[string][]string
	secretsFlushed      bool
)

func MustPopulateSecrets(target any) {
	if err := PopulateSecrets(target); err != nil {
		panic(err)
	}
}

func MustLoadDotEnvIntoEnv() bool {
	if err := LoadDotEnvIntoEnv(); err != nil {
		panic(err)
	}
	return true
}

func LoadDotEnvIntoEnv() error {
	env, err := loadSecretsEnv()
	if err != nil {
		return err
	}
	for key, value := range env {
		if _, exists := envpolicy.Lookup(key); exists {
			continue
		}
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

func PopulateSecrets(target any) error {
	value := reflect.ValueOf(target)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("runtime: secrets target must be a non-nil pointer to struct")
	}
	elem := value.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("runtime: secrets target must point to struct, got %s", elem.Kind())
	}

	env, err := loadSecretsEnv()
	if err != nil {
		return err
	}

	typ := elem.Type()
	var missing []missingSecret
	for i := 0; i < elem.NumField(); i++ {
		field := elem.Field(i)
		structField := typ.Field(i)
		if !structField.IsExported() {
			continue
		}
		if field.Kind() != reflect.String {
			return fmt.Errorf("runtime: secret field %s must be string, got %s", structField.Name, field.Type())
		}
		keys := secretEnvKeys(structField.Name)
		value, ok := lookupSecretValue(env, keys)
		if !ok {
			missing = append(missing, missingSecret{Field: structField.Name, Keys: keys})
			continue
		}
		field.SetString(value)
	}
	if len(missing) > 0 && strictSecretsRequired() {
		return fmt.Errorf("runtime: missing required secrets for production: %s", formatMissingSecrets(missing))
	}
	logMissingSecrets(missing)
	return nil
}

type missingSecret struct {
	Field string
	Keys  []string
}

func logMissingSecrets(missing []missingSecret) {
	if len(missing) == 0 {
		return
	}
	fields, keys, emitNow := rememberMissingSecrets(missing)
	if len(fields) == 0 || !emitNow {
		return
	}
	slog.Warn("scenery secrets missing", "fields", fields, "env_keys", keys, "source", ".env")
}

func strictSecretsRequired() bool {
	for _, key := range []string{"SCENERY_RUNTIME_ENV", "SCENERY_ENV"} {
		if strings.EqualFold(strings.TrimSpace(envpolicy.Get(key)), "production") {
			return true
		}
	}
	return false
}

func formatMissingSecrets(missing []missingSecret) string {
	items := make([]string, 0, len(missing))
	for _, secret := range missing {
		items = append(items, fmt.Sprintf("%s (%s)", secret.Field, strings.Join(secret.Keys, ", ")))
	}
	slices.Sort(items)
	return strings.Join(items, "; ")
}

func rememberMissingSecrets(missing []missingSecret) (fields []string, keys []string, emitNow bool) {
	secretsWarnedMu.Lock()
	defer secretsWarnedMu.Unlock()
	if secretsPendingKeys == nil {
		secretsPendingKeys = make(map[string][]string, len(missing))
	}
	if secretsWarnedFields == nil {
		secretsWarnedFields = make(map[string]bool, len(missing))
	}
	for _, secret := range missing {
		if secretsWarnedFields[secret.Field] {
			continue
		}
		if _, ok := secretsPendingKeys[secret.Field]; ok {
			continue
		}
		secretsPendingKeys[secret.Field] = append([]string(nil), secret.Keys...)
	}
	if !secretsFlushed {
		return nil, nil, false
	}
	return collectMissingSecretsLocked()
}

func FlushMissingSecretsWarnings() {
	secretsWarnedMu.Lock()
	if !secretsFlushed {
		secretsFlushed = true
	}
	fields, keys, ok := collectMissingSecretsLocked()
	secretsWarnedMu.Unlock()
	if !ok {
		return
	}
	slog.Warn("scenery secrets missing", "fields", fields, "env_keys", keys, "source", ".env")
}

func collectMissingSecretsLocked() (fields []string, keys []string, ok bool) {
	if len(secretsPendingKeys) == 0 {
		return nil, nil, false
	}
	fields = make([]string, 0, len(secretsPendingKeys))
	seenKeys := make(map[string]bool, len(secretsPendingKeys)*2)
	for field, fieldKeys := range secretsPendingKeys {
		if secretsWarnedFields[field] {
			continue
		}
		secretsWarnedFields[field] = true
		fields = append(fields, field)
		for _, key := range fieldKeys {
			if seenKeys[key] {
				continue
			}
			seenKeys[key] = true
			keys = append(keys, key)
		}
		delete(secretsPendingKeys, field)
	}
	if len(fields) == 0 {
		return nil, nil, false
	}
	slices.Sort(fields)
	slices.Sort(keys)
	return fields, keys, true
}

func loadSecretsEnv() (map[string]string, error) {
	secretsEnvOnce.Do(func() {
		secretsEnvData, secretsEnvErr = envfile.ParseFile(".env")
	})
	return secretsEnvData, secretsEnvErr
}

func lookupSecretValue(fileEnv map[string]string, keys []string) (string, bool) {
	for _, key := range keys {
		if value, ok := envpolicy.Lookup(key); ok {
			return value, true
		}
	}
	for _, key := range keys {
		if value, ok := fileEnv[key]; ok {
			return value, true
		}
	}
	return "", false
}

func secretEnvKeys(fieldName string) []string {
	keys := []string{fieldName}
	alt := toEnvKey(fieldName)
	if alt != "" && alt != fieldName {
		keys = append(keys, alt)
	}
	return keys
}

func toEnvKey(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && shouldInsertUnderscore(runes[i-1], r, nextRune(runes, i)) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

func nextRune(runes []rune, index int) rune {
	if index+1 >= len(runes) {
		return 0
	}
	return runes[index+1]
}

func shouldInsertUnderscore(prev, current, next rune) bool {
	if !unicode.IsUpper(current) {
		return false
	}
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)
}
