package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/dbstudio"
)

const dbStudioUIAddr = "127.0.0.1:4003"

var dbStudioUISourcePaths = []string{
	"package.json",
	"bun.lock",
	"bunfig.toml",
	"tsconfig.json",
	"vite.config.ts",
	"vite.config.js",
	"vite.config.mts",
	"vite.config.mjs",
	"index.html",
	"src",
	"public",
}

type dbStudioUIServer struct {
	http   *http.Server
	assets fs.FS
}

func prepareDBStudioUIDir(ctx context.Context, console *runConsole) (string, error) {
	if dir := strings.TrimSpace(os.Getenv("ONLAVA_DEV_DBSTUDIO_UI_DIR")); dir != "" {
		return dir, nil
	}

	referenceRoot := filepath.Join(app.RepoRoot(), "dbstudio", "reference", "original")
	if _, err := os.Stat(filepath.Join(referenceRoot, "index.html")); err == nil {
		return referenceRoot, nil
	}

	return prepareUIDir(ctx, console, uiBuildSpec{
		envVar:       "ONLAVA_DEV_DBSTUDIO_UI_DIR",
		root:         filepath.Join(app.RepoRoot(), "dbstudio"),
		installTitle: "Installing onlava DB Studio UI packages",
		buildTitle:   "Building onlava DB Studio UI",
		sourcePaths:  dbStudioUISourcePaths,
	})
}

func newDBStudioUIServer(assetsDir string) *dbStudioUIServer {
	s := &dbStudioUIServer{}
	if dir := strings.TrimSpace(assetsDir); dir != "" {
		s.assets = os.DirFS(dir)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.http = &http.Server{
		Addr:    dbStudioUIAddr,
		Handler: mux,
	}
	return s
}

func (s *dbStudioUIServer) Start(ctx context.Context) error {
	if s == nil || s.http == nil || s.assets == nil {
		return nil
	}
	ln, err := net.Listen("tcp", dbStudioUIAddr)
	if err != nil {
		return fmt.Errorf("onlava db studio UI failed to listen on %s: %w", dbStudioUIAddr, err)
	}
	go func() {
		<-ctx.Done()
		_ = s.http.Shutdown(context.Background())
	}()
	go func() {
		_ = s.http.Serve(ln)
	}()
	return nil
}

func (s *dbStudioUIServer) Close() error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.Close()
}

func (s *dbStudioUIServer) handle(w http.ResponseWriter, req *http.Request) {
	if s == nil || s.assets == nil {
		http.NotFound(w, req)
		return
	}
	name := strings.TrimPrefix(pathClean(req.URL.Path), "/")
	if name != "" && name != "." {
		if data, err := fs.ReadFile(s.assets, name); err == nil {
			data = patchDBStudioAsset(name, data)
			if contentType := detectAssetContentType(name); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, req, filepath.Base(name), time.Time{}, bytes.NewReader(data))
			return
		}
	}

	index, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<html><body><p>onlava DB Studio UI build is not available. Run <code>bun run build</code> inside <code>dbstudio/</code>.</p></body></html>`)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(patchDBStudioHTML(index))
}

func dbStudioUIURL(port int) string {
	if port == 0 {
		port = dbstudio.DefaultPort
	}
	return "http://" + dbStudioUIAddr + "/?port=" + fmt.Sprint(port)
}

func dbStudioDirectURL(port int) string {
	if port == 0 {
		port = dbstudio.DefaultPort
	}
	return fmt.Sprintf("https://local.drizzle.studio/?port=%d", port)
}

func pathClean(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean("/" + path))
	if strings.Contains(cleaned, "..") {
		return "/"
	}
	return cleaned
}

func dbStudioUIBuildStale(uiRoot string) (bool, error) {
	return uiBuildStale(uiRoot, dbStudioUISourcePaths)
}

func dbStudioUIDepsStale(uiRoot string) (bool, error) {
	return uiDepsStale(uiRoot)
}

func dbStudioUIAssetsAvailable(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil
}

func patchDBStudioHTML(data []byte) []byte {
	data = stripDBStudioAnalytics(data)
	data = hideDBStudioSupportUI(data)
	return data
}

func stripDBStudioAnalytics(data []byte) []byte {
	const analytics = `<script defer="defer" data-site-id="local.drizzle.studio" src="https://assets.onedollarstats.com/stonks.js"></script>`
	return []byte(strings.ReplaceAll(string(data), analytics, ""))
}

func hideDBStudioSupportUI(data []byte) []byte {
	const marker = "</head>"
	const patch = `<style>[aria-label="Support Drizzle"],[aria-describedby="support-drizzle-dialog"]{display:none!important;visibility:hidden!important;pointer-events:none!important;}</style><script>(function(){function removeSupportDrizzle(){document.querySelectorAll('[aria-label="Support Drizzle"],[aria-describedby="support-drizzle-dialog"]').forEach(function(node){node.remove();});}if(document.readyState==="loading"){document.addEventListener("DOMContentLoaded",removeSupportDrizzle,{once:true});}else{removeSupportDrizzle();}new MutationObserver(removeSupportDrizzle).observe(document.documentElement,{childList:true,subtree:true});})();</script>`
	html := string(data)
	if strings.Contains(html, `[aria-label="Support Drizzle"]`) && strings.Contains(html, `MutationObserver(removeSupportDrizzle)`) {
		return data
	}
	if idx := strings.Index(strings.ToLower(html), marker); idx >= 0 {
		return []byte(html[:idx] + patch + html[idx:])
	}
	return []byte(patch + html)
}

func patchDBStudioAsset(name string, data []byte) []byte {
	return data
}
