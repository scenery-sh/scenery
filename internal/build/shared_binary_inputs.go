package build

import (
	"fmt"
	"os"
	"path/filepath"
)

func observeBuildInputPath(observed map[string]buildInputFileStamp, path string) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("go build input is not a regular file or directory: %s", path)
	}
	stamp := buildInputStamp(info)
	path = filepath.Clean(path)
	if before, ok := observed[path]; ok && before != stamp {
		return fmt.Errorf("go build input changed during discovery: %s", path)
	}
	observed[path] = stamp
	return nil
}

// observeBuildInputDirectories records the stamps of directory and its parents
// up to packageRoot. A directory this discovery already observed was observed
// with its parents, so the walk stops there.
func observeBuildInputDirectories(observed map[string]buildInputFileStamp, directory, packageRoot string) error {
	for {
		if _, seen := observed[filepath.Clean(directory)]; seen {
			return nil
		}
		if err := observeBuildInputPath(observed, directory); err != nil {
			return err
		}
		if directory == packageRoot || filepath.Dir(directory) == directory {
			return nil
		}
		directory = filepath.Dir(directory)
	}
}
