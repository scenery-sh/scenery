package storage

import "scenery.sh/internal/storagefs"

func ValidateKey(key string) error {
	return adaptError(storagefs.ValidateKey(key), "", key, false)
}

func ValidatePrefix(prefix string) error {
	return adaptError(storagefs.ValidatePrefix(prefix), "", prefix, false)
}

func NormalizeListOptions(opts ListOptions) (ListOptions, error) {
	normalized, err := storagefs.NormalizeListOptions(opts)
	return normalized, adaptError(err, "", opts.Prefix, false)
}
