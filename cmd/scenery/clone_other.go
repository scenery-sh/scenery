//go:build !darwin

package main

import "errors"

// cloneFileOrTree reports that this platform has no directory clone;
// the caller copies file by file.
func cloneFileOrTree(string, string) error {
	return errors.ErrUnsupported
}
