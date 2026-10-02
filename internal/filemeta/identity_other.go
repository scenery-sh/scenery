//go:build !darwin && !linux

package filemeta

// Other FileInfo representations use the existing field-based observation.
func nativeIdentity(any) (Identity, bool) { return Identity{}, false }
