package filemeta

import "syscall"

func nativeIdentity(raw any) (Identity, bool) {
	stat, ok := raw.(*syscall.Stat_t)
	if !ok || stat == nil {
		return Identity{}, false
	}
	return Identity{Device: uint64(stat.Dev), Inode: stat.Ino, ChangeTimeNano: stat.Ctim.Nano()}, true
}
