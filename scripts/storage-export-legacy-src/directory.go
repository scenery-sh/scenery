package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
	"scenery.sh/internal/snapshotarchive"
)

type directorySource struct {
	root   *os.Root
	stores []string
}

func openDirectory(name string) (*directorySource, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	source := &directorySource{root: root}
	fail := func(err error) (*directorySource, error) { _ = root.Close(); return nil, err }
	if _, err := root.Lstat("owner.json"); !errors.Is(err, os.ErrNotExist) {
		return fail(fmt.Errorf("source is not a legacy cell: owner.json is present or unreadable"))
	}
	dir, err := source.open("objects", true)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, err := dir.Readdir(64)
		for _, entry := range entries {
			if !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
				return fail(fmt.Errorf("legacy objects root contains a non-store entry"))
			}
			if err := snapshotarchive.ValidatePath(entry.Name()); err != nil {
				return fail(err)
			}
			source.stores = append(source.stores, entry.Name())
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
	}
	sort.Strings(source.stores)
	return source, nil
}
func (s *directorySource) Manifest() snapshotarchive.Manifest {
	return snapshotarchive.Manifest{Storage: &snapshotarchive.Storage{}}
}
func (s *directorySource) Stores() []string { return s.stores }
func (s *directorySource) Close() error     { return s.root.Close() }
func (s *directorySource) CopyDatabase(context.Context, io.Writer) error {
	return fmt.Errorf("directory export does not infer database state")
}

func (s *directorySource) open(name string, directory bool) (*os.File, error) {
	if err := snapshotarchive.ValidatePath(name); err != nil {
		return nil, err
	}
	parent := ""
	for _, part := range strings.Split(path.Dir(name), "/") {
		parent = path.Join(parent, part)
		info, err := s.root.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe legacy parent %q", parent)
		}
	}
	f, err := s.root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && (info.IsDir() != directory || (!directory && !info.Mode().IsRegular())) {
		err = fmt.Errorf("non-regular legacy source %q", name)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (s *directorySource) Open(store, key string) (*legacyFile, error) {
	f, err := s.open("objects/"+store+"/"+key, false)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &legacyFile{ReadCloser: f, Size: info.Size(), Modified: info.ModTime()}, nil
}

func (s *directorySource) Walk(ctx context.Context, store string, visit func(string) error) error {
	var walk func(string) error
	walk = func(prefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(prefix) > snapshotarchive.MaxObjectRecordBytes {
			return fmt.Errorf("legacy path exceeds record bound")
		}
		name := path.Join("objects", store, prefix)
		dir, err := s.open(name, true)
		if err != nil {
			return err
		}
		defer func() { _ = dir.Close() }()
		for {
			entries, err := dir.Readdir(64)
			for _, entry := range entries {
				key := path.Join(prefix, entry.Name())
				if entry.IsDir() {
					if err := walk(key); err != nil {
						return err
					}
					continue
				}
				if !entry.Mode().IsRegular() {
					return fmt.Errorf("legacy source contains symlink or non-regular file %q", key)
				}
				if err := visit(key); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	return walk("")
}
