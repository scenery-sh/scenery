package runtimeassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildArchive creates deterministic gzip-compressed tar bytes from root.
// Tar entries are lexical, ownership and timestamps are zeroed, and only the
// safe file modes accepted by DescribeTree are emitted.
func BuildArchive(root string) (Archive, error) {
	descriptor, err := DescribeTree(root)
	if err != nil {
		return Archive{}, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Archive{}, fmt.Errorf("resolve archive root: %w", err)
	}
	var data bytes.Buffer
	gzipWriter := gzip.NewWriter(&data)
	// gzip embeds a timestamp by default.  Keep every header field explicit so
	// repeated builds produce byte-for-byte identical output.
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.Name = ""
	gzipWriter.Header.Comment = ""
	gzipWriter.Header.Extra = nil
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range descriptor.Entries {
		header := &tar.Header{
			Name:       entry.Path,
			Mode:       int64(entry.Mode),
			Uid:        0,
			Gid:        0,
			Uname:      "",
			Gname:      "",
			ModTime:    time.Unix(0, 0),
			AccessTime: time.Unix(0, 0),
			ChangeTime: time.Unix(0, 0),
		}
		switch entry.Kind {
		case EntryDirectory:
			header.Typeflag = tar.TypeDir
		case EntryFile:
			header.Typeflag = tar.TypeReg
			header.Size = entry.Size
		case EntrySymlink:
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.Target
		default:
			return Archive{}, fmt.Errorf("unsupported descriptor entry kind %q", entry.Kind)
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return Archive{}, fmt.Errorf("write archive header %q: %w", entry.Path, err)
		}
		if entry.Kind != EntryFile {
			continue
		}
		f, err := os.Open(filepath.Join(absolute, filepath.FromSlash(entry.Path)))
		if err != nil {
			return Archive{}, fmt.Errorf("open archive file %q: %w", entry.Path, err)
		}
		written, copyErr := io.CopyN(tarWriter, f, entry.Size)
		closeErr := f.Close()
		if copyErr != nil {
			return Archive{}, fmt.Errorf("copy archive file %q: %w", entry.Path, copyErr)
		}
		if closeErr != nil {
			return Archive{}, fmt.Errorf("close archive file %q: %w", entry.Path, closeErr)
		}
		if written != entry.Size {
			return Archive{}, fmt.Errorf("archive file %q size changed: wrote %d want %d", entry.Path, written, entry.Size)
		}
	}
	if err := tarWriter.Close(); err != nil {
		return Archive{}, fmt.Errorf("close tar archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return Archive{}, fmt.Errorf("close gzip archive: %w", err)
	}
	return NewArchive(data.Bytes(), descriptor)
}

// ExtractArchive extracts one archive into destination.  Destination may be
// a newly created directory or an existing empty directory.  The function
// rejects traversal, duplicates, special files, hardlinks, and unsafe
// symlinks before those entries can become executable content.  On any
// failure, content written by this call is removed and an existing destination
// is left in place.
func ExtractArchive(data []byte, destination string) (descriptor Descriptor, err error) {
	if len(data) == 0 {
		return Descriptor{}, fmt.Errorf("archive is empty")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return Descriptor{}, fmt.Errorf("resolve extraction destination: %w", err)
	}
	createdDestination := false
	createdPaths := make([]string, 0)
	if info, statErr := os.Lstat(absolute); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return Descriptor{}, fmt.Errorf("extraction destination is not a directory")
		}
		entries, readErr := os.ReadDir(absolute)
		if readErr != nil {
			return Descriptor{}, readErr
		}
		if len(entries) != 0 {
			return Descriptor{}, fmt.Errorf("extraction destination is not empty")
		}
	} else if !os.IsNotExist(statErr) {
		return Descriptor{}, statErr
	} else if err := os.MkdirAll(absolute, 0o755); err != nil {
		return Descriptor{}, err
	} else if err := os.Chmod(absolute, 0o755); err != nil {
		return Descriptor{}, err
	} else {
		createdDestination = true
	}
	defer func() {
		if err != nil {
			if createdDestination {
				_ = os.RemoveAll(absolute)
				return
			}
			for i := len(createdPaths) - 1; i >= 0; i-- {
				// Remove only paths created by this extraction.  Lstat avoids
				// following a symlink if a caller changed a path concurrently.
				if info, statErr := os.Lstat(createdPaths[i]); statErr == nil {
					if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
						_ = os.Remove(createdPaths[i])
					} else {
						_ = os.Remove(createdPaths[i])
					}
				}
			}
		}
	}()

	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return Descriptor{}, fmt.Errorf("open gzip archive: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	entries := make([]Entry, 0)
	seen := make(map[string]EntryKind)
	implicitDirs := make(map[string]struct{})
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return Descriptor{}, fmt.Errorf("read tar archive: %w", nextErr)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name != header.Name {
			return Descriptor{}, fmt.Errorf("archive path %q has a trailing separator", header.Name)
		}
		if err := validateArchivePath(name); err != nil {
			return Descriptor{}, fmt.Errorf("archive path %q: %w", name, err)
		}
		if name == DescriptorFilename {
			return Descriptor{}, fmt.Errorf("archive path %q is reserved", name)
		}
		if _, ok := seen[name]; ok {
			return Descriptor{}, fmt.Errorf("duplicate archive path %q", name)
		}
		kind, mode, size, target, err := validateHeader(name, header)
		if err != nil {
			return Descriptor{}, err
		}
		parents, parentErr := ensureArchiveParents(name, absolute, seen, implicitDirs)
		createdPaths = append(createdPaths, parents...)
		if parentErr != nil {
			return Descriptor{}, parentErr
		}
		destinationPath := filepath.Join(absolute, filepath.FromSlash(name))
		if _, statErr := os.Lstat(destinationPath); statErr == nil {
			return Descriptor{}, fmt.Errorf("archive path collides with existing parent %q", name)
		} else if !os.IsNotExist(statErr) {
			return Descriptor{}, statErr
		}
		switch kind {
		case EntryDirectory:
			if err := os.Mkdir(destinationPath, os.FileMode(mode)); err != nil {
				return Descriptor{}, fmt.Errorf("create directory %q: %w", name, err)
			}
			createdPaths = append(createdPaths, destinationPath)
			if err := os.Chmod(destinationPath, os.FileMode(mode)); err != nil {
				return Descriptor{}, fmt.Errorf("chmod directory %q: %w", name, err)
			}
		case EntryFile:
			created, extractErr := extractRegularFile(destinationPath, tarReader, size, os.FileMode(mode))
			if created {
				createdPaths = append(createdPaths, destinationPath)
			}
			if extractErr != nil {
				return Descriptor{}, fmt.Errorf("extract file %q: %w", name, extractErr)
			}
		case EntrySymlink:
			if err := os.Symlink(target, destinationPath); err != nil {
				return Descriptor{}, fmt.Errorf("extract symlink %q: %w", name, err)
			}
			createdPaths = append(createdPaths, destinationPath)
		}
		seen[name] = kind
		entry := Entry{Path: name, Kind: kind, Mode: mode, Size: size, Target: target}
		if kind == EntryFile {
			entry.Digest, _, err = digestFile(destinationPath)
			if err != nil {
				return Descriptor{}, fmt.Errorf("digest extracted file %q: %w", name, err)
			}
		}
		entries = append(entries, entry)
	}
	if err := reader.Close(); err != nil {
		return Descriptor{}, fmt.Errorf("close gzip archive: %w", err)
	}
	// The writer emits lexical entries.  Re-sort here so hand-authored but
	// otherwise safe archives still produce one canonical tree identity.
	// Parent directory entries created implicitly are added to the descriptor;
	// this makes an archive without explicit directory headers equivalent to
	// the resulting filesystem tree.
	descriptor, err = describeTree(absolute, false)
	if err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

