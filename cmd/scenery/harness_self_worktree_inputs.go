package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Record authored working-tree inputs, including new files but not ignored
// build caches. Performance evidence must not span changing source inputs.
func (p *worktreeRuntimeProbe) sourceDigest() (string, error) {
	out, err := p.run(p.repo, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	paths := bytes.Split(out, []byte{0})
	sort.Slice(paths, func(i, j int) bool { return bytes.Compare(paths[i], paths[j]) < 0 })
	hash := sha256.New()
	for _, raw := range paths {
		if len(raw) == 0 {
			continue
		}
		_, _ = hash.Write(raw)
		_, _ = hash.Write([]byte{0})
		path := filepath.Join(p.repo, string(raw))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			_, _ = io.WriteString(hash, "missing\x00")
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00", info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(hash, target+"\x00")
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
