// Package runtimescope retains synchronous native request scope. Child
// goroutines inherit authority only through an explicitly passed Go context.
package runtimescope

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type Scope[T any] struct{ values sync.Map }

func (scope *Scope[T]) Current() (T, bool) {
	value, ok := scope.values.Load(goroutineID())
	if !ok {
		var empty T
		return empty, false
	}
	return value.(T), true
}

// Enter restores the preceding scope on return, including reentrant calls.
// The caller must defer the returned function in the same goroutine.
func (scope *Scope[T]) Enter(value T) func() {
	id := goroutineID()
	previous, existed := scope.values.Load(id)
	scope.values.Store(id, value)
	return func() {
		if existed {
			scope.values.Store(id, previous)
		} else {
			scope.values.Delete(id)
		}
	}
}

func goroutineID() uint64 {
	var buffer [64]byte
	n := runtime.Stack(buffer[:], false)
	line := strings.TrimPrefix(string(buffer[:n]), "goroutine ")
	id, _ := strconv.ParseUint(line[:strings.IndexByte(line, ' ')], 10, 64)
	return id
}
