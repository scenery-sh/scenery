package storage

import "sync"

var storagePutLocks sync.Map

func withStoragePutLock[T any](store, key string, fn func() (T, error)) (T, error) {
	lockKey := store + "\x00" + key
	value, _ := storagePutLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}
