//go:build !unix

package jsbuild

import "sync"

var cacheMu sync.Mutex

func withCacheLock(_ string, fn func() error) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return fn()
}
