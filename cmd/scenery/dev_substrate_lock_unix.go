//go:build unix

package main

import (
	"os"
	"strings"
	"syscall"
)

func lockManagedSubstrateRoot(root, kind string) (func(), error) {
	name := "substrate.lock"
	if kind = strings.TrimSpace(kind); kind != "" {
		name = "substrate-" + safeLockName(kind) + ".lock"
	}
	return acquireDevNamedLock(root, name, "shared substrate "+firstNonEmpty(kind, "unknown"), devLockOrderSubstrate)
}

func tryLockDevFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockDevFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func isDevFileLockBusy(err error) bool {
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
}
