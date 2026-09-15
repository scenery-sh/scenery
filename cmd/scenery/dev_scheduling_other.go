//go:build !darwin

package main

// Other platforms expose no inheritable process background policy that the
// supervisor can observe and remove without additional privileges.
func clearInheritedBackgroundPolicy() (string, error) { return "", nil }
