//go:build !darwin

package filemeta

import "errors"

// Clone reports that this platform has no clone of a file or directory tree;
// the caller copies instead.
func Clone(string, string) error {
	return errors.ErrUnsupported
}
