//go:build !unix

package stateupgrade

import "os"

func owned(os.FileInfo) bool { return false }
