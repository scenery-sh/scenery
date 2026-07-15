package main

import (
	"os"

	"scenery.sh/internal/atomicfile"
)

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	return atomicfile.Write(path, data, mode, atomicfile.Options{SyncFile: true, SyncDir: true})
}
