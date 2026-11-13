package main

import (
	"fmt"
	"os"
	"syscall"
)

func validateEdgeTargetOwner(path string, info os.FileInfo, ownerUID, ownerGID int) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("edge target metadata %s has unsupported stat type", path)
	}
	if int(stat.Uid) != ownerUID || int(stat.Gid) != ownerGID {
		return fmt.Errorf("edge target metadata owner %d:%d does not match expected %d:%d", stat.Uid, stat.Gid, ownerUID, ownerGID)
	}
	return nil
}
