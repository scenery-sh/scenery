package nativebuilddriver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func FileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, err
}

func CopyRegular(src, dst string) (FileCopy, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return FileCopy{}, fmt.Errorf("stat input %s: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return FileCopy{}, fmt.Errorf("input is not a regular file: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return FileCopy{}, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return FileCopy{}, err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		sourceDigest, sourceBytes, sourceErr := FileDigest(src)
		targetDigest, targetBytes, targetErr := FileDigest(dst)
		if sourceErr == nil && targetErr == nil && sourceDigest == targetDigest && sourceBytes == targetBytes {
			return FileCopy{Original: src, Copy: dst, Digest: sourceDigest, Bytes: sourceBytes}, nil
		}
		return FileCopy{}, fmt.Errorf("existing capture differs: %s", dst)
	}
	if err != nil {
		return FileCopy{}, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), in)
	closeErr := out.Close()
	if copyErr != nil {
		return FileCopy{}, copyErr
	}
	if closeErr != nil {
		return FileCopy{}, closeErr
	}
	return FileCopy{Original: src, Copy: dst, Digest: "sha256:" + hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}
