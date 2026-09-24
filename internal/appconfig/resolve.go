package appconfig

import (
	"encoding/json"
	"sort"
)

// Entry states.
const (
	StateDefault    = "default"
	StateConfigured = "configured"
	StateAbsent     = "absent"
	StateMissing    = "missing"
	StateInvalid    = "invalid"
)

// Entry sources.
const (
	SourceDefault     = "default"
	SourceEnvironment = "environment"
	SourceNone        = "none"
)

// Entry is the resolution of one catalog input. Value is the effective
// non-secret wire value; Secret references the effective secret version.
type Entry struct {
	Key       string          `json:"key"`
	Type      string          `json:"type"`
	Sensitive bool            `json:"sensitive"`
	Source    string          `json:"source"`
	State     string          `json:"state"`
	Value     json.RawMessage `json:"value,omitempty"`
	Secret    *SecretVersion  `json:"-"`
	Problem   string          `json:"problem,omitempty"`
}

// Resolution is the validated configuration of one environment revision
// against one catalog.
type Resolution struct {
	CatalogRevision string   `json:"catalog_revision"`
	Revision        string   `json:"revision"`
	Entries         []Entry  `json:"entries"`
	Unused          []string `json:"unused"`
}

// Valid reports whether the resolution can start a runtime: no required input
// is missing and no configured value is invalid.
func (r Resolution) Valid() bool {
	for _, entry := range r.Entries {
		if entry.State == StateMissing || entry.State == StateInvalid {
			return false
		}
	}
	return true
}

// Entry returns the resolution of key.
func (r Resolution) Entry(key string) (Entry, bool) {
	index := sort.Search(len(r.Entries), func(index int) bool { return r.Entries[index].Key >= key })
	if index < len(r.Entries) && r.Entries[index].Key == key {
		return r.Entries[index], true
	}
	return Entry{}, false
}

// Resolve applies exactly two layers — declared defaults, then the selected
// environment's configured values — and validates the result. Stored keys the
// catalog does not declare (for example written by a newer branch) are
// reported as unused and never delivered.
func Resolve(catalog Catalog, document Document, deployable bool) Resolution {
	resolution := Resolution{CatalogRevision: catalog.Revision, Revision: document.Revision, Entries: make([]Entry, 0, len(catalog.Inputs)), Unused: []string{}}
	for _, input := range catalog.Inputs {
		entry := Entry{Key: input.Key, Type: input.Type, Sensitive: input.Sensitive, Source: SourceNone}
		value, hasValue := document.Values[input.Key]
		secret, hasSecret := document.Secrets[input.Key]
		switch {
		case input.Sensitive && hasValue, !input.Sensitive && hasSecret:
			entry.State, entry.Source = StateInvalid, SourceEnvironment
			entry.Problem = "stored value kind does not match the declared sensitivity"
		case input.Sensitive && hasSecret:
			version := secret
			entry.State, entry.Source, entry.Secret = StateConfigured, SourceEnvironment, &version
		case hasValue:
			canonical, err := CanonicalValue(input, value)
			if err != nil {
				entry.State, entry.Source, entry.Problem = StateInvalid, SourceEnvironment, err.Error()
				break
			}
			entry.State, entry.Source, entry.Value = StateConfigured, SourceEnvironment, canonical
		case input.Default != nil:
			entry.State, entry.Source, entry.Value = StateDefault, SourceDefault, input.Default
		case input.Required(deployable):
			entry.State, entry.Problem = StateMissing, "required input is not configured"
		default:
			entry.State = StateAbsent
		}
		resolution.Entries = append(resolution.Entries, entry)
	}
	for key := range document.Values {
		if _, ok := catalog.Lookup(key); !ok {
			resolution.Unused = append(resolution.Unused, key)
		}
	}
	for key := range document.Secrets {
		if _, ok := catalog.Lookup(key); !ok {
			resolution.Unused = append(resolution.Unused, key)
		}
	}
	sort.Strings(resolution.Unused)
	return resolution
}

// Missing lists required inputs the environment still has to configure.
func (r Resolution) Missing() []string {
	var keys []string
	for _, entry := range r.Entries {
		if entry.State == StateMissing {
			keys = append(keys, entry.Key)
		}
	}
	return keys
}
