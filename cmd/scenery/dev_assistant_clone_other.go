//go:build !darwin

package main

import "errors"

// cloneAssistantTreeEntry reports that this platform has no directory clone;
// the caller copies file by file.
func cloneAssistantTreeEntry(string, string) error {
	return errors.ErrUnsupported
}
