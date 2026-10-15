package connectionvault

import "scenery.sh/internal/connectionapi"

func NormalizeScopes(scopes []string) ([]string, error) { return connectionapi.NormalizeScopes(scopes) }
func ParseScopes(value string) ([]string, error)        { return connectionapi.ParseScopes(value) }
func ContainsScopes(have, required []string) bool {
	return connectionapi.ContainsScopes(have, required)
}
