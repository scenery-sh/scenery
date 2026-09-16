package build

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// fileDigest returns the SHA-256 content digest and size of a regular file.
func fileDigest(path string) (string, int64, error) {
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

// sourceSelectionIdentity identifies the package, import and build-directive
// part of one Go source file while deliberately ignoring function-body changes.
func sourceSelectionIdentity(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly)
	if err != nil {
		return "", err
	}
	values := []string{"package=" + file.Name.Name}
	for _, imp := range file.Imports {
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return "", err
		}
		values = append(values, "import="+name+":"+value)
	}
	fileSet := token.NewFileSet()
	position := fileSet.AddFile(path, -1, len(data))
	var sourceScanner scanner.Scanner
	sourceScanner.Init(position, data, nil, scanner.ScanComments)
	for {
		_, tok, literal := sourceScanner.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		for line := range strings.SplitSeq(literal, "\n") {
			text := strings.TrimSpace(line)
			if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "// +build") {
				values = append(values, "directive="+text)
			}
		}
	}
	slices.Sort(values[1:])
	h := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
