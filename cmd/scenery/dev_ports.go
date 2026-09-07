package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

const (
	defaultDevPortStart = 4001
	defaultDevPortEnd   = 4999
)

func preferredDevPort(appRoot string, start, end int) (int, error) {
	start, end, err := normalizeDevPortRange(start, end)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(appRoot)))
	span := uint32(end - start + 1)
	offset := binary.BigEndian.Uint32(sum[:4]) % span
	return start + int(offset), nil
}

func normalizeDevPortRange(start, end int) (int, int, error) {
	if start == 0 {
		start = defaultDevPortStart
	}
	if end == 0 {
		end = defaultDevPortEnd
	}
	if start < 1024 || end < start || end > 65535 {
		return 0, 0, fmt.Errorf("invalid dev port range %d-%d", start, end)
	}
	return start, end, nil
}
