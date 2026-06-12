//go:build unix

package main

func lockDBBranchRegistry(root string) (func(), error) {
	return acquireDevNamedLock(root, "branches.lock", "database branch registry", devLockOrderRegistry)
}
