//go:build windows

package main

func lockManagedSubstrateRoot(string, string) (func(), error) {
	return func() {}, nil
}
