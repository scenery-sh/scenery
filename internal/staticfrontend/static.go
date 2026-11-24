// Package staticfrontend serves a built frontend under the application's origin.
package staticfrontend

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type asset struct {
	etag        string
	modified    time.Time
	contentType string
	immutable   bool
}

// Handler owns the contained filesystem for one immutable dist tree.
type Handler struct {
	root    *os.Root
	assets  map[string]asset
	blocked []string
}

var hashedAsset = regexp.MustCompile(`-[a-zA-Z0-9_-]{8,}\.[a-zA-Z0-9]+$`)

func Open(directory string, reservedPrefixes []string) (*Handler, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	handler := &Handler{root: root, assets: make(map[string]asset), blocked: slices.Clone(reservedPrefixes)}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		if !validPath(name) || entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("invalid frontend output %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("frontend output %s is not a regular file", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		handler.assets[name] = asset{
			etag: fmt.Sprintf("\"%x\"", hash.Sum(nil)), modified: info.ModTime(),
			contentType: mime.TypeByExtension(path.Ext(name)),
			immutable:   strings.HasPrefix(name, "assets/") && hashedAsset.MatchString(path.Base(name)),
		}
		return nil
	})
	if err == nil {
		if _, ok := handler.assets["index.html"]; !ok {
			err = fmt.Errorf("frontend dist has no index.html")
		}
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return handler, nil
}

func (handler *Handler) Close() error { return handler.root.Close() }

func Reserved(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimRight(prefix, "/")
		if prefix != "" && (value == prefix || strings.HasPrefix(value, prefix+"/")) {
			return true
		}
	}
	return false
}

func NotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"code":"not_found","message":"endpoint not found"}`)
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, "/")
	if Reserved(request.URL.Path, handler.blocked) || name != "" && !validPath(name) {
		NotFound(w)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Sidecars are transport variants of the source file.
	if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".br") {
		NotFound(w)
		return
	}
	info, exists := handler.assets[name]
	accept := request.Header.Get("Accept")
	if !exists && (name == "" || path.Ext(name) == "" && (accept == "" || strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*"))) {
		name = "index.html"
		info, exists = handler.assets[name]
	}
	if !exists {
		NotFound(w)
		return
	}
	encodedName, encoding := handler.selectEncoding(name, request.Header.Get("Accept-Encoding"))
	if _, brotli := handler.assets[name+".br"]; brotli {
		w.Header().Add("Vary", "Accept-Encoding")
	} else if _, gzip := handler.assets[name+".gz"]; gzip {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	if encoding != "" {
		w.Header().Set("Content-Encoding", encoding)
	}
	if info.contentType != "" {
		w.Header().Set("Content-Type", info.contentType)
	}
	cache := "no-cache"
	if info.immutable {
		cache = "public, max-age=31536000, immutable"
	}
	file, err := handler.root.Open(encodedName)
	if err != nil {
		NotFound(w)
		return
	}
	defer func() { _ = file.Close() }()
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("ETag", handler.assets[encodedName].etag)
	http.ServeContent(w, request, name, handler.assets[encodedName].modified, file)
}

func validPath(name string) bool {
	if !fs.ValidPath(name) || strings.Contains(name, "\\") || strings.Contains(name, "\x00") {
		return false
	}
	for index, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") && (index != 0 || component != ".well-known") {
			return false
		}
	}
	return true
}

func (handler *Handler) selectEncoding(name, accept string) (string, string) {
	quality := make(map[string]float64)
	for value := range strings.SplitSeq(accept, ",") {
		parts := strings.Split(value, ";")
		encoding := strings.ToLower(strings.TrimSpace(parts[0]))
		q := 1.0
		for _, parameter := range parts[1:] {
			if number, found := strings.CutPrefix(strings.TrimSpace(parameter), "q="); found {
				q, _ = strconv.ParseFloat(number, 64)
			}
		}
		quality[encoding] = q
	}
	best, selected, available := 0.0, name, ""
	for _, variant := range []struct{ suffix, encoding string }{{".br", "br"}, {".gz", "gzip"}} {
		if _, ok := handler.assets[name+variant.suffix]; !ok {
			continue
		}
		q, explicit := quality[variant.encoding]
		if !explicit {
			q = quality["*"]
		}
		if q > best {
			best, selected, available = q, name+variant.suffix, variant.encoding
		}
	}
	return selected, available
}
