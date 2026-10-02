// Package filemeta reads filesystem identity and status-change timestamps from
// FileInfo. It owns no cache or freshness policy; callers decide what to reuse.
package filemeta

import (
	"io/fs"
	"reflect"
)

// Identity is the physical file identity plus its status-change timestamp.
type Identity struct {
	Device, Inode  uint64
	ChangeTimeNano int64
}

// Read reports whether all identity fields are available. Partial metadata is
// returned too, so callers can retain an identity without granting cache reuse.
func Read(info fs.FileInfo) (Identity, bool) {
	if info == nil {
		return Identity{}, false
	}
	raw := info.Sys()
	if identity, ok := nativeIdentity(raw); ok {
		return identity, true
	}
	value := reflect.ValueOf(raw)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return Identity{}, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return Identity{}, false
	}
	readNumber := func(name string) (uint64, bool) {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanUint() {
			return field.Uint(), true
		}
		if field.IsValid() && field.CanInt() {
			return uint64(field.Int()), true
		}
		return 0, false
	}
	device, deviceKnown := readNumber("Dev")
	inode, inodeKnown := readNumber("Ino")
	identity := Identity{Device: device, Inode: inode}
	for _, name := range [...]string{"Ctimespec", "Ctim", "Ctimen"} {
		stamp := value.FieldByName(name)
		if !stamp.IsValid() || stamp.Kind() != reflect.Struct {
			continue
		}
		seconds, nanos := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
		if seconds.IsValid() && nanos.IsValid() && seconds.CanInt() && nanos.CanInt() {
			identity.ChangeTimeNano = seconds.Int()*1_000_000_000 + nanos.Int()
			return identity, deviceKnown && inodeKnown
		}
	}
	return identity, false
}
