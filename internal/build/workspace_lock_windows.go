//go:build windows

package build

func lockWorkspace(string) (func(), error) {
	return func() {}, nil
}

// tryLockWorkspace cannot observe other holders where lockWorkspace is a
// no-op; it reports the workspace as free.
func tryLockWorkspace(string) (unlock func(), held bool, err error) {
	return func() {}, false, nil
}