func validateHeader(name string, header *tar.Header) (EntryKind, uint32, int64, string, error) {
	mode := uint32(header.Mode)
	switch header.Typeflag {
	case tar.TypeDir:
		if mode != 0o755 || header.Size != 0 {
			return "", 0, 0, "", fmt.Errorf("directory %q has unsafe mode or size", name)
		}
		return EntryDirectory, mode, 0, "", nil
	case tar.TypeReg, tar.TypeRegA:
		if mode != 0o644 && mode != 0o755 {
			return "", 0, 0, "", fmt.Errorf("file %q has unsafe mode %04o", name, mode)
		}
		if header.Size < 0 {
			return "", 0, 0, "", fmt.Errorf("file %q has negative size", name)
		}
		return EntryFile, mode, header.Size, "", nil
	case tar.TypeSymlink:
		if header.Size != 0 {
			return "", 0, 0, "", fmt.Errorf("symlink %q has nonzero size", name)
		}
		if err := validateSymlinkTarget(name, header.Linkname); err != nil {
			return "", 0, 0, "", err
		}
		return EntrySymlink, 0, 0, header.Linkname, nil
	case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
		return "", 0, 0, "", fmt.Errorf("archive metadata entry %q is not allowed", name)
	default:
		return "", 0, 0, "", fmt.Errorf("archive path %q has unsupported type %d", name, header.Typeflag)
	}
}

func ensureArchiveParents(name, root string, seen map[string]EntryKind, implicitDirs map[string]struct{}) ([]string, error) {
	created := make([]string, 0)
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], "/")
		if kind, ok := seen[parent]; ok {
			if kind != EntryDirectory {
				return created, fmt.Errorf("archive path %q has non-directory parent %q", name, parent)
			}
			continue
		}
		if _, ok := implicitDirs[parent]; ok {
			continue
		}
		pathName := filepath.Join(root, filepath.FromSlash(parent))
		if info, err := os.Lstat(pathName); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return created, fmt.Errorf("archive path %q has unsafe parent %q", name, parent)
			}
		} else if os.IsNotExist(err) {
			if err := os.Mkdir(pathName, 0o755); err != nil {
				return created, fmt.Errorf("create archive parent %q: %w", parent, err)
			}
			created = append(created, pathName)
			if err := os.Chmod(pathName, 0o755); err != nil {
				return created, fmt.Errorf("chmod archive parent %q: %w", parent, err)
			}
		} else {
			return created, err
		}
		implicitDirs[parent] = struct{}{}
	}
	return created, nil
}

func extractRegularFile(destination string, source io.Reader, size int64, mode os.FileMode) (bool, error) {
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return false, err
	}
	close := func() error { return f.Close() }
	defer close()
	if _, err := io.CopyN(f, source, size); err != nil {
		return true, err
	}
	if err := f.Chmod(mode); err != nil {
		return true, err
	}
	if err := f.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
