package main

import "strings"

// appProcessInheritedKeys are the operating-system and toolchain protocols an
// application process inherits from the invoking environment. Everything an
// application reads as configuration arrives from its configuration snapshot
// or from variables Scenery sets itself; SDK credential chains, dotenv-era
// settings and runtime loaders such as NODE_OPTIONS are not inherited.
var appProcessInheritedKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "TZ": true, "LANG": true, "LANGUAGE": true,
	"TERM": true, "COLORTERM": true, "NO_COLOR": true, "CLICOLOR": true, "CLICOLOR_FORCE": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	"XDG_RUNTIME_DIR": true, "XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	"LD_LIBRARY_PATH": true, "DYLD_LIBRARY_PATH": true, "DYLD_FALLBACK_LIBRARY_PATH": true,
	"GODEBUG": true, "GOMAXPROCS": true, "GOMEMLIMIT": true, "GOGC": true, "GOTRACEBACK": true, "GOCOVERDIR": true,
	// The CLI never inherits DATABASE_URL (see applyConfiguredSQLSupply), so a
	// value present here is the environment's configured external database.
	appDatabaseURLEnv: true,
}

// appProcessInheritedPrefixes cover locale categories and Scenery's own
// declared wiring.
var appProcessInheritedPrefixes = []string{"LC_", "SCENERY_"}

// minimalAppProcessEnv keeps only the inherited entries an application process
// may see.
func minimalAppProcessEnv(inherited []string) []string {
	env := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if appProcessInheritedKeys[key] || hasAnyPrefix(key, appProcessInheritedPrefixes) {
			env = append(env, entry)
		}
	}
	return env
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
