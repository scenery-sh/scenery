package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"scenery.sh/internal/watchignore"
)

// walkWatchTree walks root in the order and with the callback semantics of
// filepath.WalkDir. Before visiting a directory's entries it loads that
// directory's .gitignore from the listing it has just read, so a rescan does
// not look up .gitignore separately in every directory.
func walkWatchTree(root string, ignore *watchignore.Matcher, fn fs.WalkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkWatchDir(root, "", fs.FileInfoToDirEntry(info), ignore, fn)
	}
	if errors.Is(err, filepath.SkipDir) || errors.Is(err, filepath.SkipAll) {
		return nil
	}
	return err
}

func walkWatchDir(path, rel string, entry fs.DirEntry, ignore *watchignore.Matcher, fn fs.WalkDirFunc) error {
	if err := fn(path, entry, nil); err != nil || !entry.IsDir() {
		if errors.Is(err, filepath.SkipDir) && entry.IsDir() {
			err = nil
		}
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if err = fn(path, entry, err); err != nil {
			if errors.Is(err, filepath.SkipDir) {
				err = nil
			}
			return err
		}
	}
	ignore.LoadDirEntries(rel, entries)
	for _, child := range entries {
		childRel := child.Name()
		if rel != "" {
			childRel = rel + "/" + child.Name()
		}
		if err := walkWatchDir(filepath.Join(path, child.Name()), childRel, child, ignore, fn); err != nil {
			if errors.Is(err, filepath.SkipDir) {
				break
			}
			return err
		}
	}
	return nil
}
